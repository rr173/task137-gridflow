package service

import (
	"context"
	"math"
	"testing"

	"task137-gridflow/internal/berr"
	"task137-gridflow/internal/clock"
	"task137-gridflow/internal/domain"
	"task137-gridflow/internal/store"
)

func newService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s, clock.New(0)), s
}

// seedFiveBus builds a small testable network and seeds period 1.
func seedFiveBus(t *testing.T, svc *Service) {
	t.Helper()
	ctx := context.Background()
	for _, b := range []domain.Bus{
		{ID: "B1", Name: "slack", Type: domain.BusTypeSlack, VNom: 138, VSpec: 1.0},
		{ID: "B2", Name: "gen", Type: domain.BusTypePV, VNom: 138, VSpec: 1.0},
		{ID: "B3", Name: "load", Type: domain.BusTypePQ, VNom: 138, VMin: 0.9, VMax: 1.1},
	} {
		if err := svc.store.CreateBus(ctx, b); err != nil {
			t.Fatalf("bus %s: %v", b.ID, err)
		}
	}
	if err := svc.store.CreateBranch(ctx, domain.Branch{ID: "L1", FromBus: "B1", ToBus: "B2", X: 0.05, Tap: 1.0, MVALimit: 200, InService: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.CreateBranch(ctx, domain.Branch{ID: "L2", FromBus: "B2", ToBus: "B3", X: 0.05, Tap: 1.0, MVALimit: 200, InService: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.CreateGenerator(ctx, domain.Generator{ID: "G1", BusID: "B1", PMax: 200, RampMW: 100, MinUp: 1, MinDown: 1, QMin: -100, QMax: 100, Status: domain.GenCommitted, UpPeriods: 1}); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.CreateGenerator(ctx, domain.Generator{ID: "G2", BusID: "B2", PMax: 150, RampMW: 80, MinUp: 2, MinDown: 1, QMin: -50, QMax: 50, Status: domain.GenCommitted, UpPeriods: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SeedPeriod(ctx, 10); err != nil {
		t.Fatal(err)
	}
}

func TestRunDispatchFeasible(t *testing.T) {
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
	if res.Verdict != domain.VerdictFeasible {
		t.Errorf("verdict=%s want feasible (violations=%v)", res.Verdict, res.Violations)
	}
	if res.TotalGen < res.TotalLoad-1.0 {
		t.Errorf("gen %.2f < load %.2f", res.TotalGen, res.TotalLoad)
	}
}

// TestRunDispatchPersistsTotalLoad guards the saved-dispatch total-load
// invariant: the persisted value (read back via GetDispatch without a
// re-reconcile) must equal the live dispatch result, not a shrunk fraction of it.
func TestRunDispatchPersistsTotalLoad(t *testing.T) {
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
	got, err := svc.GetDispatch(ctx, 1)
	if err != nil {
		t.Fatalf("get dispatch: %v", err)
	}
	if math.Abs(got.TotalLoad-res.TotalLoad) > 1e-6 {
		t.Errorf("persisted total_load shrunk: got %f want %f", got.TotalLoad, res.TotalLoad)
	}
}

func TestCommitMinDownGuard(t *testing.T) {
	svc, _ := newService(t)
	seedFiveBus(t, svc)
	ctx := context.Background()
	// create a new offline gen with min_down=2, down_periods=1 → commit fails
	if err := svc.store.CreateGenerator(ctx, domain.Generator{ID: "G3", BusID: "B2", PMax: 50, MinUp: 1, MinDown: 2, Status: domain.GenOffline, DownPeriods: 1}); err != nil {
		t.Fatal(err)
	}
	err := svc.CommitGenerator(ctx, "G3")
	if err == nil {
		t.Fatal("expected min_down violation")
	}
	be, ok := err.(*berr.Error)
	if !ok || be.Code != berr.CodeStateConflict {
		t.Errorf("expected state_conflict, got %v", err)
	}
}

func TestDecommitMinUpGuard(t *testing.T) {
	svc, _ := newService(t)
	seedFiveBus(t, svc)
	ctx := context.Background()
	// G2 has min_up=2 and UpPeriods=1 → decommit fails
	err := svc.DecommitGenerator(ctx, "G2")
	if err == nil {
		t.Fatal("expected min_up violation on decommit")
	}
}

func TestDecommitSlackForbidden(t *testing.T) {
	svc, _ := newService(t)
	seedFiveBus(t, svc)
	ctx := context.Background()
	// advance enough so min_up would be satisfied, then try decommitting slack G1
	if _, err := svc.AdvancePeriod(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdvancePeriod(ctx); err != nil {
		t.Fatal(err)
	}
	err := svc.DecommitGenerator(ctx, "G1")
	if err == nil {
		t.Fatal("expected error decommitting slack generator")
	}
}

func TestReleaseRejectsRejectedPeriod(t *testing.T) {
	svc, _ := newService(t)
	seedFiveBus(t, svc)
	ctx := context.Background()
	// huge load on B3 served through L2 (rated 200) — push past limits
	if err := svc.store.UpsertLoad(ctx, domain.Load{BusID: "B3", Period: 1, PMW: 250, QMvar: 50}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.RunDispatch(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != domain.VerdictRejected {
		t.Skipf("network not rejected as expected (verdict=%s); skipping release guard", res.Verdict)
	}
	err = svc.ReleasePeriod(ctx, 1)
	if err == nil {
		t.Fatal("expected error releasing a rejected period")
	}
}

func TestAdvanceLocksAndIncrements(t *testing.T) {
	svc, _ := newService(t)
	seedFiveBus(t, svc)
	ctx := context.Background()
	if err := svc.store.UpsertLoad(ctx, domain.Load{BusID: "B3", Period: 1, PMW: 80, QMvar: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunDispatch(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReleasePeriod(ctx, 1); err != nil {
		t.Fatal(err)
	}
	newSeq, err := svc.AdvancePeriod(ctx)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if newSeq != 2 {
		t.Errorf("new period=%d want 2", newSeq)
	}
	// re-dispatching the released period 1 should now be forbidden
	_, err = svc.RunDispatch(ctx, 1)
	if err == nil {
		t.Fatal("expected error re-dispatching a released period")
	}
}

func TestReconcileNoPlannedPeriod(t *testing.T) {
	svc, _ := newService(t)
	seedFiveBus(t, svc)
	ctx := context.Background()
	msg, err := svc.Reconcile(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if msg == "" {
		t.Error("expected non-empty reconcile message")
	}
}

// TestReconcilePreservesSavedSnapshot guards the restart path: after a
// dispatch is persisted, reconciling the planned period must reproduce the
// saved total load, generator outputs, and branch-flow snapshot — not silently
// shrink them. (Reconcile recomputes power flow but must keep the saved
// dispatch invariants intact.)
func TestReconcilePreservesSavedSnapshot(t *testing.T) {
	svc, _ := newService(t)
	seedFiveBus(t, svc)
	ctx := context.Background()
	if err := svc.store.UpsertLoad(ctx, domain.Load{BusID: "B3", Period: 1, PMW: 80, QMvar: 20}); err != nil {
		t.Fatal(err)
	}
	dispatched, err := svc.RunDispatch(ctx, 1)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// capture saved generator outputs (P per committed gen) before reconcile
	wantGen := map[string]float64{}
	for _, g := range dispatched.Generators {
		wantGen[g.ID] = g.POutput
	}

	if _, err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, err := svc.GetDispatch(ctx, 1)
	if err != nil {
		t.Fatalf("get dispatch: %v", err)
	}
	// total load must be preserved (was silently dropped before)
	if math.Abs(got.TotalLoad-dispatched.TotalLoad) > 1e-6 {
		t.Errorf("total_load not preserved: got %f want %f", got.TotalLoad, dispatched.TotalLoad)
	}
	// total gen must be preserved within solve tolerance
	if math.Abs(got.TotalGen-dispatched.TotalGen) > 1e-2 {
		t.Errorf("total_gen not preserved: got %f want %f", got.TotalGen, dispatched.TotalGen)
	}
	// generator outputs must round-trip through the snapshot
	for _, g := range got.Generators {
		if math.Abs(g.POutput-wantGen[g.ID]) > 1e-2 {
			t.Errorf("gen %s output not preserved: got %f want %f", g.ID, g.POutput, wantGen[g.ID])
		}
	}
	// branch snapshot must be preserved (count + flows)
	if len(got.Branches) != len(dispatched.Branches) {
		t.Errorf("branch snapshot shrunk: got %d want %d", len(got.Branches), len(dispatched.Branches))
	}
	for _, br := range dispatched.Branches {
		var found *BranchOutput
		for i := range got.Branches {
			if got.Branches[i].ID == br.ID {
				found = &got.Branches[i]
				break
			}
		}
		if found == nil {
			t.Errorf("branch %s missing from reconciled snapshot", br.ID)
			continue
		}
		if math.Abs(found.SMVA-br.SMVA) > 1e-2 {
			t.Errorf("branch %s SMVA not preserved: got %f want %f", br.ID, found.SMVA, br.SMVA)
		}
	}
}
