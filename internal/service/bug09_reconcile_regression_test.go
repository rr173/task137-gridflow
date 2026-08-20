package service

import (
    "context"
    "testing"
"task137-gridflow/internal/domain"
)

func TestBug09_ReconcileRetainsPersistedDispatchSnapshot(t *testing.T) {
	svc, _ := newService(t)
	seedFiveBus(t, svc)
	ctx := context.Background()
	if err := svc.store.UpsertLoad(ctx, domain.Load{BusID:"B3", Period:1, PMW:80, QMvar:20}); err != nil { t.Fatal(err) }
	if _, err := svc.RunDispatch(ctx, 1); err != nil { t.Fatal(err) }
	if _, err := svc.Reconcile(ctx); err != nil { t.Fatal(err) }
	got, err := svc.GetDispatch(ctx, 1)
	if err != nil { t.Fatal(err) }
	if got.TotalLoad != 80 { t.Errorf("reconciled load=%v, want 80", got.TotalLoad) }
	if len(got.Generators) < 2 { t.Errorf("reconciled generators=%v, want snapshot", got.Generators) }
	if len(got.Branches) < 2 { t.Errorf("reconciled branches=%v, want snapshot", got.Branches) }
	snap, err := svc.store.LoadAll(ctx)
	if err != nil { t.Fatal(err) }
	if len(snap.Branches) < 2 { t.Errorf("restart branches=%v, want topology snapshot", snap.Branches) }
}
