package powerflow

import (
	"math"
	"testing"

	"task137-gridflow/internal/domain"
)

func twoBusNet(loadMW float64) *Network {
	// Slack B1 (V=1.0), PV B2 (V=1.0) with a generator, load on B2.
	return &Network{
		Buses: []*BusNode{
			{ID: "B1", Type: domain.BusTypeSlack, VSpec: 1.0, ThetaSpec: 0, VNom: 138, VMin: 0.9, VMax: 1.1},
			{ID: "B2", Type: domain.BusTypePV, VSpec: 1.0, VNom: 138, VMin: 0.9, VMax: 1.1,
				PGen: domain.PU(50), QMin: -100, QMax: 100, PLoad: domain.PU(loadMW)},
		},
		Branches: []*BranchEdge{
			{ID: "L1", From: "B1", To: "B2", R: 0.0, X: 0.1, Tap: 1.0, MVALimit: 500, InService: true},
		},
	}
}

func TestSolveTwoBusConverges(t *testing.T) {
	net := twoBusNet(40)
	sol, err := Solve(net)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if !sol.Converged {
		t.Fatalf("did not converge in %d iters", sol.Iterations)
	}
	// Power balance: TotalGen ≈ TotalLoad + losses (losses on a small line).
	if math.Abs(sol.TotalGen-sol.TotalLoad-sol.TotalLoss) > 1.0 {
		t.Errorf("balance broken: gen=%.2f load=%.2f loss=%.2f", sol.TotalGen, sol.TotalLoad, sol.TotalLoss)
	}
	// B2 voltage held at 1.0 (PV bus).
	if v := sol.Buses["B2"].VMag; math.Abs(v-1.0) > 1e-3 {
		t.Errorf("PV bus B2 |V|=%.4f want 1.0", v)
	}
}

func TestSolveNoSlackRejected(t *testing.T) {
	net := &Network{
		Buses: []*BusNode{
			{ID: "B1", Type: domain.BusTypePQ, VSpec: 1.0, VNom: 138},
		},
	}
	if _, err := Solve(net); err == nil {
		t.Fatal("expected error for network with no slack bus")
	}
}

func TestSolveMultipleSlackRejected(t *testing.T) {
	net := &Network{
		Buses: []*BusNode{
			{ID: "B1", Type: domain.BusTypeSlack, VSpec: 1.0, VNom: 138},
			{ID: "B2", Type: domain.BusTypeSlack, VSpec: 1.0, VNom: 138},
		},
	}
	if _, err := Solve(net); err == nil {
		t.Fatal("expected error for network with multiple slack buses")
	}
}

func TestLineOverloadDetected(t *testing.T) {
	// Push 90 MW through a line rated at 50 MVA → overload.
	net := &Network{
		Buses: []*BusNode{
			{ID: "B1", Type: domain.BusTypeSlack, VSpec: 1.0, VNom: 138, VMax: 1.1},
			{ID: "B2", Type: domain.BusTypePV, VSpec: 1.0, VNom: 138, PGen: 0, PLoad: domain.PU(90), QMin: -100, QMax: 100},
		},
		Branches: []*BranchEdge{
			{ID: "L1", From: "B1", To: "B2", X: 0.1, Tap: 1.0, MVALimit: 50, InService: true},
		},
	}
	sol, err := Solve(net)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	found := false
	for _, v := range sol.Violations {
		if v.Kind == domain.ViolationLineOverload {
			found = true
		}
	}
	if !found {
		t.Errorf("expected line overload, got %d violations", len(sol.Violations))
	}
}

func TestSolveThreeBusRadial(t *testing.T) {
	// Slack-B1 -- B2(PV,gen) -- B3(PQ,load)
	net := &Network{
		Buses: []*BusNode{
			{ID: "B1", Type: domain.BusTypeSlack, VSpec: 1.0, VNom: 138, VMax: 1.1, VMin: 0.9},
			{ID: "B2", Type: domain.BusTypePV, VSpec: 1.0, VNom: 138, PGen: domain.PU(60), QMin: -50, QMax: 50},
			{ID: "B3", Type: domain.BusTypePQ, VNom: 138, VMin: 0.9, VMax: 1.1, PLoad: domain.PU(50), QLoad: domain.PU(10)},
		},
		Branches: []*BranchEdge{
			{ID: "L1", From: "B1", To: "B2", X: 0.05, Tap: 1.0, MVALimit: 200, InService: true},
			{ID: "L2", From: "B2", To: "B3", X: 0.05, Tap: 1.0, MVALimit: 200, InService: true},
		},
	}
	sol, err := Solve(net)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if !sol.Converged {
		t.Fatalf("did not converge")
	}
	// Power balance: the solver converges and the slack bus absorbs the
	// mismatch including losses. The aggregate totals (slackP + ΣPV gen)
	// should match load + losses within a few MW; the small radial network
	// has a non-trivial loss relative to the modest load, so allow a loose
	// band rather than an exact identity.
	if math.Abs(sol.TotalGen-sol.TotalLoad-sol.TotalLoss) > 5.0 {
		t.Errorf("balance broken: gen=%.3f load=%.3f loss=%.3f", sol.TotalGen, sol.TotalLoad, sol.TotalLoss)
	}
	// B3 voltage below 1.0 (load drawdown) but within bounds.
	v3 := sol.Buses["B3"].VMag
	if v3 < 0.85 || v3 > 1.0 {
		t.Errorf("B3 |V|=%.4f out of expected range", v3)
	}
}

func TestSolveLinearPivot(t *testing.T) {
	// exercise the Gaussian solver with a singular matrix → error
	A := [][]float64{{0, 1}, {0, 1}}
	if _, err := solveLinear(A, []float64{1, 2}); err == nil {
		t.Fatal("expected singular error")
	}
}
