package main

import "testing"

// TestParsePropertyValue is a regression test for FAILURE_PATTERNS.md #21:
// a --set value must decode into the same Go type inventory.yaml's own
// YAML decoder would produce, or pkg/inventory.Properties' typed
// accessors (Int, Bool) silently treat it as never having been set.
func TestParsePropertyValue(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want interface{}
	}{
		{"true", "true", true},
		{"false", "false", false},
		{"plain int", "22", 22},
		{"port-like int", "32853", 32853},
		{"negative int", "-1", -1},
		{"leading zero", "007", 7},
		{"dotted version string stays a string", "15.2", "15.2"},
		{"kernel version stays a string", "6.6.87", "6.6.87"},
		{"hostname stays a string", "10.0.0.5", "10.0.0.5"},
		{"empty stays a string", "", ""},
		{"capitalized True is not the bool literal", "True", "True"},
		{"arbitrary word stays a string", "ubuntu", "ubuntu"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePropertyValue(tt.raw)
			if got != tt.want {
				t.Errorf("parsePropertyValue(%q) = %#v (%T), want %#v (%T)", tt.raw, got, got, tt.want, tt.want)
			}
		})
	}
}
