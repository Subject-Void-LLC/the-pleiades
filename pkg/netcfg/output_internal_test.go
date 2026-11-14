// Package netcfg: MaskOutput's paths a whole-output test does not reach.
package netcfg

import (
	"strings"
	"testing"
)

// TestMaskOutput_TheRemainingPaths covers the answers MaskOutput gives that
// its form tests do not reach: well-formed XML that MaskXML refuses, a
// secret the device escaped so it cannot be found in place, a non-secret
// shape left alone, and arrays of scalars inside an object.
func TestMaskOutput_TheRemainingPaths(t *testing.T) {
	if _, _, err := MaskOutput(`<config password="x"/>`); err == nil {
		t.Error("an attribute naming a secret was not refused")
	}
	// A JSON \u escape, built in code so no tool turns it into the letter.
	escaped := `{"secret": "` + string(rune(92)) + `u0041bcdefghijkl"}`
	if _, _, err := MaskOutput(escaped); err == nil {
		t.Error("a secret escaped so it cannot be replaced in place was not refused")
	}
	out := `<name>r1</name><type>9</type><secret></secret> cut short`
	if masked, _, err := MaskOutput(out); err != nil || masked != out {
		t.Errorf("non-secret shapes in text were changed: %q, %v", masked, err)
	}
	masked, _, err := MaskOutput(`{"ports": [1, 2, "a"], "secret": "Gx-0123456789abcdef"}`)
	if err != nil || strings.Contains(masked, "Gx-0123456789abcdef") || !strings.Contains(masked, `[1, 2, "a"]`) {
		t.Errorf("masked %q, %v", masked, err)
	}
}

// TestMaskJSON_RefusesWhatDoesNotParse: maskJSON is called only on whole
// JSON, and still refuses anything else rather than reading part of it.
func TestMaskJSON_RefusesWhatDoesNotParse(t *testing.T) {
	for _, out := range []string{"", "{bad"} {
		if _, _, err := maskJSON(out); err == nil {
			t.Errorf("maskJSON(%q) = nil error", out)
		}
	}
}
