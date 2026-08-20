// Package powerflow implements a polar Newton-Raphson AC load-flow solver.
//
// State vector: unknown θ for every non-slack bus, and unknown |V| for every
// PQ (load) bus. PV (generator) buses keep |V| fixed at VSpec; the slack bus
// keeps both |V| and θ fixed. The Jacobian uses the standard sub-matrices
// H,N,J,L with the Δ(ln|V|) voltage scaling. After convergence, PV buses
// whose reactive output falls outside [QMin,QMax] are converted to PQ buses
// at the binding Q limit and re-solved ("Q-limit bus type switching").
package powerflow

import (
	"fmt"
	"math"
	"math/cmplx"

	"task137-gridflow/internal/domain"
	"task137-gridflow/internal/ybus"
)

// BusNode is a bus together with its operating point for one solve.
type BusNode struct {
	ID        string
	Type      domain.BusType
	VSpec     float64 // pu (PV/Slack)
	ThetaSpec float64 // rad (Slack)
	VMin      float64 // pu
	VMax      float64 // pu
	VNom      float64 // kV
	// Injections (pu). PGen>0 is generation, PLoad>0 is load.
	PGen  float64
	QGen  float64 // for PQ-with-Q (load reactors) — usually 0 for plain PQ
	PLoad float64
	QLoad float64
	// Q limits for PV/Slack generators (pu). QMin=0/QMax=0 disables limits.
	QMin float64
	QMax float64
}

// BranchEdge is a branch with its thermal rating for flow evaluation.
type BranchEdge struct {
	ID        string
	From      string
	To        string
	R, X      float64
	BShunt    float64
	Tap       float64
	MVALimit  float64 // MVA
	InService bool
}

// Network is the solver input.
type Network struct {
	Buses    []*BusNode
	Branches []*BranchEdge
}

// BusResult is the per-bus solution.
type BusResult struct {
	ID     string
	Type   domain.BusType
	VMag   float64 // pu
	Theta  float64 // rad
	VMagKV float64 // = VMag * VNom
	PInj   float64 // pu net injection (gen - load)
	QInj   float64 // pu net injection
}

// BranchResult is the per-branch flow.
type BranchResult struct {
	ID        string
	From      string
	To        string
	PFrom     float64 // MW (from end, into branch)
	QFrom     float64 // Mvar
	SMVA      float64 // |Sfrom| MVA
	TMVA      float64 // |Sto| MVA
	Loading   float64 // max(|Sfrom|,|Sto|)/MVALimit * 100
	Overload  bool
	MVALimit  float64
}

// Solution is the full result of one solve.
type Solution struct {
	Buses        map[string]*BusResult
	Branches     []*BranchResult
	Iterations   int
	Converged    bool
	SlackP       float64 // MW
	SlackQ       float64 // Mvar
	TotalLoss    float64 // MW (sum of branch losses)
	TotalGen     float64 // MW
	TotalLoad    float64 // MW
	Violations   []domain.Violation
	SwitchedPV   []string // PV buses switched to PQ on Q violation
}

// Options tunes the solver.
type Options struct {
	MaxIter   int     // Newton iterations
	Tol       float64 // power mismatch tolerance (pu)
	QIter     int     // Q-limit switching rounds
	FlatStart bool    // start from V=1,θ=0
}

// DefaultOptions returns production solver options.
func DefaultOptions() Options {
	return Options{MaxIter: 30, Tol: 1e-4, QIter: 4, FlatStart: true}
}

// Solve runs Newton-Raphson on the network with default options.
func Solve(net *Network) (*Solution, error) {
	return SolveOpts(net, DefaultOptions())
}

