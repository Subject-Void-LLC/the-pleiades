package filterscaffold_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/filterscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/gooverlay"
)

// repoRoot locates the module root from this test file's own package
// directory (internal/forge/filterscaffold), so the release gate can
// build pkg/filters with a generated file in it, using the real go
// toolchain.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

// TestGenerate_ReleaseGate proves generated output does not just parse
// (FuzzGenerate's own proof) but actually compiles as a real member of
// package filters, alongside the real pkg/filters functions, and that
// its own generated starter test runs (and is honestly skipped, not
// green over an unfinished implementation). Unlike a Collection method, a
// filter has no per-entity package of its own to build in isolation, since
// every filter shares the one flat pkg/filters package (PLAN.md Section
// 36), so the generated files join that package. They do so only in the
// go command's overlay (gooverlay), never on disk: a file written into
// pkg/filters itself was compiled into every package importing it that
// another test built meanwhile.
func TestGenerate_ReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping release gate test in -short mode")
	}

	root := repoRoot(t)
	goName := fmt.Sprintf("ReleaseGateCheck%d", os.Getpid())
	celName := fmt.Sprintf("releaseGateCheck%d", os.Getpid())

	cfg := filterscaffold.Config{
		GoName:   goName,
		CELName:  celName,
		Category: "network",
		Summary:  "is a release-gate scratch filter, never committed.",
		Params:   []filterscaffold.Param{{Name: "arg", GoType: "string"}},
		Return:   filterscaffold.Return{GoType: "string"},
	}
	files, err := filterscaffold.Generate(cfg)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	tree := gooverlay.New(t, root)
	for _, f := range files {
		full := filepath.Join(root, f.Path)
		if _, err := os.Stat(full); err == nil {
			t.Fatalf("refusing to run the release gate: %s already exists, and the overlay would replace it", full)
		}
		tree.Write(t, f.Path, f.Content)
	}

	const filters = "github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
	tree.Build(t, filters)
	if out := tree.Test(t, filters, "-test.run", "Test"+goName); !strings.Contains(out, "Test"+goName) {
		t.Fatalf("the generated test did not run:\n%s", out)
	}
}
