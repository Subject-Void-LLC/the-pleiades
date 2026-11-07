// Package golden: the comparison, the update path, and the failure text.
package golden

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recorder is a testing.TB that keeps what Assert reports instead of
// failing the real test, so a failing comparison can itself be asserted.
type recorder struct {
	testing.TB
	// errors holds every message passed to Errorf.
	errors []string
}

// Helper does nothing: the recorder has no frames of its own to hide.
func (r *recorder) Helper() {}

// Errorf records the message.
func (r *recorder) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

// withUpdate runs fn with -update-golden set, restoring it after.
func withUpdate(t *testing.T, fn func()) {
	t.Helper()
	old := *update
	*update = true
	defer func() { *update = old }()
	fn()
}

func TestAssertPassesOnEqualBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "same.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &recorder{TB: t}
	Assert(r, path, []byte("one\ntwo\n"))
	if len(r.errors) != 0 {
		t.Errorf("equal bytes failed: %v", r.errors)
	}
}

func TestAssertFailsOnAMismatchWithADiffAndTheUpdateCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "want.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &recorder{TB: t}
	Assert(r, path, []byte("a\nb\nX\nd\n"))
	if len(r.errors) != 1 {
		t.Fatalf("got %d failures, want 1: %v", len(r.errors), r.errors)
	}
	msg := r.errors[0]
	for _, want := range []string{
		"first difference at line 3, column 1",
		"  b\n",
		"- c\n",
		"+ X\n",
		"go test ./internal/golden -run '^TestAssertFailsOnAMismatchWithADiffAndTheUpdateCommand$' -update-golden",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the failure lacks %q:\n%s", want, msg)
		}
	}
	// The run of differing lines stops where the sides agree again.
	if strings.Contains(msg, "- d\n") || strings.Contains(msg, "+ d\n") {
		t.Errorf("an equal line after the difference was shown as changed:\n%s", msg)
	}
}

func TestDiffNamesTheColumnOfALongLine(t *testing.T) {
	got := diff(`{"a":1,"b":2}`, `{"a":1,"b":3}`)
	if !strings.Contains(got, "line 1, column 12") {
		t.Errorf("the column was not named:\n%s", got)
	}
}

func TestAssertFailsOnAMissingFileWithTheUpdateHint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.txt")
	r := &recorder{TB: t}
	Assert(r, path, []byte("x"))
	if len(r.errors) != 1 || !strings.Contains(r.errors[0], "create it with: go test ./internal/golden") ||
		!strings.Contains(r.errors[0], "-update-golden") {
		t.Errorf("a missing file was not reported with the hint: %v", r.errors)
	}
}

func TestAssertWritesTheFileUnderUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "dir", "out.txt")
	r := &recorder{TB: t}
	withUpdate(t, func() { Assert(r, path, []byte("fresh\n")) })
	if len(r.errors) != 0 {
		t.Fatalf("update failed: %v", r.errors)
	}
	got, err := os.ReadFile(path) // #nosec G304 -- a path under this test's own TempDir
	if err != nil || string(got) != "fresh\n" {
		t.Fatalf("the file holds %q, %v; want the written bytes", got, err)
	}
	// Once written, the same bytes compare equal without the flag.
	Assert(r, path, []byte("fresh\n"))
	if len(r.errors) != 0 {
		t.Errorf("the written file did not match its own bytes: %v", r.errors)
	}
}

func TestAssertReportsAWriteFailureUnderUpdate(t *testing.T) {
	// A file where the directory should be makes MkdirAll fail.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := &recorder{TB: t}
	withUpdate(t, func() { Assert(r, filepath.Join(blocker, "sub", "out.txt"), []byte("x")) })
	if len(r.errors) != 1 || !strings.Contains(r.errors[0], "creating the directory") {
		t.Errorf("a failed write was not reported: %v", r.errors)
	}
}

func TestJSONIndentsAndEndsWithANewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.json")
	r := &recorder{TB: t}
	withUpdate(t, func() { JSON(r, path, map[string]int{"b": 2, "a": 1}) })
	got, err := os.ReadFile(path) // #nosec G304 -- a path under this test's own TempDir
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"a\": 1,\n  \"b\": 2\n}\n"; string(got) != want {
		t.Errorf("JSON wrote %q, want %q", got, want)
	}
}

func TestJSONReportsAValueThatCannotMarshal(t *testing.T) {
	r := &recorder{TB: t}
	JSON(r, filepath.Join(t.TempDir(), "v.json"), func() {})
	if len(r.errors) != 1 || !strings.Contains(r.errors[0], "marshaling") {
		t.Errorf("an unmarshalable value was not reported: %v", r.errors)
	}
}

func TestDiffShowsALongerSide(t *testing.T) {
	got := diff("a\n", "a\nb\nc\n")
	if !strings.Contains(got, "+ b\n") || !strings.Contains(got, "+ c\n") || !strings.Contains(got, "first difference at line 2") {
		t.Errorf("an appended tail was not shown:\n%s", got)
	}
}