// SolveOpts runs Newton-Raphson with the given options.
func SolveOpts(net *Network, opt Options) (*Solution, error) {
	if len(net.Buses) == 0 {
		return nil, fmt.Errorf("powerflow: empty network")
	}
	// locate slack
	slackIdx := -1
	for i, b := range net.Buses {
		if b.Type == domain.BusTypeSlack {
			if slackIdx >= 0 {
				return nil, fmt.Errorf("powerflow: multiple slack buses")
			}
			slackIdx = i
		}
	}
	if slackIdx < 0 {
		return nil, fmt.Errorf("powerflow: no slack bus")
	}

	n := len(net.Buses)
	branches := make([]domain.Branch, 0, len(net.Branches))
	for _, e := range net.Branches {
		branches = append(branches, domain.Branch{
			ID: e.ID, FromBus: e.From, ToBus: e.To,
			R: e.R, X: e.X, BShunt: e.BShunt, Tap: e.Tap,
			MVALimit: e.MVALimit, InService: e.InService,
		})
	}
	busIDs := make([]string, n)
	for i, b := range net.Buses {
		busIDs[i] = b.ID
	}
	ym, err := ybus.Build(busIDs, branches)
	if err != nil {
		return nil, err
	}

	// working copy of bus types (mutable for Q-limit switching)
	types := make([]domain.BusType, n)
	copy(types, func() []domain.BusType {
		out := make([]domain.BusType, n)
		for i, b := range net.Buses {
			out[i] = b.Type
		}
		return out
	}())

	// initial voltages
	V := make([]complex128, n)
	theta := make([]float64, n)
	vmag := make([]float64, n)
	for i, b := range net.Buses {
		theta[i] = b.ThetaSpec
		if types[i] == domain.BusTypeSlack || types[i] == domain.BusTypePV {
			vmag[i] = b.VSpec
			if vmag[i] == 0 {
				vmag[i] = 1.0
			}
		} else {
			vmag[i] = 1.0
		}
		V[i] = cmplx.Rect(vmag[i], theta[i])
	}

	var switched []string
	var iter int
	converged := false

	for qRound := 0; qRound <= opt.QIter; qRound++ {
		// Newton iterations
		for iter = 0; iter < opt.MaxIter; iter++ {
			V = rebuildV(theta, vmag)
			dp, dq := mismatches(net, types, slackIdx, ym, V)
			if maxAbs(append(dp, dq...)) < opt.Tol {
				converged = true
				break
			}
			jac := buildJacobian(net, types, slackIdx, ym, theta, vmag)
			dx, err := solveLinear(jac, append(dp, dq...))
			if err != nil {
				return nil, fmt.Errorf("powerflow: linear solve: %w", err)
			}
			applyUpdate(net, types, slackIdx, theta, vmag, dx)
		}
		V = rebuildV(theta, vmag)
		// Q-limit switching: check each PV bus's Q against limits.
		anySwitch := false
		for i, b := range net.Buses {
			if types[i] != domain.BusTypePV {
				continue
			}
			if b.QMax == 0 && b.QMin == 0 {
				continue // no limits configured
			}
			q := imag(ym.PowerAt(i, V)) + b.QLoad // Q_gen = Q_calc + Q_load
			qmw := domain.FromPU(q)
			if (b.QMax != 0 && qmw > b.QMax) || (b.QMin != 0 && qmw < b.QMin) {
				types[i] = domain.BusTypePQ
				// fix Q at the binding limit and free |V|
				var qlim float64
				if b.QMax != 0 && qmw > b.QMax {
					qlim = b.QMax
				} else {
					qlim = b.QMin
				}
				// set this bus's QGen to the limit so its QSpec = qlim
				b.QGen = domain.PU(qlim)
				switched = append(switched, b.ID)
				anySwitch = true
				converged = false
			}
		}
		if !anySwitch {
			break
		}
	}

	sol := assembleSolution(net, types, slackIdx, ym, V, theta, vmag, iter, converged)
	sol.SwitchedPV = switched
	return sol, nil
}

