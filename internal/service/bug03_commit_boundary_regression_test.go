package service

import (
    "context"
    "testing"
"task137-gridflow/internal/dispatch"
	"task137-gridflow/internal/domain"
)

func TestBug03_CommitBoundaryInitializesPersistentCounters(t *testing.T) {
	if !dispatch.CanCommit(&dispatch.GenView{Status: domain.GenOffline, DownPeriods: 2, MinDown: 2}) { t.Fatal("exact min-down boundary must be committable") }
	svc, _ := newService(t)
	seedFiveBus(t, svc)
	ctx := context.Background()
	if err := svc.store.CreateGenerator(ctx, domain.Generator{ID:"G3", BusID:"B2", PMax:20, MinUp:1, MinDown:2, Status:domain.GenOffline, DownPeriods:2}); err != nil { t.Fatal(err) }
	if err := svc.CommitGenerator(ctx, "G3"); err != nil { t.Fatalf("commit exact boundary: %v", err) }
	g, err := svc.store.GetGenerator(ctx, "G3")
	if err != nil { t.Fatal(err) }
	if g.UpPeriods != 1 || g.DownPeriods != 0 { t.Errorf("counters after commit=(up=%d down=%d), want (1,0)", g.UpPeriods, g.DownPeriods) }
}
