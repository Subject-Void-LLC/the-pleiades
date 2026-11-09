// Tests for Require: carry on, skip, or fail, and nothing else.
package need

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// recordingTB stands in for a testing.TB and records which verdict Require
// reached, so the failing case can be checked without failing this test.
type recordingTB struct {
	testing.TB
	fatal, skip string
}

func (r *recordingTB) Helper() {}
func (r *recordingTB) Fatalf(format string, args ...any) {
	r.fatal = fmt.Sprintf(format, args...)
}
func (r *recordingTB) Skipf(format string, args ...any) {
	r.skip = fmt.Sprintf(format, args...)
}

// TestRequire covers the three answers: available carries on, missing
// skips with the reason, and missing in a run that requires it fails.
func TestRequire(t *testing.T) {
	tests := []struct {
		name      string
		env       string
		available bool
		wantFatal bool
		wantSkip  bool
	}{
		{"available", "", true, false, false},
		{"available and required", "userns", true, false, false},
		{"missing", "", false, false, true},
		{"missing, another thing required", "docker", false, false, true},
		{"missing and required", "docker, userns", false, true, false},
		{"missing and everything required", "all", false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(Env, tt.env)
			r := &recordingTB{}
			Require(r, "userns", tt.available, "unshare refused")
			if (r.fatal != "") != tt.wantFatal || (r.skip != "") != tt.wantSkip {
				t.Fatalf("fatal %q, skip %q; want fatal %v, skip %v", r.fatal, r.skip, tt.wantFatal, tt.wantSkip)
			}
			if tt.wantSkip && r.skip != "needs userns: unshare refused" {
				t.Errorf("skip reason %q does not name what is missing and why", r.skip)
			}
			if tt.wantFatal && !strings.Contains(r.fatal, "requires userns") {
				t.Errorf("failure %q does not say the run requires it", r.fatal)
			}
		})
	}
}

// TestPOSIXShell answers from this machine: no stop where ShellPath
// exists, and a skip naming posix-shell where it does not.
func TestPOSIXShell(t *testing.T) {
	t.Setenv(Env, "")
	r := &recordingTB{}
	POSIXShell(r)
	_, err := os.Stat(ShellPath)
	if err == nil && (r.skip != "" || r.fatal != "") {
		t.Fatalf("this machine has %s, and the check stopped: skip %q, fatal %q", ShellPath, r.skip, r.fatal)
	}
	if err != nil && !strings.HasPrefix(r.skip, "needs posix-shell: ") {
		t.Fatalf("this machine has no %s, and the skip is %q", ShellPath, r.skip)
	}
}

// TestRequire_UnknownNameInEnvFails shows a name Env lists that no test
// needs (a typo, or a need that was renamed) fails the run at its first
// Require, whether or not that Require's own need is available, instead of
// requiring nothing.
func TestRequire_UnknownNameInEnvFails(t *testing.T) {
	for _, available := range []bool{true, false} {
		t.Setenv(Env, "userns, docekr")
		r := &recordingTB{}
		Require(r, "userns", available, "unshare refused")
		if !strings.Contains(r.fatal, `"docekr"`) || !strings.Contains(r.fatal, Env) {
			t.Errorf("available=%v: failure %q does not name the unknown entry and the variable", available, r.fatal)
		}
		if r.skip != "" {
			t.Errorf("available=%v: skipped (%q) instead of failing", available, r.skip)
		}
	}
}

// TestRequire_EveryKnownNameAndAllAreAccepted shows the registry does not
// reject what CI legitimately lists.
func TestRequire_EveryKnownNameAndAllAreAccepted(t *testing.T) {
	t.Setenv(Env, strings.Join(append([]string{All}, Names...), ","))
	r := &recordingTB{}
	Require(r, Userns, true, "unused")
	if r.fatal != "" || r.skip != "" {
		t.Fatalf("a list of known names stopped the test: fatal %q, skip %q", r.fatal, r.skip)
	}
}
