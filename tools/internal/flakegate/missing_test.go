// Tests for Missing: reading what a package's tests lacked from their
// skip reasons, in the format testsupport.Require writes.
package flakegate

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// skipRecorder is a testing.TB that keeps the message Require skips or
// fails with, so the real format can be fed to Missing.
type skipRecorder struct {
	testing.TB
	message string
}

func (r *skipRecorder) Helper() {}
func (r *skipRecorder) Skipf(format string, args ...any) {
	r.message = fmt.Sprintf(format, args...)
}
func (r *skipRecorder) Fatalf(format string, args ...any) {
	r.message = fmt.Sprintf(format, args...)
}

// requireSkip returns the reason testsupport.Require gives a skip for
// need, exactly as a test would print it.
func requireSkip(t *testing.T, need string) string {
	t.Helper()
	t.Setenv(testsupport.RequireEnv, "")
	r := &skipRecorder{}
	testsupport.Require(r, need, false, "not on this machine")
	if r.message == "" {
		t.Fatalf("Require(%q) did not skip", need)
	}
	return r.message
}

// TestMissing_ReadsTheReasonRequireGives holds Missing to the format
// Require really writes, so a change to either one fails here rather than
// quietly turning every requirement skip into an ordinary one.
func TestMissing_ReadsTheReasonRequireGives(t *testing.T) {
	const pkg = "example.com/aws"
	got := Missing([]Skip{
		{Package: pkg, Test: "TestA", Reason: requireSkip(t, "localstack")},
		{Package: pkg, Test: "TestB", Reason: requireSkip(t, "localstack")},
		{Package: pkg, Test: "TestC", Reason: requireSkip(t, "docker")},
		{Package: "example.com/plain", Test: "TestD", Reason: "set PLEIADES_E2E_IOS=1 to run this"},
		{Package: "example.com/bare", Test: "TestE", Reason: "no reason given"},
	})
	want := map[string][]string{pkg: {"docker", "localstack"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Missing = %v, want %v: one sorted entry per need, and nothing for a skip Require did not give", got, want)
	}
}
