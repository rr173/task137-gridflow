// Package ybus builds the bus admittance matrix (Ybus) for a network of
// buses and branches. Lines use the π-model with a half line-charging
// susceptance; transformers add a real off-nominal tap ratio on the From side.
package ybus

import (
	"fmt"
	"math/cmplx"

	"task137-gridflow/internal/domain"
)

// Index maps a bus id to a dense row/column index in the matrix.
type Index struct {
	buses  []string
	lookup map[string]int
}

// NewIndex builds an index over the given bus ids in order.
func NewIndex(buses []string) *Index {
	idx := &Index{lookup: make(map[string]int, len(buses))}
	for _, b := range buses {
		if _, ok := idx.lookup[b]; ok {
			continue
		}
		idx.lookup[b] = len(idx.buses)
		idx.buses = append(idx.buses, b)
	}
	return idx
}

// At returns the dense index of a bus id.
func (i *Index) At(id string) (int, bool) {
	n, ok := i.lookup[id]
	return n, ok
}

// Len returns the number of buses.
func (i *Index) Len() int { return len(i.buses) }

// Buses returns the ordered bus ids.
func (i *Index) Buses() []string { return i.buses }

// Matrix is a dense complex128 admittance matrix.
type Matrix struct {
	Y  [][]complex128
	n  int
	idx *Index
}

// N returns the matrix dimension.
func (m *Matrix) N() int { return m.n }

// Get returns Y[i][j].
func (m *Matrix) Get(i, j int) complex128 { return m.Y[i][j] }

// Index returns the bus index.
func (m *Matrix) Index() *Index { return m.idx }

// Build constructs the Ybus for the given buses and branches. Only in-service
// branches contribute. Returns an error if a branch references an unknown bus.
func Build(buses []string, branches []domain.Branch) (*Matrix, error) {
	idx := NewIndex(buses)
	n := idx.Len()
	if n == 0 {
		return nil, fmt.Errorf("ybus: no buses")
	}
	y := make([][]complex128, n)
	for i := range y {
		y[i] = make([]complex128, n)
	}
	m := &Matrix{Y: y, n: n, idx: idx}
	for _, br := range branches {
		if !br.InService {
			continue
		}
		i, ok := idx.At(br.FromBus)
		if !ok {
			return nil, fmt.Errorf("ybus: branch %s unknown from bus %s", br.ID, br.FromBus)
		}
		j, ok := idx.At(br.ToBus)
		if !ok {
			return nil, fmt.Errorf("ybus: branch %s unknown to bus %s", br.ID, br.ToBus)
		}
		if i == j {
			return nil, fmt.Errorf("ybus: branch %s from==to %s", br.ID, br.FromBus)
		}
		tap := br.Tap
		if tap == 0 {
			tap = 1.0
		}
		// series admittance ys = 1 / (R + jX)
		z := complex(br.R, br.X)
		if z == 0 {
			z = complex(1e-9, 0) // avoid division by zero
		}
		ys := 1.0 / z
		// half line-charging susceptance at each end (shunt admittance j*BShunt)
		ysh := complex(0, br.BShunt)
		// π-model with real tap on From side. A real tap scales the series
		// admittance; convert tap to complex for the divisions below.
		ctap := complex(tap, 0)
		// Y[i][i] += ys/tap^2 + ysh ; Y[j][j] += ys + ysh
		// Y[i][j] -= ys/tap ; Y[j][i] -= ys/tap
		y[i][i] += ys/(ctap*ctap) + ysh
		y[j][j] += ys + ysh
		off := ys / ctap
		y[i][j] -= off
		y[j][i] -= off
	}
	return m, nil
}

// G returns the real part G[i][j] of Ybus.
func (m *Matrix) G(i, j int) float64 { return real(m.Y[i][j]) }

// B returns the imaginary part B[i][j] of Ybus.
func (m *Matrix) B(i, j int) float64 { return imag(m.Y[i][j]) }

// Gmag returns |Y[i][j]|.
func (m *Matrix) Gmag(i, j int) float64 { return cmplx.Abs(m.Y[i][j]) }

// PowerAt computes the complex injected power (pu) at bus i given the full
// voltage vector V (complex pu). S_i = V_i * conj(I_i), I_i = Σ Y[i][k] V[k].
func (m *Matrix) PowerAt(i int, V []complex128) complex128 {
	var I complex128
	for k := 0; k < m.n; k++ {
		I += m.Y[i][k] * V[k]
	}
	return V[i] * cmplx.Conj(I)
}

// Branch-flow computation and helpers live in flow.go.

