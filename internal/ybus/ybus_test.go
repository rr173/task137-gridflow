package ybus

import (
	"math"
	"math/cmplx"
	"testing"

	"task137-gridflow/internal/domain"
)

func TestBuildSimpleLine(t *testing.T) {
	buses := []string{"B1", "B2"}
	branches := []domain.Branch{
		{ID: "L1", FromBus: "B1", ToBus: "B2", Kind: domain.BranchLine, R: 0.0, X: 0.1, Tap: 1.0, MVALimit: 100, InService: true},
	}
	m, err := Build(buses, branches)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if m.N() != 2 {
		t.Fatalf("N=%d want 2", m.N())
	}
	// series admittance ys = 1/(j*0.1) = 1/(0.1j) = -10j
	ys := complex(0, -10)
	// Y[0][0] = ys (no shunt), Y[0][1] = -ys = 10j
	if got := m.Get(0, 0); cmplx.Abs(got-ys) > 1e-9 {
		t.Errorf("Y[0][0]=%v want %v", got, ys)
	}
	if got := m.Get(0, 1); cmplx.Abs(got+ys) > 1e-9 {
		t.Errorf("Y[0][1]=%v want %v", got, -ys)
	}
}

func TestBuildTransformerTap(t *testing.T) {
	buses := []string{"B1", "B2"}
	branches := []domain.Branch{
		{ID: "T1", FromBus: "B1", ToBus: "B2", Kind: domain.BranchXfmr, R: 0.0, X: 0.05, Tap: 0.95, MVALimit: 100, InService: true},
	}
	m, err := Build(buses, branches)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ys := 1.0 / complex(0, 0.05) // -20j
	tap := 0.95
	// Y[0][0] = ys/tap^2 ; Y[0][1] = -ys/tap
	want00 := ys / complex(tap*tap, 0)
	want01 := -ys / complex(tap, 0)
	if got := m.Get(0, 0); cmplx.Abs(got-want00) > 1e-9 {
		t.Errorf("Y[0][0]=%v want %v", got, want00)
	}
	if got := m.Get(0, 1); cmplx.Abs(got-want01) > 1e-9 {
		t.Errorf("Y[0][1]=%v want %v", got, want01)
	}
}

func TestBuildIgnoresOutOfService(t *testing.T) {
	buses := []string{"B1", "B2", "B3"}
	branches := []domain.Branch{
		{ID: "L1", FromBus: "B1", ToBus: "B2", X: 0.1, Tap: 1.0, InService: true},
		{ID: "L2", FromBus: "B2", ToBus: "B3", X: 0.2, Tap: 1.0, InService: false},
	}
	m, err := Build(buses, branches)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// L2 is out: Y[1][2] should be 0
	if got := m.Get(1, 2); cmplx.Abs(got) > 1e-12 {
		t.Errorf("out-of-service branch leaked into Ybus: Y[1][2]=%v", got)
	}
	if got := m.Get(0, 1); cmplx.Abs(got) < 1e-6 {
		t.Errorf("in-service branch missing from Ybus: Y[0][1]=%v", got)
	}
}

func TestBuildUnknownBus(t *testing.T) {
	buses := []string{"B1", "B2"}
	branches := []domain.Branch{
		{ID: "L1", FromBus: "B1", ToBus: "BX", X: 0.1, Tap: 1.0, InService: true},
	}
	if _, err := Build(buses, branches); err == nil {
		t.Fatal("expected error for unknown bus")
	}
}

func TestPowerAt(t *testing.T) {
	buses := []string{"B1", "B2"}
	branches := []domain.Branch{
		{ID: "L1", FromBus: "B1", ToBus: "B2", X: 0.1, Tap: 1.0, InService: true},
	}
	m, _ := Build(buses, branches)
	// V1 = 1∠0, V2 = 1∠0 → I1 = (Y11+Y12)*1 ... with equal voltages, current
	// flows through ys: I1 = ys*V1 - ys*V2 = 0 (no flow). Power should be ~0.
	V := []complex128{complex(1, 0), complex(1, 0)}
	s := m.PowerAt(0, V)
	if math.Abs(real(s)) > 1e-9 || math.Abs(imag(s)) > 1e-9 {
		t.Errorf("zero-flow power = %v, want ~0", s)
	}
	// Now V2 = 1∠0.05 → flow should be non-zero.
	V[1] = cmplx.Rect(1.0, 0.05)
	s = m.PowerAt(0, V)
	if math.Abs(real(s)) < 1e-6 {
		t.Errorf("expected non-zero real power flow, got %v", s)
	}
}
