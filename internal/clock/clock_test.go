package clock

import "testing"

func TestAdvanceAndNow(t *testing.T) {
	c := New(0)
	if c.Now() != 0 {
		t.Errorf("Now()=%d want 0", c.Now())
	}
	if got := c.Advance(); got != 1 {
		t.Errorf("Advance=%d want 1", got)
	}
	if c.Now() != 1 {
		t.Errorf("Now()=%d want 1", c.Now())
	}
	c.Advance()
	if c.Now() != 2 {
		t.Errorf("Now()=%d want 2", c.Now())
	}
}

func TestSet(t *testing.T) {
	c := New(0)
	c.Set(5)
	if c.Now() != 5 {
		t.Errorf("Now()=%d want 5", c.Now())
	}
	// advance from the seeded value
	if got := c.Advance(); got != 6 {
		t.Errorf("Advance=%d want 6", got)
	}
}
