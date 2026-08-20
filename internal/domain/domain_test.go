package domain

import "testing"

func TestParseBusType(t *testing.T) {
	cases := []struct {
		in   string
		want BusType
		err  bool
	}{
		{"slack", BusTypeSlack, false},
		{"pv", BusTypePV, false},
		{"pq", BusTypePQ, false},
		{"", "", true},
		{"xyz", "", true},
	}
	for _, c := range cases {
		got, err := ParseBusType(c.in)
		if c.err {
			if err == nil {
				t.Errorf("ParseBusType(%q) expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseBusType(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseBusType(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestPUConversions(t *testing.T) {
	if PU(100) != 1.0 {
		t.Errorf("PU(100)=%v want 1.0", PU(100))
	}
	if FromPU(2.0) != 200.0 {
		t.Errorf("FromPU(2)=%v want 200", FromPU(2.0))
	}
	if PU(0) != 0 {
		t.Errorf("PU(0)=%v want 0", PU(0))
	}
	// round trip
	if got := FromPU(PU(350.0)); got != 350.0 {
		t.Errorf("round trip 350 failed: %v", got)
	}
}

func TestViolationKinds(t *testing.T) {
	v := Violation{Kind: ViolationLineOverload, Ref: "L1", Value: 120, Limit: 100}
	if v.Kind != "line_overload" {
		t.Errorf("kind string mismatch: %s", v.Kind)
	}
}
