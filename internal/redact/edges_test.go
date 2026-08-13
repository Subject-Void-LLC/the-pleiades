package redact_test

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// TestPackageTextUsesTheSharedState covers redact.Text, the entry point
// that replaced credential.Mask.
//
// The assertion that matters is not that it masks, it is that it masks
// against the SAME state Masker.Attr reads. Two entry points reading two
// sets of literals would disagree about what is secret, and the
// disagreement would be silent.
func TestPackageTextUsesTheSharedState(t *testing.T) {
	// Deliberately not parallel: it mutates the process-wide literal set.
	const shared = "package-level-shared-secret"

	redact.Shared().Literals().Add(shared)
	t.Cleanup(func() { redact.Shared().Literals().Forget(shared) })

	if got := redact.Text(nil, "value is "+shared); strings.Contains(got, shared) {
		t.Errorf("redact.Text did not mask a value registered on the shared masker: %q", got)
	}

	// The caller-supplied list works too, which is how every migrated call
	// site uses it.
	const perCall = "per-call-explicit-secret"
	if got := redact.Text([]string{perCall}, "value is "+perCall); strings.Contains(got, perCall) {
		t.Errorf("redact.Text did not mask an explicitly supplied secret: %q", got)
	}
}

// TestAttrLeavesNonStringValuesAlone pins the deliberate narrowness of the
// attribute pass.
//
// Only a string value is scrubbed, because scrubbing a number or a duration
// would mean formatting it, masking the text, and putting a string back,
// which changes the type of the field in the structured output. A key rule
// still catches a secret held in a non-string value, and that is the right
// division: by name when the type is unknown, by content when it is text.
func TestAttrLeavesNonStringValuesAlone(t *testing.T) {
	t.Parallel()

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}

	got := m.Attr(nil, slog.Int("port", 22))
	if got.Value.Kind() != slog.KindInt64 {
		t.Errorf("an int attribute came back as %v, want it untouched", got.Value.Kind())
	}
	if got.Key != "port" {
		t.Errorf("the key changed to %q", got.Key)
	}

	// A key rule still applies regardless of the value's type, which is
	// what stops a secret held as a non-string from escaping.
	masked := m.Attr(nil, slog.Int("password", 12345678))
	if masked.Value.String() != redact.Marker {
		t.Errorf("a key rule did not apply to a non-string value: %v", masked.Value)
	}
}

// TestAttrMatchesKeysWithoutRegardToCase covers the normalization. A caller
// logging "Password" or "API_TOKEN" means the same thing as one logging
// "password", and a case-sensitive match would leak on the difference.
func TestAttrMatchesKeysWithoutRegardToCase(t *testing.T) {
	t.Parallel()

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}

	for _, key := range []string{"password", "Password", "PASSWORD", "Api_Token", "AUTHORIZATION"} {
		got := m.Attr(nil, slog.String(key, "some-secret-value"))
		if got.Value.String() != redact.Marker {
			t.Errorf("attribute %q was not masked: %v", key, got.Value)
		}
	}
}

// TestTextOnEmptyInputIsANoOp covers the early return, which exists so the
// common case of an empty message does not snapshot the literal set.
func TestTextOnEmptyInputIsANoOp(t *testing.T) {
	t.Parallel()

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}
	if got := m.Text([]string{"a-secret-value"}, ""); got != "" {
		t.Errorf("Text(_, \"\") = %q, want the empty string", got)
	}
}

// TestNewMaskerRefusesAnInvalidRuleset proves NewMasker validates rather
// than trusting its caller. A Ruleset can be built in Go without going
// through Parse, so the check cannot live only in the decoder.
func TestNewMaskerRefusesAnInvalidRuleset(t *testing.T) {
	t.Parallel()

	if _, err := redact.NewMasker(redact.Ruleset{}); err == nil {
		t.Fatal("NewMasker() accepted a ruleset with no rules")
	}
}

// TestLiteralsRefusesPastItsBound covers the overflow decision.
//
// Refusing rather than evicting is the deliberate part: evicting would mean
// a secret still in flight silently stops being masked, which is the one
// failure this package exists to prevent. Refusing loses masking for the
// new value, which is bad, but the other two channels still apply and Add
// reports the refusal.
func TestLiteralsRefusesPastItsBound(t *testing.T) {
	t.Parallel()

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}
	lits := m.Literals()

	// Matches maxLiterals in literals.go. The constant is unexported, so
	// this test states the number it depends on and fails loudly if the
	// two ever disagree.
	const bound = 10_000

	values := make([]string, 0, bound)
	for i := 0; i < bound; i++ {
		values = append(values, fmt.Sprintf("literal-secret-%06d", i))
	}
	if added := lits.Add(values...); added != bound {
		t.Fatalf("Add() recorded %d of %d values before the bound", added, bound)
	}

	if added := lits.Add("one-past-the-bound-value"); added != 0 {
		t.Errorf("Add() past the bound recorded %d, want 0", added)
	}
	if lits.Len() != bound {
		t.Errorf("Len() = %d after overflowing, want %d, so something was evicted", lits.Len(), bound)
	}

	// The important half: an earlier value is still masked, proving
	// nothing was evicted to make room.
	if got := m.Text(nil, "value literal-secret-000000 here"); strings.Contains(got, "literal-secret-000000") {
		t.Error("a value recorded before the bound stopped being masked, so the bound evicts rather than refuses")
	}
}

// TestWriterOnEmptyInput covers the early return, which matters because an
// empty write is common and must not be reported as an error.
func TestWriterOnEmptyInput(t *testing.T) {
	t.Parallel()

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}

	n, err := m.Writer(failingWriter{}).Write(nil)
	if err != nil {
		t.Errorf("Write(nil) failed: %v", err)
	}
	if n != 0 {
		t.Errorf("Write(nil) = %d, want 0", n)
	}
}

// TestWriterReportsAnUnderlyingFailure covers the error path. The count is
// reported as zero rather than partial, because the caller counts original
// bytes and the writer counts masked ones, so no partial count it could
// return would mean anything to the caller.
func TestWriterReportsAnUnderlyingFailure(t *testing.T) {
	t.Parallel()

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}

	n, err := m.Writer(failingWriter{}).Write([]byte("anything"))
	if !errors.Is(err, errWriteFailed) {
		t.Errorf("Write() error = %v, want the underlying failure", err)
	}
	if n != 0 {
		t.Errorf("Write() = %d alongside an error, want 0", n)
	}
}

// errWriteFailed is failingWriter's error.
var errWriteFailed = errors.New("write failed")

// failingWriter always fails, so the decorator's error path is reachable.
type failingWriter struct{}

// Write always fails.
func (failingWriter) Write([]byte) (int, error) { return 0, errWriteFailed }
