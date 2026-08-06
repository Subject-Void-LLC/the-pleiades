package main

import (
	"reflect"
	"testing"
)

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

// TestSplitPositional_BoolFlagTakesNoFollowingValue is a regression test:
// splitPositional used to assume every "-"-prefixed token consumes the
// next token as its value, which silently swallowed a real flag as a bare
// boolean flag's "value" whenever one followed it (found while wiring
// forge new-collection's own --requires-elevation bool flag, and real for
// add-credential's pre-existing --passphrase bool flag too, not just the
// new callers). boolFlags is what closes that gap.
func TestSplitPositional_BoolFlagTakesNoFollowingValue(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		boolFlags      map[string]bool
		wantPositional string
		wantRest       []string
		wantErr        bool
	}{
		{
			name:           "bool flag mid-args does not swallow the next flag",
			args:           []string{"name1", "--requires-elevation", "--engine-version", ">=1.0.0"},
			boolFlags:      map[string]bool{"requires-elevation": true},
			wantPositional: "name1",
			wantRest:       []string{"--requires-elevation", "--engine-version", ">=1.0.0"},
		},
		{
			name:           "bool flag with explicit inline value still takes no following token",
			args:           []string{"name1", "--requires-elevation=true", "--engine-version", ">=1.0.0"},
			boolFlags:      map[string]bool{"requires-elevation": true},
			wantPositional: "name1",
			wantRest:       []string{"--requires-elevation=true", "--engine-version", ">=1.0.0"},
		},
		{
			name:           "no boolFlags given, string flag still consumes its value",
			args:           []string{"name1", "--type", "linux_server"},
			boolFlags:      nil,
			wantPositional: "name1",
			wantRest:       []string{"--type", "linux_server"},
		},
		{
			name:      "unrecognized bool flag would wrongly swallow the next token without boolFlags",
			args:      []string{"name1", "--requires-elevation", "--engine-version", ">=1.0.0"},
			boolFlags: nil,
			wantErr:   true, // ">=1.0.0" is misread as a second positional argument
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			positional, rest, err := splitPositional(tt.args, tt.boolFlags)
			if (err != nil) != tt.wantErr {
				t.Fatalf("splitPositional(%v, %v) error = %v, wantErr %v", tt.args, tt.boolFlags, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if positional != tt.wantPositional {
				t.Errorf("positional = %q, want %q", positional, tt.wantPositional)
			}
			if !reflect.DeepEqual(rest, tt.wantRest) {
				t.Errorf("rest = %v, want %v", rest, tt.wantRest)
			}
		})
	}
}
