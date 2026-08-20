package service

import (
    "context"
    "testing"
"task137-gridflow/internal/domain"
	"task137-gridflow/internal/ybus"
)

func TestBug05_OutageRemovesBranchFromStateAndNetwork(t *testing.T) {
	svc, _ := newService(t)
	seedFiveBus(t, svc)
	ctx := context.Background()
	if err := svc.OutageBranch(ctx, "L2"); err != nil { t.Fatal(err) }
	branches, err := svc.store.ListBranches(ctx)
	if err != nil { t.Fatal(err) }
	for _, br := range branches { if br.ID == "L2" && br.InService { t.Error("L2 remains in service after outage") } }
	events, err := svc.store.ListEvents(ctx, 10)
	if err != nil { t.Fatal(err) }
	if len(events) == 0 || events[len(events)-1].Kind != domain.EventOutage { t.Errorf("last event=%v, want outage", events) }
	ym, err := ybus.Build([]string{"A","B"}, []domain.Branch{{ID:"off", FromBus:"A", ToBus:"B", X:0.1, InService:false}})
	if err != nil { t.Fatal(err) }
	if ybus.Abs(ym.Get(0,0)) != 0 { t.Errorf("out-of-service branch leaked into Ybus: %v", ym.Get(0,0)) }
}
