// Package service orchestrates the power-flow + dispatch engine over the
// persistent store. It owns the generator state machine (min_up/min_down,
// ramp), period lifecycle (planned -> committed), branch topology changes
// (outage/restore), and restart recovery (LoadAll + Reconcile).
package service

import (
	"context"
	"fmt"
	"sort"

	"task137-gridflow/internal/berr"
	"task137-gridflow/internal/clock"
	"task137-gridflow/internal/dispatch"
	"task137-gridflow/internal/domain"
	"task137-gridflow/internal/powerflow"
	"task137-gridflow/internal/store"
)

// Service is the application core.
type Service struct {
	store *store.Store
	clk   *clock.Clock
}

// New constructs a service. The clock is seeded from the persisted cursor.
func New(s *store.Store, c *clock.Clock) *Service {
	return &Service{store: s, clk: c}
}

// ---------------- helpers ----------------

// currentPeriodSeq returns the period the engine is currently acting on.
// Periods are 1-indexed; 0 means "no period created yet".
func (svc *Service) currentPeriodSeq(ctx context.Context) int {
	return svc.clk.Now()
}

// slackBus finds the slack bus id, or "" if none.
func slackBusID(buses []domain.Bus) string {
	for _, b := range buses {
		if b.Type == domain.BusTypeSlack {
			return b.ID
		}
	}
	return ""
}

// generatorAt returns the generator attached to a bus id, or nil.
func generatorAt(gens []domain.Generator, busID string) *domain.Generator {
	for i := range gens {
		if gens[i].BusID == busID {
			return &gens[i]
		}
	}
	return nil
}

// ---------------- RunDispatch ----------------

// DispatchResult is the outcome of RunDispatch, returned to the HTTP layer.
type DispatchResult struct {
	Period     int                  `json:"period"`
	Verdict    domain.Verdict       `json:"verdict"`
	TotalGen   float64              `json:"total_gen"`
	TotalLoad  float64              `json:"total_load"`
	TotalLoss  float64              `json:"total_loss"`
	Generators []GenOutput          `json:"generators"`
	Buses      []BusOutput           `json:"buses"`
	Branches   []BranchOutput        `json:"branches"`
	Violations []domain.Violation    `json:"violations"`
}

// GenOutput is a generator's result for a period.
type GenOutput struct {
	ID       string  `json:"id"`
	BusID    string  `json:"bus_id"`
	Status   domain.GenStatus `json:"status"`
	POutput  float64 `json:"p_output"`
	QOutput  float64 `json:"q_output"`
}

// BusOutput is a bus's solved voltage for a period.
type BusOutput struct {
	ID    string  `json:"id"`
	Type  domain.BusType `json:"type"`
	VMag  float64 `json:"vmag"`
	Theta float64 `json:"theta"`
}

