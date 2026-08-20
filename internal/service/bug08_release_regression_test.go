package service

import (
    "context"
    "testing"
"task137-gridflow/internal/domain"
	"task137-gridflow/internal/store"
)

func TestBug08_ReleaseLocksPeriodAndAuditsCommitState(t *testing.T) {
	svc, _ := newService(t)
	seedFiveBus(t, svc)
	ctx := context.Background()
	if err := svc.store.UpsertLoad(ctx, domain.Load{BusID:"B3", Period:1, PMW:80, QMvar:20}); err != nil { t.Fatal(err) }
	if _, err := svc.RunDispatch(ctx, 1); err != nil { t.Fatal(err) }
	if err := svc.ReleasePeriod(ctx, 1); err != nil { t.Fatal(err) }
	if _, err := svc.RunDispatch(ctx, 1); err == nil { t.Error("released period was redispatched") }
	p := domain.Period{Seq:77, Status:domain.PeriodCommitted, Verdict:domain.VerdictFeasible}
	if err := svc.store.InTx(ctx, func(tx store.DBTX) error { return store.UpsertPeriodTx(tx, ctx, p) }); err != nil { t.Fatal(err) }
	if err := svc.store.InTx(ctx, func(tx store.DBTX) error { got, e := store.GetPeriodTx(tx, ctx, 77); if e != nil { return e }; if got.Status != domain.PeriodCommitted { t.Errorf("direct stored status=%s", got.Status) }; return nil }); err != nil { t.Fatal(err) }
	events, err := svc.store.ListEvents(ctx, 20)
	if err != nil { t.Fatal(err) }
	foundRelease := false
	for _, event := range events { if event.Kind == domain.EventRelease { foundRelease = true } }
	if !foundRelease { t.Errorf("release event missing: %v", events) }
}
