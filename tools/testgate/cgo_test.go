// Tests for cgoOff, which decides whether a run reports go-sqlite3's stub
// as a skip.
package main

import (
	"errors"
	"testing"
)

// TestCgoOff: only go env's "0" is off; "1", anything else, and a go env
// that cannot answer leave the stub's failures as failures. The real go env
// answers one or the other.
func TestCgoOff(t *testing.T) {
	old := goEnvCgo
	t.Cleanup(func() { goEnvCgo = old })
	for answer, want := range map[string]bool{"0": true, "1": false, "": false} {
		goEnvCgo = func() (string, error) { return answer, nil }
		if got := cgoOff(); got != want {
			t.Errorf("go env %q: cgoOff %v, want %v", answer, got, want)
		}
	}
	goEnvCgo = func() (string, error) { return "0", errors.New("no go") }
	if cgoOff() {
		t.Error("a go env that failed counted as off")
	}
	goEnvCgo = old
	if v, err := goEnvCgo(); err != nil || (v != "0" && v != "1") {
		t.Errorf("the real go env CGO_ENABLED = %q, %v", v, err)
	}
}
