package filterscaffold_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/filterscaffold"
)

// repoRoot locates the module root from this test file's own package
// directory (internal/forge/filterscaffold), so the release gate can
// write a real, temporary file directly into pkg/filters/ and invoke the
// real go toolchain against it.
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
// green over an unfinished implementation). This has to write into the
// real pkg/filters/ directory rather than a scratch subdirectory the way
// collectionscaffold's release gate uses internal/catalog/test/: unlike
// a Collection method, a filter has no per-entity package of its own to
// build in isolation, since every filter shares the one flat pkg/filters
// package (PLAN.md Section 36). The generated files are named with this
// process's PID and removed by t.Cleanup, so parallel `go test ./...`
// runs of sibling packages are unaffected, matching the same
// process-unique-name discipline collectionscaffold's own release gate
// documents.
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

	var written []string
	for _, f := range files {
		full := filepath.Join(root, f.Path)
		if _, err := os.Stat(full); err == nil {
			t.Fatalf("refusing to run the release gate: %s already exists", full)
		}
		if err := os.WriteFile(full, f.Content, 0o644); err != nil { // #nosec G306 -- test-only, removed by t.Cleanup below
			t.Fatalf("WriteFile(%s): %v", full, err)
		}
		written = append(written, full)
	}
	t.Cleanup(func() {
		for _, full := range written {
			_ = os.Remove(full)
		}
	})

	runGo(t, root, "build", "./pkg/filters/...")
	runGo(t, root, "test", "-run", "Test"+goName, "-v", "./pkg/filters/...")
}

func runGo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %v failed: %v\n%s", args, err, out)
	}
}