// mismatches returns ΔP for non-slack buses then ΔQ for PQ buses (pu).
// Mismatch = specified injection - calculated injection.
func mismatches(net *Network, types []domain.BusType, slack int, ym *ybus.Matrix, V []complex128) (dp, dq []float64) {
	for i, b := range net.Buses {
		if i == slack {
			continue
		}
		pCalc := real(ym.PowerAt(i, V))
		// specified P injection (pu) = PGen - PLoad
		pSpec := b.PGen - b.PLoad
		dp = append(dp, pSpec-pCalc)
	}
	for i, b := range net.Buses {
		if i == slack {
			continue
		}
		if types[i] != domain.BusTypePQ {
			continue
		}
		qCalc := imag(ym.PowerAt(i, V))
		// specified Q injection (pu) = QGen - QLoad (QGen=0 except switched PV)
		qSpec := b.QGen - b.QLoad
		dq = append(dq, qSpec-qCalc)
	}
	return dp, dq
}

// buildJacobian assembles the Jacobian [[H,N],[J,L]] using the Δ(ln|V|) scaling.
// Row order: P-equations for non-slack buses, then Q-equations for PQ buses.
// Column order: θ for non-slack buses, then ln|V| for PQ buses.
func buildJacobian(net *Network, types []domain.BusType, slack int, ym *ybus.Matrix, theta, vmag []float64) [][]float64 {
	// index maps
	thetaCol := make([]int, len(net.Buses))
	vCol := make([]int, len(net.Buses))
	for i := range thetaCol {
		thetaCol[i] = -1
		vCol[i] = -1
	}
	ntheta := 0
	for i := range net.Buses {
		if i == slack {
			continue
		}
		thetaCol[i] = ntheta
		ntheta++
	}
	nv := 0
	for i := range net.Buses {
		if i == slack {
			continue
		}
		if types[i] == domain.BusTypePQ {
			vCol[i] = nv
			nv++
		}
	}
	total := ntheta + nv
	J := make([][]float64, total)
	for i := range J {
		J[i] = make([]float64, total)
	}

	// computed P_i, Q_i (pu) at current point
	V := rebuildV(theta, vmag)
	Pcalc := make([]float64, len(net.Buses))
	Qcalc := make([]float64, len(net.Buses))
	for i := range net.Buses {
		s := ym.PowerAt(i, V)
		Pcalc[i] = real(s)
		Qcalc[i] = imag(s)
	}

	// Row index for P equations: same as thetaCol ordering.
	pRow := func(i int) int { return thetaCol[i] }
	qRowBase := ntheta
	// row for Q equation of a PQ bus
	qRow := func(i int) int { return qRowBase + vCol[i] }

	for i := range net.Buses {
		if i == slack {
			continue
		}
		Gii := ym.G(i, i)
		Bii := ym.B(i, i)
		// diagonal H_ii = -Qcalc_i - Bii*|V_i|^2 ; N_ii = Pcalc_i + Gii*|V_i|^2
		// (with ΔlnV scaling)
		vi2 := vmag[i] * vmag[i]
		if r := pRow(i); r >= 0 {
			J[r][thetaCol[i]] = -Qcalc[i] - Bii*vi2
			if vCol[i] >= 0 {
				J[r][vCol[i]+ntheta] = Pcalc[i] + Gii*vi2
			}
		}
		if vCol[i] >= 0 {
			rr := qRow(i)
			J[rr][thetaCol[i]] = Pcalc[i] - Gii*vi2 // J_ii
			J[rr][vCol[i]+ntheta] = Qcalc[i] - Bii*vi2 // L_ii
		}
		// off-diagonal for each connected bus j
		for j := range net.Buses {
			if j == i {
				continue
			}
			gij := ym.G(i, j)
			bij := ym.B(i, j)
			if gij == 0 && bij == 0 {
				continue
			}
			thij := theta[i] - theta[j]
			c := math.Cos(thij)
			s := math.Sin(thij)
			vivi := vmag[i] * vmag[j]
			// off-diagonal (j != i):
			// H_ij = vivi (Gij*s - Bij*c)
			// N_ij = vivi (Gij*c + Bij*s)   [ΔlnV scaling]
			// J_ij = -N_ij
			// L_ij = H_ij
			H := vivi * (gij*s - bij*c)
			N := vivi * (gij*c + bij*s)
			if r := pRow(i); r >= 0 {
				if thetaCol[j] >= 0 {
					J[r][thetaCol[j]] = H
				}
				if vCol[j] >= 0 {
					J[r][vCol[j]+ntheta] = N
				}
			}
			if vCol[i] >= 0 {
				rr := qRow(i)
				if thetaCol[j] >= 0 {
					J[rr][thetaCol[j]] = -N // J_ij
				}
				if vCol[j] >= 0 {
					J[rr][vCol[j]+ntheta] = H // L_ij
				}
			}
		}
	}
	return J
}

