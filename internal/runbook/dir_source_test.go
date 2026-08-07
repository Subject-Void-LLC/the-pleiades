package runbook_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/runbook"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

// writeRunbook writes a minimal, valid runbook YAML file named id+".yaml"
// under dir, with a single leaf task calling fqcn. It is this test file's
// one fixture-construction helper, reused by every test below so a single
// real, engine.Builder-compilable YAML shape backs every scenario rather
// than each test hand-rolling its own slightly different fixture text
// (RULE 0, AGENTS.md: this exercises the same YAML surface format
// BuildFromYAML actually compiles, not a stub DAG built by hand).
func writeRunbook(t *testing.T, dir, id, fqcn string) string {
	t.Helper()
	path := filepath.Join(dir, id+".yaml")
	content := "id: " + id + "\ntasks:\n  - name: step\n    fqcn: " + fqcn + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fixture %s: %v", path, err)
	}
	return path
}

// TestDirSource_Get_ResolvesRequiredCapabilities proves the real, full
// path: a runbook file on disk, naming a real engine.ActionCapability key
// (ios_backup), resolves through NewDirSource/Get to the exact Required
// set capability_rule.go's own lookup would compute for the same task.
func TestDirSource_Get_ResolvesRequiredCapabilities(t *testing.T) {
	dir := t.TempDir()
	writeRunbook(t, dir, "backup-job", "ios_backup")

	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("NewDirSource: %v", err)
	}

	rb, err := src.Get(context.Background(), "backup-job")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rb.ID != "backup-job" {
		t.Errorf("ID = %q, want %q", rb.ID, "backup-job")
	}
	want := []capability.Name{capability.NameCiscoIOS}
	if len(rb.Required) != len(want) || rb.Required[0] != want[0] {
		t.Errorf("Required = %v, want %v", rb.Required, want)
	}
}

// TestDirSource_Get_UnknownID_ReturnsErrNotFound proves a valid-shaped id
// that simply has no backing file resolves to an error satisfying
// errors.Is(err, runbook.ErrNotFound), the contract Source.Get promises.
func TestDirSource_Get_UnknownID_ReturnsErrNotFound(t *testing.T) {
	dir := t.TempDir()
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("NewDirSource: %v", err)
	}

	_, err = src.Get(context.Background(), "nope")
	if !errors.Is(err, runbook.ErrNotFound) {
		t.Fatalf("Get(%q): got %v, want an error satisfying errors.Is(err, runbook.ErrNotFound)", "nope", err)
	}
}

// TestDirSource_Get_RejectsHostileIDs proves every id in the battery is
// rejected by the allow-list before it ever reaches the filesystem, run
// against a t.TempDir() that contains no file matching any of these ids
// (nor their names taken literally as a filename). That absence is what
// lets this test tell "rejected by validation" apart from "file
// legitimately absent": both would return a non-nil error, but only the
// filesystem-absent case wraps ErrNotFound (see
// TestDirSource_Get_UnknownID_ReturnsErrNotFound above). A hostile id
// that instead returned ErrNotFound here would mean validation was
// skipped and the id reached the Stat/ReadFile call, the exact bug this
// test exists to catch.
func TestDirSource_Get_RejectsHostileIDs(t *testing.T) {
	dir := t.TempDir()
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("NewDirSource: %v", err)
	}

	tests := []struct {
		name string
		id   string
	}{
		{"path traversal", "../../etc/passwd"},
		{"bare dot-dot", ".."},
		{"embedded NUL byte", "evil\x00name"},
		{"forward slash", "a/b"},
		{"absolute path", "/etc/passwd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := src.Get(context.Background(), tt.id)
			if err == nil {
				t.Fatal("Get: expected an error, got nil")
			}
			// The load-bearing assertion: this must NOT be the ErrNotFound
			// path. If it were, the id would have reached the filesystem
			// check and merely found nothing there, which is a different,
			// weaker guarantee than "never touched the filesystem at all."
			if errors.Is(err, runbook.ErrNotFound) {
				t.Fatalf("Get(%q): got ErrNotFound, want a validation rejection distinct from ErrNotFound (the id reached the filesystem check instead of being rejected up front)", tt.id)
			}
			// The error must never echo the raw id back to the caller,
			// since id is untrusted, caller-controlled input (an HTTP
			// query parameter in the real dispatch path).
			if strings.Contains(err.Error(), tt.id) {
				t.Fatalf("Get(%q): error %q echoes the raw id back to the caller", tt.id, err.Error())
			}
		})
	}
}

// TestDirSource_Get_UnrecognizedFQCN_YieldsEmptyRequired proves a task
// whose fqcn has no entry in engine.ActionCapability compiles successfully
// with an empty Required slice, rather than an error. This is
// action_capability.go's own documented contract for the gap direction it
// deliberately leaves open ("a fqcn can be validated before it is
// capability-constrained"; CheckActionCapabilityBindings' own doc comment
// states this explicitly): requiredCapabilities (dir_source.go) must skip
// an unmatched fqcn, not fail the whole compile over it.
func TestDirSource_Get_UnrecognizedFQCN_YieldsEmptyRequired(t *testing.T) {
	dir := t.TempDir()
	// "noop" is a real, engine.Builder-compilable fqcn (used throughout
	// internal/engine's own tests as a controller-side action) that
	// deliberately has no entry in engine.ActionCapability.
	writeRunbook(t, dir, "capability-free", "noop")

	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("NewDirSource: %v", err)
	}

	rb, err := src.Get(context.Background(), "capability-free")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(rb.Required) != 0 {
		t.Errorf("Required = %v, want empty for a fqcn with no ActionCapability entry", rb.Required)
	}
}

// TestDirSource_Get_CachePicksUpRealFileChange proves the Flyweight cache
// is keyed off the file's own mtime, not the compiled result being sticky
// forever: writing a new fqcn to the same file and forcing a distinct
// mtime via os.Chtimes (rather than relying on wall-clock granularity,
// which a fast test can easily race past on a coarse filesystem clock)
// must make a subsequent Get return the newly compiled Required set, not
// the stale cached one.
func TestDirSource_Get_CachePicksUpRealFileChange(t *testing.T) {
	dir := t.TempDir()
	path := writeRunbook(t, dir, "mutable", "ssh_exec")

	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("NewDirSource: %v", err)
	}

	first, err := src.Get(context.Background(), "mutable")
	if err != nil {
		t.Fatalf("Get (first): %v", err)
	}
	if len(first.Required) != 1 || first.Required[0] != capability.NameSSHTransport {
		t.Fatalf("Required (first) = %v, want [%v]", first.Required, capability.NameSSHTransport)
	}

	// Rewrite the file with a different fqcn, then force a mtime strictly
	// later than whatever the first write landed on, explicitly, rather
	// than trusting the two os.WriteFile calls above and below to land in
	// different filesystem-clock ticks.
	newContent := "id: mutable\ntasks:\n  - name: step\n    fqcn: ios_backup\n"
	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		t.Fatalf("failed to rewrite fixture: %v", err)
	}
	newModTime := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(path, newModTime, newModTime); err != nil {
		t.Fatalf("failed to set mtime: %v", err)
	}

	second, err := src.Get(context.Background(), "mutable")
	if err != nil {
		t.Fatalf("Get (second): %v", err)
	}
	if len(second.Required) != 1 || second.Required[0] != capability.NameCiscoIOS {
		t.Fatalf("Required (second) = %v, want [%v] (cache should have picked up the file change)", second.Required, capability.NameCiscoIOS)
	}
}
