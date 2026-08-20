package service

import (
    "context"
    "testing"
"task137-gridflow/internal/domain"
)

func TestBug02_DispatchSnapshotKeepsSolvedTotalsAligned(t *testing.T) {
	svc, _ := newService(t)
	seedFiveBus(t, svc)
	ctx := context.Background()
	if err := svc.store.UpsertLoad(ctx, domain.Load{BusID: "B3", Period: 1, PMW: 80, QMvar: 20}); err != nil { t.Fatal(err) }
	live, err := svc.RunDispatch(ctx, 1)
	if err != nil { t.Fatal(err) }
	if live.TotalGen < 79 { t.Errorf("live generation=%v, want MW-scale total", live.TotalGen) }
	if live.TotalLoss > 10 { t.Errorf("live loss=%v, want uninflated loss", live.TotalLoss) }
	saved, err := svc.GetDispatch(ctx, 1)
	if err != nil { t.Fatal(err) }
	if saved.TotalGen < 79 { t.Errorf("saved generation=%v, want persisted MW-scale total", saved.TotalGen) }
}
