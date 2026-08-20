package dispatch

import (
	"testing"

	"task137-gridflow/internal/domain"
)

func gview(id string, status domain.GenStatus, up, down int, pmin, pmax, ramp float64, isSlack bool) *GenView {
	return &GenView{
		ID: id, BusID: "B" + id, PMin: pmin, PMax: pmax, RampMW: ramp,
		MinUp: 1, MinDown: 1, QMin: -50, QMax: 50,
		Status: status, UpPeriods: up, DownPeriods: down, IsSlack: isSlack,
	}
}

func TestCanCommitMinDown(t *testing.T) {
	g := gview("G1", domain.GenOffline, 0, 2, 0, 100, 50, false)
	g.MinDown = 3
	if CanCommit(g) {
		t.Error("CanCommit should be false with down=2 < min_down=3")
	}
	g.DownPeriods = 3
	if !CanCommit(g) {
		t.Error("CanCommit should be true with down=3 >= min_down=3")
	}
}

func TestCanDecommitMinUp(t *testing.T) {
	g := gview("G1", domain.GenCommitted, 1, 0, 0, 100, 50, false)
	g.MinUp = 2
	if CanDecommit(g) {
		t.Error("CanDecommit should be false with up=1 < min_up=2")
	}
	g.UpPeriods = 2
	if !CanDecommit(g) {
		t.Error("CanDecommit should be true with up=2 >= min_up=2")
	}
}

func TestRampOK(t *testing.T) {
	g := gview("G1", domain.GenCommitted, 5, 0, 0, 100, 20, false)
	g.POutput = 50
	if !RampOK(g, 70) {
		t.Error("70 from 50 (Δ20) should be ok at ramp 20")
	}
	if RampOK(g, 71) {
		t.Error("71 from 50 (Δ21) should exceed ramp 20")
	}
	// negative direction
	if !RampOK(g, 30) {
		t.Error("30 from 50 (Δ20) should be ok")
	}
	if RampOK(g, 29) {
		t.Error("29 from 50 (Δ21) should exceed ramp 20")
	}
}

func TestPlanCapacityInsufficient(t *testing.T) {
	gens := []*GenView{
		gview("G1", domain.GenOffline, 0, 5, 0, 30, 30, false),
		gview("GS", domain.GenOffline, 0, 5, 0, 20, 20, true), // slack, 20 MW
	}
	// 100 MW load, only 50 MW available → insufficient
	p := Compute(PlanRequest{Gens: gens, TotalLoadMW: 100, ReserveReq: 5, LossFactor: 0.0})
	found := false
	for _, v := range p.Violations {
		if v.Kind == domain.ViolationInsuffCapacity {
			found = true
		}
	}
	if !found {
		t.Errorf("expected insufficient capacity, got %d violations", len(p.Violations))
	}
}

func TestPlanReserveInsufficient(t *testing.T) {
	gens := []*GenView{
		gview("G1", domain.GenOffline, 0, 5, 0, 60, 60, false),
		gview("GS", domain.GenOffline, 0, 5, 0, 60, 60, true),
	}
	// 80 MW load, 120 MW capacity → ok capacity but reserve = 120-80 = 40 < req 50
	p := Compute(PlanRequest{Gens: gens, TotalLoadMW: 80, ReserveReq: 50, LossFactor: 0.0})
	found := false
	for _, v := range p.Violations {
		if v.Kind == domain.ViolationInsuffReserve {
			found = true
		}
	}
	if !found {
		t.Errorf("expected insufficient reserve, got %d violations", len(p.Violations))
	}
}

func TestPlanStartsToCoverLoad(t *testing.T) {
	g1 := gview("G1", domain.GenOffline, 0, 5, 0, 60, 60, false)
	gs := gview("GS", domain.GenOffline, 0, 5, 0, 30, 30, true)
	p := Compute(PlanRequest{Gens: []*GenView{g1, gs}, TotalLoadMW: 70, ReserveReq: 10, LossFactor: 0.0})
	// G1 should be started (transition) to cover load beyond slack's 30 MW.
	found := false
	for _, tr := range p.Transitions {
		if tr.GenID == "G1" && tr.To == domain.GenCommitted {
			found = true
		}
	}
	if !found {
		t.Errorf("expected G1 start transition, got %+v", p.Transitions)
	}
	if p.OnlineCapacity < 70+10 {
		t.Errorf("online capacity %.1f should cover load+reserve 80", p.OnlineCapacity)
	}
}

func TestPlanSlackAbsorbsResidual(t *testing.T) {
	g1 := gview("G1", domain.GenCommitted, 5, 0, 0, 40, 40, false)
	gs := gview("GS", domain.GenOffline, 0, 5, 0, 200, 200, true)
	// 100 MW load; G1 covers up to 40, slack takes the rest (60).
	p := Compute(PlanRequest{Gens: []*GenView{g1, gs}, TotalLoadMW: 100, ReserveReq: 0, LossFactor: 0.0})
	if got := p.Outputs["G1"]; got != 40 {
		t.Errorf("G1 output=%.1f want 40 (full PMax)", got)
	}
	if got := p.Outputs["GS"]; got != 60 {
		t.Errorf("slack residual=%.1f want 60", got)
	}
}

func TestPlanSlackExceedsPMax(t *testing.T) {
	g1 := gview("G1", domain.GenCommitted, 5, 0, 0, 30, 30, false)
	gs := gview("GS", domain.GenOffline, 0, 5, 0, 40, 40, true) // slack 40 MW
	// 100 MW load, non-slack 30, slack would need 70 > 40 → violation
	p := Compute(PlanRequest{Gens: []*GenView{g1, gs}, TotalLoadMW: 100, ReserveReq: 0, LossFactor: 0.0})
	found := false
	for _, v := range p.Violations {
		if v.Kind == domain.ViolationInsuffCapacity && v.Ref == "GS" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected slack capacity violation, got %+v", p.Violations)
	}
}
