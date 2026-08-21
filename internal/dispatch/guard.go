// Package dispatch — guard.go holds the state-machine transition guards.
package dispatch

import "task137-gridflow/internal/domain"

// CanCommit reports whether an OFFLINE generator may be started: it must have
// been OFFLINE for at least MinDown periods.
func CanCommit(g *GenView) bool {
	return g.Status == domain.GenOffline && g.DownPeriods >= g.MinDown
}

// CanDecommit reports whether a COMMITTED generator may be stopped: it must
// have been COMMITTED for at least MinUp periods.
func CanDecommit(g *GenView) bool {
	return g.Status == domain.GenCommitted && g.UpPeriods >= g.MinUp
}

// RampOK reports whether moving from g.POutput to newP respects the ramp rate.
func RampOK(g *GenView, newP float64) bool {
	if g.RampMW <= 0 {
		return true
	}
	delta := newP - g.POutput
	if delta < 0 {
		delta = -delta
	}
	return delta <= g.RampMW+1e-6
}
