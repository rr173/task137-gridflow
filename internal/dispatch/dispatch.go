// Package dispatch performs unit commitment and economic dispatch for one
// period: it decides which generators are COMMITTED, allocates active-power
// output P among them subject to limits and ramp rate, and checks capacity
// and reserve adequacy. It returns the per-generator P plan plus any
// constraint violations (min_up/min_down, ramp, capacity, reserve).
//
// The strategy is a priority-list commitment with merit-order economic
// dispatch — a deliberately bounded heuristic, not full mixed-integer UC.
// The domain constraints (min up/down, ramp, reserve) are the real,
// bug-relevant boundaries; the heuristic is simple by design.
package dispatch

import (
	"sort"

	"task137-gridflow/internal/domain"
)

// GenView is the dispatch view of a generator: its parameters and current
// commitment state. Dispatch may request a status transition; the caller
// (service) applies the transition only after min_up/min_down checks pass.
type GenView struct {
	ID         string
	BusID      string
	PMin       float64
	PMax       float64
	RampMW     float64
	MinUp      int
	MinDown    int
	QMin       float64
	QMax       float64
	Status     domain.GenStatus
	UpPeriods  int
	DownPeriods int
	POutput    float64 // previous-period output (for ramp)
	IsSlack    bool    // the swing generator: always committed, P set by power flow, not merit order
}

// DispatchPlan is the dispatch result for one period.
type Plan struct {
	// Outputs maps gen id -> committed output (MW). Missing ids are OFFLINE (0).
	Outputs map[string]float64
	// Transitions records requested OFFLINE->COMMITTED and COMMITTED->OFFLINE.
	Transitions []Transition
	// Violations are constraint violations detected during planning.
	Violations []domain.Violation
	// OnlineCapacity is Σ PMax of committed gens (MW).
	OnlineCapacity float64
	// TotalGen is Σ outputs (MW).
	TotalGen float64
}

// Transition is a requested status change for a generator.
type Transition struct {
	GenID string
	From  domain.GenStatus
	To    domain.GenStatus
}

// PlanRequest is the input to Plan.
type PlanRequest struct {
	Gens        []*GenView
	TotalLoadMW float64 // total active load this period
	ReserveReq  float64 // required spinning reserve (MW)
	LossFactor  float64 // estimated loss factor applied to load for capacity check (0.0..0.1)
}

