// Package domain defines the core types and enums for the power system
// load-flow and unit-dispatch engine.
package domain

import "fmt"

// PowerBase is the per-unit base power (MVA) used throughout the engine.
const PowerBase = 100.0

// BusType identifies the role of a bus in the power-flow solution.
type BusType string

const (
	BusTypeSlack BusType = "slack" // balance bus: |V| and θ specified, P/Q solved
	BusTypePV    BusType = "pv"    // generator bus: P and |V| specified, θ and Q solved
	BusTypePQ    BusType = "pq"    // load bus: P/Q specified, θ and |V| solved
)

// GenStatus is the two-state commitment of a generator.
type GenStatus string

const (
	GenOffline  GenStatus = "offline"   // P = 0, not producing
	GenCommitted GenStatus = "committed" // online, P ∈ [PMin, PMax]
)

// PeriodStatus tracks the lifecycle of a dispatch period.
type PeriodStatus string

const (
	PeriodPlanned   PeriodStatus = "planned"   // solved but not released; may be recomputed
	PeriodCommitted PeriodStatus = "committed" // released and locked; immutable
)

// Verdict is the post-solve assessment of a period.
type Verdict string

const (
	VerdictFeasible Verdict = "feasible" // all hard limits satisfied, may be released
	VerdictRejected Verdict = "rejected" // at least one hard violation; cannot release
)

// ViolationKind classifies a single limit exceedance.
type ViolationKind string

const (
	ViolationLineOverload    ViolationKind = "line_overload"      // |S| > MVA limit
	ViolationVoltage         ViolationKind = "voltage_violation"  // |V| outside [VMin, VMax]
	ViolationReactive        ViolationKind = "reactive_violation" // PV bus Q outside [QMin, QMax]
	ViolationInsuffCapacity  ViolationKind = "insufficient_capacity"
	ViolationInsuffReserve   ViolationKind = "insufficient_reserve"
	ViolationRamp            ViolationKind = "ramp_violation"
	ViolationMinUp           ViolationKind = "min_up_violation"
	ViolationMinDown         ViolationKind = "min_down_violation"
)

// Violation is a single recorded limit exceedance for a period.
type Violation struct {
	Kind   ViolationKind `json:"kind"`
	Ref    string        `json:"ref"`    // id of the offending branch/bus/generator
	Detail string        `json:"detail"`
	Value  float64       `json:"value"`  // measured value
	Limit  float64       `json:"limit"`  // applicable limit
}

// Bus is a network node.
type Bus struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Type       BusType `json:"type"`
	VNom       float64 `json:"vnom"`       // nominal voltage kV
	VMin       float64 `json:"vmin"`        // pu lower bound
	VMax       float64 `json:"vmax"`        // pu upper bound
	VSpec      float64 `json:"vspec"`       // specified |V| pu (PV/Slack)
	ThetaSpec  float64 `json:"theta_spec"`  // specified θ rad (Slack, usually 0)
}

// BranchKind distinguishes lines and transformers.
type BranchKind string

const (
	BranchLine BranchKind = "line"
	BranchXfmr BranchKind = "xformer"
)

// Branch is a series impedance element between two buses.
type Branch struct {
	ID        string     `json:"id"`
	FromBus   string     `json:"from_bus"`
	ToBus     string     `json:"to_bus"`
	Kind      BranchKind `json:"kind"`
	R         float64    `json:"r"`       // pu resistance
	X         float64    `json:"x"`       // pu reactance
	BShunt    float64    `json:"b_shunt"`  // half line-charging susceptance (pu)
	Tap       float64    `json:"tap"`      // off-nominal tap ratio (real), 1.0 for lines
	MVALimit  float64    `json:"mva_limit"` // thermal rating MVA
	InService bool       `json:"in_service"`
}

// Generator is a dispatchable unit attached to a PV or Slack bus.
type Generator struct {
	ID         string `json:"id"`
	BusID      string `json:"bus_id"`
	PMin       float64 `json:"pmin"`     // MW
	PMax       float64 `json:"pmax"`     // MW
	RampMW     float64 `json:"ramp_mw"`  // MW per period
	MinUp      int    `json:"min_up"`    // periods
	MinDown    int    `json:"min_down"`  // periods
	QMin       float64 `json:"qmin"`     // Mvar
	QMax       float64 `json:"qmax"`     // Mvar
	Status     GenStatus `json:"status"`
	UpPeriods  int    `json:"up_periods"`
	DownPeriods int   `json:"down_periods"`
	POutput    float64 `json:"p_output"` // current MW output
}

// Load is a forecasted load at a bus for a period.
type Load struct {
	ID     string  `json:"id"`
	BusID  string  `json:"bus_id"`
	Period int     `json:"period"`
	PMW    float64 `json:"p_mw"`
	QMvar  float64 `json:"q_mvar"`
}

// Period is a dispatch horizon step.
type Period struct {
	Seq        int          `json:"seq"`
	Status     PeriodStatus `json:"status"`
	TotalGen   float64      `json:"total_gen"`
	TotalLoad  float64      `json:"total_load"`
	TotalLoss  float64      `json:"total_loss"`
	Verdict    Verdict      `json:"verdict"`
	ReserveReq float64      `json:"reserve_req"`
}

// EventKind enumerates audit-log event types.
type EventKind string

const (
	EventDispatch   EventKind = "dispatch"
	EventCommit     EventKind = "commit"
	EventDecommit   EventKind = "decommit"
	EventOutage     EventKind = "outage"
	EventRestore    EventKind = "restore"
	EventAdvance    EventKind = "advance"
	EventRelease    EventKind = "release"
	EventReconcile  EventKind = "reconcile"
)

// Event is an immutable audit record.
type Event struct {
	Seq     int       `json:"seq"`
	Period  int       `json:"period"`
	Kind    EventKind `json:"kind"`
	Payload string    `json:"payload"`
}

// SumStats describes the network size and current cursor.
type SumStats struct {
	BusCount      int    `json:"bus_count"`
	BranchCount   int    `json:"branch_count"`
	GeneratorCount int   `json:"generator_count"`
	CurrentPeriod int    `json:"current_period"`
	SlackBus      string `json:"slack_bus"`
}

// PU converts MW to per-unit on PowerBase.
func PU(mw float64) float64 { return mw / PowerBase }

// FromPU converts per-unit to MW on PowerBase.
func FromPU(pu float64) float64 { return pu * PowerBase }

// String renders a bus type for debugging.
func (b BusType) String() string { return string(b) }

// ParseBusType parses a bus type string.
func ParseBusType(s string) (BusType, error) {
	switch s {
	case string(BusTypeSlack), string(BusTypePV), string(BusTypePQ):
		return BusType(s), nil
	}
	return "", fmt.Errorf("invalid bus type %q", s)
}
