// Tests for MapValues, the one helper every tier feeds a credential's
// values to masking through.
package redact

import "testing"

// TestMapValues_ReturnsEveryValue pins the helper Text is fed from: a
// secret missing from this slice is a secret that will not be masked out of
// captured output. It moved here from internal/adapters/native when three
// packages' copies of the helper became this one (Phase 117a).
func TestMapValues_ReturnsEveryValue(t *testing.T) {
	got := MapValues(map[string]string{"username": "admin", "password": "hunter2"})
	if len(got) != 2 {
		t.Fatalf("MapValues() returned %d values, want 2", len(got))
	}
	seen := map[string]bool{}
	for _, v := range got {
		seen[v] = true
	}
	if !seen["admin"] || !seen["hunter2"] {
		t.Errorf("MapValues() = %v, want both values present", got)
	}
}

// TestMapValues_EmptyIsEmpty pins the nil and empty cases: a device with no
// stored credential masks nothing and does not fail.
func TestMapValues_EmptyIsEmpty(t *testing.T) {
	if got := MapValues(nil); len(got) != 0 {
		t.Errorf("MapValues(nil) = %v, want empty", got)
	}
}