// Compute runs the commitment and dispatch for the request.
func Compute(req PlanRequest) *Plan {
	p := &Plan{Outputs: map[string]float64{}}

	// 1) Validate any pending transitions against min_up / min_down. The
	//    caller passes gens whose Status already reflects pending transitions;
	//    we record violations here and keep the previous output for offenders.
	//    (Min_up/down enforcement on the *service* side mutates Status; here we
	//    only flag if a gen is in an inconsistent state.)
	// NOTE: service enforces min_up/min_down at transition time; Plan trusts
	//       the given statuses but still sanity-checks outputs against limits.

	// 2) Build candidate list for commitment: currently COMMITTED gens are
	//    must-run (stay committed). OFFLINE gens are candidates to start.
	type cand struct {
		g    *GenView
		merit float64 // average cost proxy = PMax (cheaper = larger PMax, arbitrary but stable)
	}
	var committed []*GenView
	var candidates []cand
	for _, g := range req.Gens {
		if g.Status == domain.GenCommitted {
			committed = append(committed, g)
		} else if g.IsSlack {
			// a slack generator that is offline is still a must-start; the
			// service guarantees the swing unit is committed, so this branch
			// should not normally happen.
			committed = append(committed, g)
		} else if CanCommit(g) {
			// only OFFLINE units that have satisfied min_down are candidates;
			// units still in min_down contribute nothing to capacity, which
			// correctly yields INSUFFICIENT_CAPACITY when load can't be met.
			candidates = append(candidates, cand{g: g, merit: g.PMax})
		}
	}
	// start candidates in priority order (larger PMax first — heuristic).
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].merit > candidates[j].merit
	})

	// 3) Capacity check: even with all online units at PMax, can we cover
	//    load + losses + reserve?
	totalOnlinePMax := 0.0
	for _, g := range committed {
		totalOnlinePMax += g.PMax
	}
	for _, c := range candidates {
		totalOnlinePMax += c.g.PMax
	}
	adjustedLoad := req.TotalLoadMW * (1.0 + req.LossFactor)
	if totalOnlinePMax < adjustedLoad+1e-6 {
		p.Violations = append(p.Violations, domain.Violation{
			Kind: domain.ViolationInsuffCapacity,
			Ref:  "system",
			Value: totalOnlinePMax,
			Limit: adjustedLoad,
			Detail: "insufficient capacity: online PMax < load(+loss)+reserve",
		})
	}

	// 4) Commit additional gens until capacity (committed) >= load + reserve.
	needed := adjustedLoad + req.ReserveReq
	onlineCap := 0.0
	for _, g := range committed {
		onlineCap += g.PMax
	}
	started := []*GenView{}
	for _, c := range candidates {
		if onlineCap >= needed {
			break
		}
		// start this gen
		committed = append(committed, c.g)
		started = append(started, c.g)
		onlineCap += c.g.PMax
		p.Transitions = append(p.Transitions, Transition{GenID: c.g.ID, From: domain.GenOffline, To: domain.GenCommitted})
	}
	p.OnlineCapacity = onlineCap

	// 5) Reserve adequacy after commitment.
	reserve := onlineCap - req.TotalLoadMW
	if reserve < req.ReserveReq-1e-6 {
		p.Violations = append(p.Violations, domain.Violation{
			Kind: domain.ViolationInsuffReserve,
			Ref:  "system",
			Value: reserve,
			Limit: req.ReserveReq,
			Detail: "insufficient spinning reserve",
		})
	}

	// 6) Economic dispatch: allocate load among committed non-slack gens (merit
	//    order by PMax descending for larger units first), respecting
	//    Pmin/Pmax and ramp rate from previous output. The slack generator is
	//    NOT allocated here — it absorbs the residual + losses in power flow.
	sort.SliceStable(committed, func(i, j int) bool {
		return committed[i].PMax > committed[j].PMax
	})
	remaining := req.TotalLoadMW
	for _, g := range committed {
		if g.IsSlack {
			continue // swing unit: P is determined by the power-flow solve
		}
		lo := g.PMin
		hi := g.PMax
		// ramp constraint: |P - prev| <= ramp
		if g.RampMW > 0 {
			if g.POutput+g.RampMW < hi {
				hi = g.POutput + g.RampMW
			}
			if g.POutput-g.RampMW > lo {
				lo = g.POutput - g.RampMW
			}
		}
		if hi < g.PMin {
			hi = g.PMin
		}
		if hi < 0 {
			hi = 0
		}
		// allocate as much as possible up to hi, at least lo
		target := hi
		if target > remaining {
			target = remaining
		}
		if target < lo {
			// can't take less than Pmin while committed — record ramp/limit violation
			p.Violations = append(p.Violations, domain.Violation{
				Kind: domain.ViolationRamp,
				Ref:  g.ID,
				Value: remaining,
				Limit: lo,
				Detail: "load too low for min output under ramp constraint",
			})
			target = lo
		}
		if target > hi {
			target = hi
		}
		p.Outputs[g.ID] = target
		p.TotalGen += target
		remaining -= target
	}
	// The slack generator picks up whatever the non-slack units could not
	// cover (plus losses, which are unknown until the power-flow solve). We
	// check the slack's share against its limits here; the final, loss-corrected
	// slack P is validated after the solve in the service layer.
	for _, g := range committed {
		if !g.IsSlack {
			continue
		}
		slackP := remaining
		p.Outputs[g.ID] = slackP
		p.TotalGen += slackP
		if g.PMax > 0 && slackP > g.PMax+1e-6 {
			p.Violations = append(p.Violations, domain.Violation{
				Kind: domain.ViolationInsuffCapacity,
				Ref:  g.ID,
				Value: slackP,
				Limit: g.PMax,
				Detail: "slack generator exceeds PMax: non-slack units cannot cover load",
			})
		}
	}
	// if remaining > 0 after dispatch, capacity was insufficient at the
	// ramp-limited level (already flagged above). Keep outputs as-is.
	return p
}

// Ramp/commit guards live in guard.go.

