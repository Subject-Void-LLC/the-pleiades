// Tests for Require: carry on, skip, or fail, and nothing else.
package testsupport

import (
	"fmt"
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
			t.Setenv(RequireEnv, tt.env)
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

// TestLocalStackToken covers the token helper's three answers: a token is
// returned, its absence skips naming localstack (the reason a gate reads
// to tell a package measured without it), and its absence fails a run
// that requires it.
func TestLocalStackToken(t *testing.T) {
	t.Setenv(RequireEnv, "")
	t.Setenv(LocalStackEnv, "not-a-real-token")
	if got := LocalStackToken(&recordingTB{}); got != "not-a-real-token" {
		t.Errorf("LocalStackToken = %q, want the variable's value", got)
	}

	t.Setenv(LocalStackEnv, "")
	r := &recordingTB{}
	LocalStackToken(r)
	if !strings.HasPrefix(r.skip, "needs localstack: ") || !strings.Contains(r.skip, LocalStackEnv) {
		t.Errorf("skip %q does not name localstack and the variable to set", r.skip)
	}

	t.Setenv(RequireEnv, "localstack")
	r = &recordingTB{}
	LocalStackToken(r)
	if !strings.Contains(r.fatal, "requires localstack") {
		t.Errorf("failure %q does not say the run requires localstack", r.fatal)
	}
}

// TestUserNamespaces_AnswersConsistently proves the probe answers the same
// every time it is asked, with a reason exactly when the answer is no.
func TestUserNamespaces_AnswersConsistently(t *testing.T) {
	ok, why := UserNamespaces()
	again, whyAgain := UserNamespaces()
	if ok != again || why != whyAgain {
		t.Fatal("the probe changed its answer")
	}
	if ok == (why != "") {
		t.Fatalf("ok %v with reason %q: a refusal must say why, and an allowance must not", ok, why)
	}
}
