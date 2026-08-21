package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestFilterStageLabelsMatchLibraryOptions guards the positional coupling
// generateFilters relies on: filterStageLabels() must carry exactly one
// entry per engine.CELLibraryOptions() entry, in the same order, or a
// future library addition/reorder silently mislabels a section instead of
// failing the build the way generateFilters itself is designed to.
func TestFilterStageLabelsMatchLibraryOptions(t *testing.T) {
	got := len(filterStageLabels())
	want := len(engine.CELLibraryOptions())
	if got != want {
		t.Fatalf("filterStageLabels() has %d entries, engine.CELLibraryOptions() returns %d; "+
			"update tools/gendocs/filters.go's stage table to match", got, want)
	}
}

// TestGenerateFilters_ContainsExpectedContent proves the generated page
// actually documents what Phase 50 wires: the three pkg/filters cast
// functions with their real signatures, at least one function from each
// cel-go extension library, and the optional-chaining orValue function
// that stands in for a hand-written default filter. This is a content
// check, not a structural one: TestRun_Idempotent already proves
// regeneration is byte-stable.
func TestGenerateFilters_ContainsExpectedContent(t *testing.T) {
	tmp := t.TempDir()
	if err := generateFilters(tmp); err != nil {
		t.Fatalf("generateFilters: %v", err)
	}

	data := readGeneratedFilters(t, tmp)

	mustContain := []string{
		"filters.safeInt(dyn, int) -> int",
		"filters.safeFloat(dyn, double) -> double",
		"filters.safeBool(dyn, bool) -> bool",
		"isIP(string) -> bool",
		"base64.encode(bytes) -> string",
		"orValue",
	}
	for _, want := range mustContain {
		if !strings.Contains(data, want) {
			t.Errorf("generated filters page missing expected content: %q", want)
		}
	}
}

func readGeneratedFilters(t *testing.T, outDir string) string {
	t.Helper()
	path := filepath.Join(outDir, "filters", "index.md")
	data, err := os.ReadFile(path) // #nosec G304 -- test-controlled temp dir path
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}