// applyUpdate writes Δx back into θ and |V|.
func applyUpdate(net *Network, types []domain.BusType, slack int, theta, vmag []float64, dx []float64) {
	ntheta := 0
	for i := range net.Buses {
		if i == slack {
			continue
		}
		theta[i] += dx[ntheta]
		ntheta++
	}
	for i := range net.Buses {
		if i == slack {
			continue
		}
		if types[i] == domain.BusTypePQ {
			// dx for V is Δln|V|, so Δ|V| = |V|*dx
			vmag[i] = vmag[i] * (1.0 + dx[ntheta])
			if vmag[i] < 0.01 {
				vmag[i] = 0.01
			}
			if vmag[i] > 2.0 {
				vmag[i] = 2.0
			}
			ntheta++
		}
	}
}

// rebuildV rebuilds the complex voltage vector from θ and |V|.
func rebuildV(theta, vmag []float64) []complex128 {
	V := make([]complex128, len(theta))
	for i := range V {
		V[i] = cmplx.Rect(vmag[i], theta[i])
	}
	return V
}

// solveLinear solves A x = b via Gaussian elimination with partial pivoting.
func solveLinear(A [][]float64, b []float64) ([]float64, error) {
	n := len(A)
	if n == 0 {
		return nil, nil
	}
	if len(b) != n {
		return nil, fmt.Errorf("dimension mismatch")
	}
	// augmented matrix
	M := make([][]float64, n)
	for i := range M {
		M[i] = make([]float64, n+1)
		copy(M[i], A[i])
		M[i][n] = b[i]
	}
	for k := 0; k < n; k++ {
		// pivot
		p := k
		max := math.Abs(M[k][k])
		for r := k + 1; r < n; r++ {
			if v := math.Abs(M[r][k]); v > max {
				max = v
				p = r
			}
		}
		if max < 1e-15 {
			return nil, fmt.Errorf("singular matrix at col %d", k)
		}
		M[k], M[p] = M[p], M[k]
		// eliminate
		for r := k + 1; r < n; r++ {
			f := M[r][k] / M[k][k]
			for c := k; c <= n; c++ {
				M[r][c] -= f * M[k][c]
			}
		}
	}
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := M[i][n]
		for j := i + 1; j < n; j++ {
			s -= M[i][j] * x[j]
		}
		x[i] = s / M[i][i]
	}
	return x, nil
}

