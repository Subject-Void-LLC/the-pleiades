//go:build unix

// Package loader: the warning a development build gives for a program's engine
// version constraints.
package loader

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// TestLoad_OneVersionWarningPerProgram covers the development build's
// warning: a program whose two methods both state an engine version gets
// one warning naming both, not two, and a program stating none gets none.
func TestLoad_OneVersionWarningPerProgram(t *testing.T) {
	requireConfinement(t)
	dir := programDir(t)
	constrained := implemented(false)
	constrained.EngineVersion = ">=1.0.0"
	out := describeJSON(t, 0,
		external.DescribedMethod{Name: "loadertest.versioned.one", Manifest: constrained},
		external.DescribedMethod{Name: "loadertest.versioned.two", Manifest: constrained},
	)
	writeProgram(t, dir, "versioned", script(out, "true"))
	oneMethodProgram(t, dir, "loadertest.plain.run", false, "true")

	t.Cleanup(collection.SnapshotForTest())
	opts := testOptions()
	opts.EngineVersion = "0.0.0-dev+abc123def456"
	set, err := Load(t.Context(), dir, opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	warnings := set.Warnings()
	if len(warnings) != 1 {
		t.Fatalf("warnings = %q, want exactly one, for the program that states a version", warnings)
	}
	for _, want := range []string{"development build (0.0.0-dev+abc123def456)", "loadertest.versioned.one (>=1.0.0)", "loadertest.versioned.two (>=1.0.0)"} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("warning %q does not say %q", warnings[0], want)
		}
	}
}
