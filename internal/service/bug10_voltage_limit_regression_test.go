package service

import (
    "context"
    "testing"
"task137-gridflow/internal/domain"
)

func TestBug10_ThermalLimitsRemainVisibleInDispatchVerdict(t *testing.T) {
	svc, _ := newService(t)
	seedFiveBus(t, svc)
	ctx := context.Background()
	if err := svc.store.UpsertLoad(ctx, domain.Load{BusID:"B3", Period:1, PMW:250, QMvar:50}); err != nil { t.Fatal(err) }
	res, err := svc.RunDispatch(ctx, 1)
	if err != nil { t.Fatal(err) }
	if res.Verdict != domain.VerdictRejected { t.Errorf("verdict=%s, want rejected for thermally limited dispatch", res.Verdict) }
	found := false
	for _, v := range res.Violations { if v.Kind == domain.ViolationLineOverload { found = true } }
	if !found { t.Errorf("violations=%v, want line overload", res.Violations) }
	if err := svc.ReleasePeriod(ctx, 1); err == nil { t.Error("thermally rejected period was released") }
}
