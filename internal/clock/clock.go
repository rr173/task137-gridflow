// Package clock provides a deterministic period clock for tests and the engine.
package clock

// Clock advances logical dispatch periods. The engine never uses wall-clock
// time; every advance is an explicit step, which keeps restart replay exact.
type Clock struct {
	period int
}

// New creates a clock seeded at the given period (0 before any period exists).
func New(start int) *Clock { return &Clock{period: start} }

// Now returns the current period number.
func (c *Clock) Now() int { return c.period }

// Advance increments the period and returns the new value.
func (c *Clock) Advance() int {
	c.period++
	return c.period
}

// Set forces the period to a specific value (used by LoadAll during restart).
func (c *Clock) Set(p int) { c.period = p }
