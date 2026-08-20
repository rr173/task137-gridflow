package service

import (
	"context"
	"math"
	"testing"

	"task137-gridflow/internal/domain"
)

func TestBug01_DispatchPreservesPowerBaseAcrossSolvedOutputs(t *testing.T) {
	svc, _ := newService(t)
	seedFiveBus(t, svc)
	ctx := context.Background()
	if err := svc.store.UpsertLoad(ctx, domain.Load{BusID: "B3", Period: 1, PMW: 80, QMvar: 20}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.RunDispatch(ctx, 1)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if math.Abs(res.TotalLoad-80) > 0.01 {
		t.Errorf("total load=%v, want 80 MW", res.TotalLoad)
	}
	if res.TotalGen < 79 {
		t.Errorf("total generation=%v, want dispatch in MW", res.TotalGen)
	}
	maxFlow := 0.0
	for _, br := range res.Branches {
		if br.SMVA > maxFlow {
			maxFlow = br.SMVA
		}
	}
	if maxFlow < 10 {
		t.Errorf("branch result=%+v, want MVA-scale solved flow", res.Branches)
	}
}
