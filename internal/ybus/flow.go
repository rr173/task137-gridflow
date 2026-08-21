// Package ybus — flow.go holds the branch-flow computation and small
// complex-number helpers used by callers to evaluate per-branch power.
package ybus

import (
	"math"
	"math/cmplx"

	"task137-gridflow/internal/domain"
)

// BranchFlow computes the complex power (pu) flowing into a branch at the From
// end and at the To end, using the branch's own π-model. Returns Sfrom, Sto.
func BranchFlow(br domain.Branch, vFrom, vTo complex128) (complex128, complex128) {
	// Off-nominal tap ratio (real) on the From side. A zero tap is treated as
	// 1.0 (untapped), matching the network-assembly normalization so the
	// branch-flow model uses the same tap as the Ybus and the assembly.
	tap := br.Tap
	if tap == 0 {
		tap = 1.0
	}
	z := complex(br.R, br.X)
	if z == 0 {
		z = complex(1e-9, 0)
	}
	ys := 1.0 / z
	ysh := complex(0, br.BShunt)
	ctap := complex(tap, 0)
	// From-end current into branch: I_from = (ys/tap^2 + ysh) V_from - (ys/tap) V_to
	Ifrom := (ys/(ctap*ctap)+ysh)*vFrom - (ys/ctap)*vTo
	// To-end current into branch: I_to = (ys + ysh) V_to - (ys/tap) V_from
	Ito := (ys+ysh)*vTo - (ys/ctap)*vFrom
	Sfrom := vFrom * cmplx.Conj(Ifrom)
	Sto := vTo * cmplx.Conj(Ito)
	return Sfrom, Sto
}

// Abs returns |complex|.
func Abs(c complex128) float64 { return cmplx.Abs(c) }

// Round is a small helper retained for symmetry with other tasks.
func Round(x float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(x*p) / p
}
