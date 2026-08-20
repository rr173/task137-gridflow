package service

import (
    "context"
    "testing"
)

func TestBug04_AdvanceKeepsCountersAndRecoveryCursorInStep(t *testing.T) {
	svc, _ := newService(t)
	seedFiveBus(t, svc)
	ctx := context.Background()
	seq, err := svc.AdvancePeriod(ctx)
	if err != nil { t.Fatal(err) }
	if seq != 2 { t.Errorf("new period=%d, want 2", seq) }
	g, err := svc.store.GetGenerator(ctx, "G2")
	if err != nil { t.Fatal(err) }
	if g.UpPeriods != 2 { t.Errorf("up periods=%d, want 2", g.UpPeriods) }
	cursor, err := svc.store.GetState(ctx, "current_period")
	if err != nil { t.Fatal(err) }
	if cursor != "2" { t.Errorf("stored cursor=%q, want 2", cursor) }
}
