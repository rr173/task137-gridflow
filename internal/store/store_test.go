package store

import (
	"context"
	"testing"

	"task137-gridflow/internal/domain"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateAndListBuses(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, b := range []domain.Bus{
		{ID: "B1", Name: "slack", Type: domain.BusTypeSlack, VNom: 138, VSpec: 1.0},
		{ID: "B2", Name: "pv", Type: domain.BusTypePV, VNom: 138, VSpec: 1.0},
		{ID: "B3", Name: "load", Type: domain.BusTypePQ, VNom: 138, VMin: 0.9, VMax: 1.1},
	} {
		if err := s.CreateBus(ctx, b); err != nil {
			t.Fatalf("create bus %s: %v", b.ID, err)
		}
	}
	if err := s.CreateBus(ctx, domain.Bus{ID: "B1", Type: domain.BusTypeSlack}); err == nil {
		t.Fatal("duplicate bus id should error")
	}
	got, err := s.ListBuses(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("len(buses)=%d want 3", len(got))
	}
}

func TestCreateGeneratorWithFK(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// generator on a non-existent bus → FK error
	g := domain.Generator{ID: "G1", BusID: "BX", PMax: 100, Status: domain.GenOffline}
	if err := s.CreateGenerator(ctx, g); err == nil {
		t.Fatal("expected FK error for unknown bus")
	}
	// now create the bus and attach the generator to it
	if err := s.CreateBus(ctx, domain.Bus{ID: "B1", Type: domain.BusTypePV, VNom: 138, VSpec: 1.0}); err != nil {
		t.Fatal(err)
	}
	g.BusID = "B1"
	if err := s.CreateGenerator(ctx, g); err != nil {
		t.Fatalf("create generator: %v", err)
	}
}

func TestUpsertLoadAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.CreateBus(ctx, domain.Bus{ID: "B4", Type: domain.BusTypePQ, VNom: 138}); err != nil {
		t.Fatal(err)
	}
	l1 := domain.Load{BusID: "B4", Period: 1, PMW: 50, QMvar: 10}
	if err := s.UpsertLoad(ctx, l1); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// upsert again replaces
	l2 := domain.Load{BusID: "B4", Period: 1, PMW: 60, QMvar: 12}
	if err := s.UpsertLoad(ctx, l2); err != nil {
		t.Fatalf("upsert2: %v", err)
	}
	err := s.InTx(ctx, func(tx DBTX) error {
		ls, err := ListLoadsTx(tx, ctx, 1)
		if err != nil {
			return err
		}
		if len(ls) != 1 {
			t.Errorf("len=%d want 1", len(ls))
		}
		if ls[0].PMW != 60 {
			t.Errorf("PMW=%.1f want 60 (upserted)", ls[0].PMW)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBranchServiceToggle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.CreateBus(ctx, domain.Bus{ID: "B1", Type: domain.BusTypeSlack, VNom: 138}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBus(ctx, domain.Bus{ID: "B2", Type: domain.BusTypePV, VNom: 138}); err != nil {
		t.Fatal(err)
	}
	br := domain.Branch{ID: "L1", FromBus: "B1", ToBus: "B2", X: 0.1, Tap: 1.0, MVALimit: 100, InService: true}
	if err := s.CreateBranch(ctx, br); err != nil {
		t.Fatalf("create branch: %v", err)
	}
	err := s.InTx(ctx, func(tx DBTX) error {
		return SetBranchServiceTx(tx, ctx, "L1", false)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListBranches(ctx)
	if got[0].InService {
		t.Error("branch should be out of service")
	}
	// toggle back
	_ = s.InTx(ctx, func(tx DBTX) error { return SetBranchServiceTx(tx, ctx, "L1", true) })
	// unknown branch → error
	if err := s.InTx(ctx, func(tx DBTX) error { return SetBranchServiceTx(tx, ctx, "LX", true) }); err == nil {
		t.Error("expected error for unknown branch")
	}
}

func TestLoadAllRebuildsState(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.CreateBus(ctx, domain.Bus{ID: "B1", Type: domain.BusTypeSlack, VNom: 138}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateGenerator(ctx, domain.Generator{ID: "G1", BusID: "B1", PMax: 100, Status: domain.GenCommitted, UpPeriods: 3}); err != nil {
		t.Fatal(err)
	}
	_ = s.SetState(ctx, "current_period", "2")
	if err := UpsertPeriodTx(s.db, ctx, domain.Period{Seq: 1, Status: domain.PeriodPlanned, Verdict: domain.VerdictFeasible}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertPeriodTx(s.db, ctx, domain.Period{Seq: 2, Status: domain.PeriodPlanned, Verdict: domain.VerdictRejected}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.LoadAll(ctx)
	if err != nil {
		t.Fatalf("loadall: %v", err)
	}
	if len(snap.Generators) != 1 || snap.Generators[0].UpPeriods != 3 {
		t.Errorf("generator state not rebuilt: %+v", snap.Generators)
	}
	if snap.CurrentPeriod != 2 {
		t.Errorf("current period=%d want 2", snap.CurrentPeriod)
	}
	if len(snap.Periods) != 2 {
		t.Errorf("periods=%d want 2", len(snap.Periods))
	}
}

func TestResetAll(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.CreateBus(ctx, domain.Bus{ID: "B1", Type: domain.BusTypeSlack, VNom: 138}); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetAll(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	got, _ := s.ListBuses(ctx)
	if len(got) != 0 {
		t.Errorf("after reset len=%d want 0", len(got))
	}
}