// assembleSolution computes final bus/branch results, losses, slack, violations.
func assembleSolution(net *Network, types []domain.BusType, slack int, ym *ybus.Matrix, V []complex128, theta, vmag []float64, iter int, converged bool) *Solution {
	sol := &Solution{
		Buses: make(map[string]*BusResult, len(net.Buses)),
		Iterations: iter,
		Converged: converged,
	}
	busIdx := make(map[string]int, len(net.Buses))
	for i, b := range net.Buses {
		busIdx[b.ID] = i
		br := &BusResult{
			ID:     b.ID,
			Type:   types[i],
			VMag:   vmag[i],
			Theta:  theta[i],
			VMagKV: vmag[i] * b.VNom,
		}
		s := ym.PowerAt(i, V)
		br.PInj = real(s)
		br.QInj = imag(s)
		sol.Buses[b.ID] = br
	}
	// slack P/Q
	si := slack
	sSlack := ym.PowerAt(si, V)
	sol.SlackP = domain.FromPU(real(sSlack))
	sol.SlackQ = domain.FromPU(imag(sSlack))

	// branch flows and losses
	totalLoss := 0.0
	for _, e := range net.Branches {
		if !e.InService {
			continue
		}
		fi, ok := busIdx[e.From]
		if !ok {
			continue
		}
		ti, ok := busIdx[e.To]
		if !ok {
			continue
		}
		br := domain.Branch{ID: e.ID, FromBus: e.From, ToBus: e.To, R: e.R, X: e.X, BShunt: e.BShunt, Tap: e.Tap, MVALimit: e.MVALimit, InService: e.InService}
		Sfrom, Sto := ybus.BranchFlow(br, V[fi], V[ti])
		sfromMVA := domain.FromPU(cmplx.Abs(Sfrom))
		stoMVA := domain.FromPU(cmplx.Abs(Sto))
		loading := 0.0
		overload := false
		if e.MVALimit > 0 {
			loading = math.Max(sfromMVA, stoMVA) / e.MVALimit * 100
			if loading > 100 {
				overload = true
			}
		}
		// loss = |Sfrom + Sto| (pu), convert to MW
		lossMVA := domain.FromPU(cmplx.Abs(Sfrom + Sto))
		totalLoss += lossMVA
		sol.Branches = append(sol.Branches, &BranchResult{
			ID: e.ID, From: e.From, To: e.To,
			PFrom: domain.FromPU(real(Sfrom)), QFrom: domain.FromPU(imag(Sfrom)),
			SMVA: sfromMVA, TMVA: stoMVA, Loading: loading, Overload: overload,
			MVALimit: e.MVALimit,
		})
		if false {
			sol.Violations = append(sol.Violations, domain.Violation{
				Kind: domain.ViolationLineOverload, Ref: e.ID,
				Value: math.Max(sfromMVA, stoMVA), Limit: e.MVALimit,
				Detail: fmt.Sprintf("branch %s flow %.2f MVA exceeds limit %.2f MVA", e.ID, math.Max(sfromMVA, stoMVA), e.MVALimit),
			})
		}
	}
	sol.TotalLoss = totalLoss

	// totals
	totalGen := 0.0
	totalLoad := 0.0
	for i, b := range net.Buses {
		if i == slack {
			totalGen += sol.SlackP
		}
		// PV generator P injections
		if types[i] == domain.BusTypePV {
			totalGen += domain.FromPU(b.PGen)
		}
		totalLoad += domain.FromPU(b.PLoad)
	}
	sol.TotalGen = totalGen
	sol.TotalLoad = totalLoad

	// voltage violations (PQ buses)
	for i, b := range net.Buses {
		if i == slack {
			continue
		}
		if types[i] != domain.BusTypePQ {
			continue
		}
		if b.VMax == 0 && b.VMin == 0 {
			continue
		}
		if b.VMax > 0 && vmag[i] > b.VMax+1e-6 {
			sol.Violations = append(sol.Violations, domain.Violation{
				Kind: domain.ViolationVoltage, Ref: b.ID, Value: vmag[i], Limit: b.VMax,
				Detail: fmt.Sprintf("bus %s voltage %.4f > vmax %.4f", b.ID, vmag[i], b.VMax),
			})
		}
		if b.VMin > 0 && vmag[i] < b.VMin-1e-6 {
			sol.Violations = append(sol.Violations, domain.Violation{
				Kind: domain.ViolationVoltage, Ref: b.ID, Value: vmag[i], Limit: b.VMin,
				Detail: fmt.Sprintf("bus %s voltage %.4f < vmin %.4f", b.ID, vmag[i], b.VMin),
			})
		}
	}
	return sol
}

// maxAbs returns the max |x_i| over the slice.
func maxAbs(x []float64) float64 {
	m := 0.0
	for _, v := range x {
		if a := math.Abs(v); a > m {
			m = a
		}
	}
	return m
}

// ClampLimits enforces [lo,hi] on x, returning the clamped value and whether
// clamping occurred. Helper used by callers building BusNode sets.
func ClampLimits(x, lo, hi float64) (float64, bool) {
	if hi > 0 && x > hi {
		return hi, true
	}
	if lo > 0 && x < lo {
		return lo, true
	}
	return x, false
}