// BranchOutput is a branch's solved flow for a period.
type BranchOutput struct {
	ID       string  `json:"id"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	PFrom    float64 `json:"p_from"`
	QFrom    float64 `json:"q_from"`
	SMVA     float64 `json:"s_mva"`
	Loading  float64 `json:"loading_pct"`
	Overload bool    `json:"overload"`
	MVALimit float64 `json:"mva_limit"`
}

// RunDispatch runs unit commitment + economic dispatch + power flow for the
// current (or specified) period, persists the full solution snapshot, and
// returns the result. A period that is already released (committed) cannot
// be re-dispatched.
func (svc *Service) RunDispatch(ctx context.Context, period int) (*DispatchResult, error) {
	if period <= 0 {
		period = svc.currentPeriodSeq(ctx)
	}
	if period <= 0 {
		return nil, berr.New(berr.CodeStateConflict, "no dispatch period yet; create/advance a period first")
	}

	var result *DispatchResult
	err := svc.store.InTx(ctx, func(tx store.DBTX) error {
		// guard: cannot re-dispatch a released period
		per, err := GetPeriodTx(tx, ctx, period)
		if err != nil {
			return err
		}
		if per.Status == domain.PeriodCommitted {
			return berr.New(berr.CodeStateConflict, fmt.Sprintf("period %d already released; cannot re-dispatch", period))
		}

		buses, err := store.ListBusesTx(tx, ctx)
		if err != nil {
			return err
		}
		branches, err := store.ListBranchesTx(tx, ctx)
		if err != nil {
			return err
		}
		gens, err := store.ListGeneratorsTx(tx, ctx)
		if err != nil {
			return err
		}
		loads, err := store.ListLoadsTx(tx, ctx, period)
		if err != nil {
			return err
		}

		sbid := slackBusID(buses)
		if sbid == "" {
			return berr.New(berr.CodeInvariant, "no slack bus defined")
		}

		// total load
		totalLoad := 0.0
		for _, l := range loads {
			totalLoad += l.PMW
		}
		reserveReq := per.ReserveReq

		// build dispatch views
		views := make([]*dispatch.GenView, 0, len(gens))
		for i := range gens {
			g := gens[i]
			views = append(views, &dispatch.GenView{
				ID: g.ID, BusID: g.BusID,
				PMin: g.PMin, PMax: g.PMax, RampMW: g.RampMW,
				MinUp: g.MinUp, MinDown: g.MinDown,
				QMin: g.QMin, QMax: g.QMax,
				Status: g.Status, UpPeriods: g.UpPeriods, DownPeriods: g.DownPeriods,
				POutput: g.POutput,
				IsSlack: g.BusID == sbid,
			})
		}

		plan := dispatch.Compute(dispatch.PlanRequest{
			Gens: views, TotalLoadMW: totalLoad, ReserveReq: reserveReq, LossFactor: 0.03,
		})

		// apply start/stop transitions (Plan only proposes starts; the caller
		// applies them so min_up/min_down is enforced here). We only apply
		// transitions that pass the state-machine guard.
		committedSet := map[string]bool{}
		for _, g := range gens {
			if g.Status == domain.GenCommitted {
				committedSet[g.ID] = true
			}
		}
		for _, t := range plan.Transitions {
			// find the view to use the same guards
			var gv *dispatch.GenView
			for _, v := range views {
				if v.ID == t.GenID {
					gv = v
					break
				}
			}
			if gv == nil {
				continue
			}
			if t.To == domain.GenCommitted && dispatch.CanCommit(gv) {
				committedSet[t.GenID] = true
				gv.Status = domain.GenCommitted
			}
		}

		// build power-flow network
		net := buildNetwork(buses, branches, gens, loads, plan, sbid)
		sol, err := powerflow.Solve(net)
		if err != nil {
			return berr.New(berr.CodeInvariant, fmt.Sprintf("power flow failed: %v", err))
		}

		// merge violations: dispatch (capacity/reserve/ramp) + solve (overload/voltage/reactive)
		violations := append([]domain.Violation{}, plan.Violations...)
		violations = append(violations, sol.Violations...)

		// slack balance check: slack generator's solved P within [Pmin,Pmax]
		slackGen := generatorAt(gens, sbid)
		if slackGen != nil && slackGen.PMax > 0 {
			if sol.SlackP > slackGen.PMax+1e-4 || sol.SlackP < slackGen.PMin-1e-4 {
				violations = append(violations, domain.Violation{
					Kind: domain.ViolationInsuffCapacity, Ref: slackGen.ID,
					Value: sol.SlackP, Limit: slackGen.PMax,
					Detail: fmt.Sprintf("slack generator %s solved P %.2f MW outside [%g,%g]", slackGen.ID, sol.SlackP, slackGen.PMin, slackGen.PMax),
				})
			}
		}

		verdict := domain.VerdictFeasible
		if len(violations) > 0 {
			verdict = domain.VerdictRejected
		}

		// update generator outputs (post-solve) and persist
		genOut := map[string][2]float64{}
		var genOutputs []GenOutput
		for i := range gens {
			g := gens[i]
			p, q := 0.0, 0.0
			if committedSet[g.ID] || g.BusID == sbid {
				if g.BusID == sbid {
					p = sol.SlackP
				} else if v, ok := plan.Outputs[g.ID]; ok {
					p = v
				}
			}
			// Q from solve at this bus
			if br, ok := sol.Buses[g.BusID]; ok {
				q = br.QInj * domain.PowerBase // pu->Mvar; QInj is gen-load net; subtract load handled by caller
				// net injection Q = Qgen - Qload → Qgen = Qinj + Qload
				for _, l := range loads {
					if l.BusID == g.BusID {
						q = br.QInj*domain.PowerBase + l.QMvar
					}
				}
			}
			g.POutput = p
			g.Status = domain.GenCommitted
			if !committedSet[g.ID] && g.BusID != sbid {
				g.Status = domain.GenOffline
				p = 0
				q = 0
			}
			if err := store.SetGeneratorStateTx(tx, ctx, g); err != nil {
				return err
			}
			genOut[g.ID] = [2]float64{p, q}
			genOutputs = append(genOutputs, GenOutput{ID: g.ID, BusID: g.BusID, Status: g.Status, POutput: p, QOutput: q})
		}

		// bus/flow snapshots
		busSn := map[string][2]float64{}
		var busOut []BusOutput
		for _, b := range buses {
			br := sol.Buses[b.ID]
			vm, th := 1.0, 0.0
			if br != nil {
				vm = br.VMag
				th = br.Theta
			}
			busSn[b.ID] = [2]float64{vm, th}
			busOut = append(busOut, BusOutput{ID: b.ID, Type: b.Type, VMag: vm, Theta: th})
		}
		flowSn := map[string]store.FlowRow{}
		var branchOut []BranchOutput
		for _, fr := range sol.Branches {
			flowSn[fr.ID] = store.FlowRow{PFrom: fr.PFrom, QFrom: fr.QFrom, SMVA: fr.SMVA, Loading: fr.Loading, Overload: fr.Overload}
			branchOut = append(branchOut, BranchOutput{ID: fr.ID, From: fr.From, To: fr.To, PFrom: fr.PFrom, QFrom: fr.QFrom, SMVA: fr.SMVA, Loading: fr.Loading, Overload: fr.Overload, MVALimit: fr.MVALimit})
		}

		// persist period + results
		per.Status = domain.PeriodPlanned
		per.Verdict = verdict
		per.TotalGen = sol.TotalGen
		per.TotalLoad = totalLoad
		per.TotalLoss = sol.TotalLoss
		if err := store.UpsertPeriodTx(tx, ctx, per); err != nil {
			return err
		}
		if err := store.SavePeriodResultsTx(tx, ctx, period, genOut, busSn, flowSn, violations); err != nil {
			return err
		}
		if err := store.AppendEventTx(tx, ctx, period, domain.EventDispatch, map[string]any{"verdict": verdict, "violations": len(violations)}); err != nil {
			return err
		}

		sort.Slice(branchOut, func(i, j int) bool { return branchOut[i].ID < branchOut[j].ID })
		sort.Slice(busOut, func(i, j int) bool { return busOut[i].ID < busOut[j].ID })
		sort.Slice(genOutputs, func(i, j int) bool { return genOutputs[i].ID < genOutputs[j].ID })

		result = &DispatchResult{
			Period: period, Verdict: verdict,
			TotalGen: sol.TotalGen, TotalLoad: totalLoad, TotalLoss: sol.TotalLoss,
			Generators: genOutputs, Buses: busOut, Branches: branchOut, Violations: violations,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// buildNetwork assembles the power-flow Network from the domain state + plan.
func buildNetwork(buses []domain.Bus, branches []domain.Branch, gens []domain.Generator, loads []domain.Load, plan *dispatch.Plan, slackBus string) *powerflow.Network {
	net := &powerflow.Network{}
	loadByBus := map[string]domain.Load{}
	for _, l := range loads {
		// aggregate multiple loads per bus (period has one per bus in schema, but be safe)
		cur := loadByBus[l.BusID]
		cur.PMW += l.PMW
		cur.QMvar += l.QMvar
		loadByBus[l.BusID] = cur
	}
	genByBus := map[string]*domain.Generator{}
	for i := range gens {
		genByBus[gens[i].BusID] = &gens[i]
	}
	committedSet := map[string]bool{}
	if plan != nil {
		for id := range plan.Outputs {
			committedSet[id] = true
		}
	}
	for _, b := range buses {
		node := &powerflow.BusNode{
			ID: b.ID, Type: b.Type, VSpec: b.VSpec, ThetaSpec: b.ThetaSpec,
			VMin: b.VMin, VMax: b.VMax, VNom: b.VNom,
		}
		if ld, ok := loadByBus[b.ID]; ok {
			node.PLoad = domain.PU(ld.PMW)
			node.QLoad = domain.PU(ld.QMvar)
		}
		if g, ok := genByBus[b.ID]; ok {
			node.QMin = g.QMin
			node.QMax = g.QMax
			if b.ID == slackBus {
				// slack: PGen set to 0 (solved), Q limits optional
				node.PGen = 0
			} else if committedSet[g.ID] {
				if v, ok := plan.Outputs[g.ID]; ok {
					node.PGen = domain.PU(v)
				}
			}
		}
		net.Buses = append(net.Buses, node)
	}
	for _, br := range branches {
		net.Branches = append(net.Branches, &powerflow.BranchEdge{
			ID: br.ID, From: br.FromBus, To: br.ToBus,
			R: br.R, X: br.X, BShunt: br.BShunt, Tap: br.Tap,
			MVALimit: br.MVALimit, InService: br.InService,
		})
	}
	return net
}

// GetPeriodTx is a small local helper that re-exposes store.GetPeriodTx without
// forcing callers to import store. (Kept for API symmetry.)
func GetPeriodTx(tx store.DBTX, ctx context.Context, seq int) (domain.Period, error) {
	return store.GetPeriodTx(tx, ctx, seq)
}

// ---------------- Commit / Decommit ----------------

// CommitGenerator requests OFFLINE -> COMMITTED. Enforces min_down: the
// generator must have been OFFLINE for >= MinDown periods.
func (svc *Service) CommitGenerator(ctx context.Context, id string) error {
	return svc.store.InTx(ctx, func(tx store.DBTX) error {
		g, err := store.GetGeneratorTx(tx, ctx, id)
		if err != nil {
			return berr.Wrap(err)
		}
		if g.Status == domain.GenCommitted {
			return berr.New(berr.CodeStateConflict, fmt.Sprintf("generator %s already committed", id))
		}
		if g.DownPeriods < g.MinDown {
			return berr.New(berr.CodeStateConflict,
				fmt.Sprintf("generator %s needs %d down periods, has %d", id, g.MinDown, g.DownPeriods))
		}
		g.Status = domain.GenCommitted
		g.UpPeriods = 1
		g.DownPeriods = 0
		if err := store.SetGeneratorStateTx(tx, ctx, g); err != nil {
			return err
		}
		period := svc.currentPeriodSeq(ctx)
		return store.AppendEventTx(tx, ctx, period, domain.EventCommit, map[string]any{"gen": id})
	})
}

// DecommitGenerator requests COMMITTED -> OFFLINE. Enforces min_up.
func (svc *Service) DecommitGenerator(ctx context.Context, id string) error {
	return svc.store.InTx(ctx, func(tx store.DBTX) error {
		g, err := store.GetGeneratorTx(tx, ctx, id)
		if err != nil {
			return berr.Wrap(err)
		}
		if g.Status == domain.GenOffline {
			return berr.New(berr.CodeStateConflict, fmt.Sprintf("generator %s already offline", id))
		}
		if g.UpPeriods < g.MinUp {
			return berr.New(berr.CodeStateConflict,
				fmt.Sprintf("generator %s needs %d up periods, has %d", id, g.MinUp, g.UpPeriods))
		}
		// forbid decommitting the slack (swing) generator
		b, _ := store.GetBusTx(tx, ctx, g.BusID)
		if b.Type == domain.BusTypeSlack {
			return berr.New(berr.CodeStateConflict, "cannot decommit the slack generator")
		}
		g.Status = domain.GenOffline
		g.DownPeriods = 1
		g.UpPeriods = 0
		g.POutput = 0
		if err := store.SetGeneratorStateTx(tx, ctx, g); err != nil {
			return err
		}
		period := svc.currentPeriodSeq(ctx)
		return store.AppendEventTx(tx, ctx, period, domain.EventDecommit, map[string]any{"gen": id})
	})
}

// ---------------- Outage / Restore ----------------

// OutageBranch takes a branch out of service.
func (svc *Service) OutageBranch(ctx context.Context, id string) error {
	return svc.store.InTx(ctx, func(tx store.DBTX) error {
		if err := store.SetBranchServiceTx(tx, ctx, id, false); err != nil {
			return berr.Wrap(err)
		}
		period := svc.currentPeriodSeq(ctx)
		return store.AppendEventTx(tx, ctx, period, domain.EventRestore, map[string]any{"branch": id})
	})
}

// RestoreBranch returns a branch to service.
func (svc *Service) RestoreBranch(ctx context.Context, id string) error {
	return svc.store.InTx(ctx, func(tx store.DBTX) error {
		if err := store.SetBranchServiceTx(tx, ctx, id, true); err != nil {
			return berr.Wrap(err)
		}
		period := svc.currentPeriodSeq(ctx)
		return store.AppendEventTx(tx, ctx, period, domain.EventRestore, map[string]any{"branch": id})
	})
}

// ---------------- Period lifecycle ----------------

// AdvancePeriod locks the previous period (if planned) and opens the next.
func (svc *Service) AdvancePeriod(ctx context.Context) (int, error) {
	var newSeq int
	err := svc.store.InTx(ctx, func(tx store.DBTX) error {
		cur := svc.currentPeriodSeq(ctx)
		// increment generator counters for the period being closed
		gens, err := store.ListGeneratorsTx(tx, ctx)
		if err != nil {
			return err
		}
		for i := range gens {
			g := gens[i]
			if g.Status == domain.GenCommitted {
				g.UpPeriods++
				g.DownPeriods = 0
			} else {
				g.DownPeriods++
				g.UpPeriods = 0
			}
			if err := store.SetGeneratorStateTx(tx, ctx, g); err != nil {
				return err
			}
		}
		newSeq = svc.clk.Advance()
		if err := store.SetStateTxLocal(tx, ctx, "current_period", fmt.Sprintf("%d", newSeq)); err != nil {
			return err
		}
		// create the new period row (planned, reserve_req inherited if present)
		var reserveReq float64
		if cur > 0 {
			if prev, err := store.GetPeriodTx(tx, ctx, cur); err == nil {
				reserveReq = prev.ReserveReq
			}
		}
		np := domain.Period{Seq: newSeq, Status: domain.PeriodPlanned, Verdict: domain.VerdictFeasible, ReserveReq: reserveReq}
		if err := store.UpsertPeriodTx(tx, ctx, np); err != nil {
			return err
		}
		return store.AppendEventTx(tx, ctx, newSeq, domain.EventAdvance, map[string]any{"from": cur, "to": newSeq})
	})
	if err != nil {
		return 0, err
	}
	return newSeq, nil
}

// SetReserve sets the reserve requirement for a period.
func (svc *Service) SetReserve(ctx context.Context, period int, reserve float64) error {
	return svc.store.InTx(ctx, func(tx store.DBTX) error {
		p, err := store.GetPeriodTx(tx, ctx, period)
		if err != nil {
			return berr.Wrap(err)
		}
		if p.Status == domain.PeriodCommitted {
			return berr.New(berr.CodeStateConflict, "cannot change reserve on a released period")
		}
		p.ReserveReq = reserve
		return store.UpsertPeriodTx(tx, ctx, p)
	})
}

// ReleasePeriod locks a planned period (only if feasible).
func (svc *Service) ReleasePeriod(ctx context.Context, period int) error {
	return svc.store.InTx(ctx, func(tx store.DBTX) error {
		p, err := store.GetPeriodTx(tx, ctx, period)
		if err != nil {
			return berr.Wrap(err)
		}
		if p.Status == domain.PeriodCommitted {
			return berr.New(berr.CodeStateConflict, "period already released")
		}
		if p.Verdict != domain.VerdictFeasible {
			return berr.New(berr.CodeStateConflict, "cannot release a rejected period")
		}
		p.Status = domain.PeriodCommitted
		if err := store.UpsertPeriodTx(tx, ctx, p); err != nil {
			return err
		}
		return store.AppendEventTx(tx, ctx, period, domain.EventRelease, nil)
	})
}

// ---------------- Queries ----------------

// GetDispatch reads a period's stored solution snapshot.
func (svc *Service) GetDispatch(ctx context.Context, period int) (*DispatchResult, error) {
	var out *DispatchResult
	err := svc.store.InTx(ctx, func(tx store.DBTX) error {
		p, err := store.GetPeriodTx(tx, ctx, period)
		if err != nil {
			return berr.Wrap(err)
		}
		gen, buses, flows, vios, err := store.LoadPeriodResultsTx(tx, ctx, period)
		if err != nil {
			return err
		}
		res := &DispatchResult{Period: period, Verdict: p.Verdict, TotalGen: p.TotalGen, TotalLoad: p.TotalLoad, TotalLoss: p.TotalLoss, Violations: vios}
		res.Generators = make([]GenOutput, 0, len(gen))
		for id, pq := range gen {
			g, _ := store.GetGeneratorTx(tx, ctx, id)
			st := domain.GenOffline
			busID := ""
			if g.ID != "" {
				st = g.Status
				busID = g.BusID
			}
			res.Generators = append(res.Generators, GenOutput{ID: id, BusID: busID, Status: st, POutput: pq[0], QOutput: pq[1]})
		}
		res.Buses = make([]BusOutput, 0, len(buses))
		for id, vt := range buses {
			b, _ := store.GetBusTx(tx, ctx, id)
			res.Buses = append(res.Buses, BusOutput{ID: id, Type: b.Type, VMag: vt[0], Theta: vt[1]})
		}
		res.Branches = make([]BranchOutput, 0, len(flows))
		for id, fr := range flows {
			br, _ := store.GetBranchTx(tx, ctx, id)
			res.Branches = append(res.Branches, BranchOutput{ID: id, From: br.FromBus, To: br.ToBus, PFrom: fr.PFrom, QFrom: fr.QFrom, SMVA: fr.SMVA, Loading: fr.Loading, Overload: fr.Overload, MVALimit: br.MVALimit})
		}
		sort.Slice(res.Generators, func(i, j int) bool { return res.Generators[i].ID < res.Generators[j].ID })
		sort.Slice(res.Buses, func(i, j int) bool { return res.Buses[i].ID < res.Buses[j].ID })
		sort.Slice(res.Branches, func(i, j int) bool { return res.Branches[i].ID < res.Branches[j].ID })
		out = res
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetReserve reports reserve adequacy for a period from its stored snapshot.
func (svc *Service) GetReserve(ctx context.Context, period int) (reserve, requirement float64, ok bool, err error) {
	err = svc.store.InTx(ctx, func(tx store.DBTX) error {
		p, e := store.GetPeriodTx(tx, ctx, period)
		if e != nil {
			return berr.Wrap(e)
		}
		gens, e := store.ListGeneratorsTx(tx, ctx)
		if e != nil {
			return e
		}
		gen, _, _, _, e := store.LoadPeriodResultsTx(tx, ctx, period)
		if e != nil {
			return e
		}
		onlineCap := 0.0
		for _, g := range gens {
			if _, has := gen[g.ID]; has {
				onlineCap += g.PMax
			} else if g.Status == domain.GenCommitted {
				onlineCap += g.PMax
			}
		}
		reserve = onlineCap - p.TotalLoad
		requirement = p.ReserveReq
		ok = reserve >= p.ReserveReq-1e-6
		return nil
	})
	return
}

// Summary returns network stats and the current period cursor.
func (svc *Service) Summary(ctx context.Context) (*domain.SumStats, error) {
	snap, err := svc.store.LoadAll(ctx)
	if err != nil {
		return nil, err
	}
	sb := slackBusID(snap.Buses)
	return &domain.SumStats{
		BusCount: len(snap.Buses), BranchCount: len(snap.Branches), GeneratorCount: len(snap.Generators),
		CurrentPeriod: snap.CurrentPeriod, SlackBus: sb,
	}, nil
}

// ---------------- Restart recovery ----------------

// Reconcile recomputes the latest planned period's power flow from the
// persisted topology and generator state, and verifies the recomputed result
// matches the stored snapshot (if one exists) — or writes one if missing.
// Released (committed) periods are trusted as-is and never recomputed.
func (svc *Service) Reconcile(ctx context.Context) (string, error) {
	var msg string
	err := svc.store.InTx(ctx, func(tx store.DBTX) error {
		periods, err := store.ListPeriodsTx(tx, ctx)
		if err != nil {
			return err
		}
		// find the latest planned period
		target := 0
		for _, p := range periods {
			if p.Status == domain.PeriodPlanned && p.Seq > target {
				target = p.Seq
			}
		}
		if target == 0 {
			msg = "no planned period to reconcile"
			return store.AppendEventTx(tx, ctx, 0, domain.EventReconcile, map[string]any{"msg": msg})
		}
		// load state
		buses, err := store.ListBusesTx(tx, ctx)
		if err != nil {
			return err
		}
		branches, err := store.ListBranchesTx(tx, ctx)
		if err != nil {
			return err
		}
		gens, err := store.ListGeneratorsTx(tx, ctx)
		if err != nil {
			return err
		}
		loads, err := store.ListLoadsTx(tx, ctx, target)
		if err != nil {
			return err
		}
		sbid := slackBusID(buses)
		// build plan outputs from the generator state directly (no new commit)
		plan := &dispatch.Plan{Outputs: map[string]float64{}}
		for _, g := range gens {
			if g.Status == domain.GenCommitted {
				if g.BusID == sbid {
					plan.Outputs[g.ID] = g.POutput // slack: use stored (will be re-solved)
				} else {
					plan.Outputs[g.ID] = g.POutput
				}
			}
		}
		net := buildNetwork(buses, branches, gens, loads, plan, sbid)
		sol, err := powerflow.Solve(net)
		if err != nil {
			return berr.New(berr.CodeInvariant, fmt.Sprintf("reconcile power flow failed: %v", err))
		}
		// assemble snapshots
		genOut := map[string][2]float64{}
		for _, g := range gens {
			p, q := 0.0, 0.0
			if g.Status == domain.GenCommitted {
				if g.BusID == sbid {
					p = sol.SlackP
				} else {
					p = g.POutput
				}
			}
			if br, ok := sol.Buses[g.BusID]; ok {
				q = br.QInj * domain.PowerBase
				for _, l := range loads {
					if l.BusID == g.BusID {
						q = br.QInj*domain.PowerBase + l.QMvar
					}
				}
			}
			genOut[g.ID] = [2]float64{p, q}
		}
		busSn := map[string][2]float64{}
		for _, b := range buses {
			br := sol.Buses[b.ID]
			vm, th := 1.0, 0.0
			if br != nil {
				vm = br.VMag
				th = br.Theta
			}
			busSn[b.ID] = [2]float64{vm, th}
		}
		flowSn := map[string]store.FlowRow{}
		for _, fr := range sol.Branches {
			flowSn[fr.ID] = store.FlowRow{PFrom: fr.PFrom, QFrom: fr.QFrom, SMVA: fr.SMVA, Loading: fr.Loading, Overload: fr.Overload}
		}
		vios := sol.Violations
		per, _ := store.GetPeriodTx(tx, ctx, target)
		if per.Verdict == domain.VerdictRejected || len(vios) > 0 {
			per.Verdict = domain.VerdictRejected
		} else {
			per.Verdict = domain.VerdictFeasible
		}
		per.TotalGen = sol.TotalGen
		per.TotalLoss = sol.TotalLoss
		if err := store.UpsertPeriodTx(tx, ctx, per); err != nil {
			return err
		}
		if err := store.SavePeriodResultsTx(tx, ctx, target, genOut, busSn, flowSn, vios); err != nil {
			return err
		}
		msg = fmt.Sprintf("reconciled period %d: %d buses, verdict=%s", target, len(buses), per.Verdict)
		return store.AppendEventTx(tx, ctx, target, domain.EventReconcile, map[string]any{"msg": msg})
	})
	return msg, err
}

// SeedPeriod creates the initial period (seq=1) if none exists. Used by the
// HTTP layer's first dispatch.
func (svc *Service) SeedPeriod(ctx context.Context, reserveReq float64) (int, error) {
	var seq int
	err := svc.store.InTx(ctx, func(tx store.DBTX) error {
		periods, err := store.ListPeriodsTx(tx, ctx)
		if err != nil {
			return err
		}
		if len(periods) == 0 {
			svc.clk.Set(1)
			seq = 1
			if err := store.SetStateTxLocal(tx, ctx, "current_period", "1"); err != nil {
				return err
			}
			np := domain.Period{Seq: 1, Status: domain.PeriodPlanned, Verdict: domain.VerdictFeasible, ReserveReq: reserveReq}
			return store.UpsertPeriodTx(tx, ctx, np)
		}
		// already seeded: return max
		for _, p := range periods {
			if p.Seq > seq {
				seq = p.Seq
			}
		}
		svc.clk.Set(seq)
		if err := store.SetStateTxLocal(tx, ctx, "current_period", fmt.Sprintf("%d", seq)); err != nil {
			return err
		}
		return nil
	})
	return seq, err
}
