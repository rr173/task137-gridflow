package service

import (
    "context"
    "testing"
"task137-gridflow/internal/dispatch"
	"task137-gridflow/internal/domain"
)

func TestBug07_ReserveStartsExactBoundaryCapacity(t *testing.T) {
	gens := []*dispatch.GenView{{ID:"S", Status:domain.GenCommitted, PMax:100, IsSlack:true}, {ID:"R", Status:domain.GenOffline, PMax:100, MinDown:1, DownPeriods:1}}
	plan := dispatch.Compute(dispatch.PlanRequest{Gens:gens, TotalLoadMW:100, ReserveReq:100})
	if len(plan.Transitions) != 1 || plan.Transitions[0].GenID != "R" { t.Errorf("reserve plan=%+v, want R committed", plan.Transitions) }
	if !dispatch.CanCommit(gens[1]) { t.Error("exact min-down unit must be available for reserve") }
	svc, _ := newService(t)
	if _, err := svc.SeedPeriod(context.Background(), 0); err != nil { t.Fatal(err) }
	if err := svc.SetReserve(context.Background(), 1, 100); err != nil { t.Fatal(err) }
	_, req, _, err := svc.GetReserve(context.Background(), 1)
	if err != nil || req != 100 { t.Errorf("stored reserve requirement=%v, want 100 (err=%v)", req, err) }
}
