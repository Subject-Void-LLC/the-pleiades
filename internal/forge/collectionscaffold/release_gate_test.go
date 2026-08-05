package collectionscaffold_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

// repoRoot locates the module root from this test file's own package
// directory (internal/forge/collectionscaffold), so the release gate can
// write a real, temporary package under internal/catalog/ and invoke the
// real go toolchain against it.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

// TestGenerate_ReleaseGate is Phase 33's Release Gate for the collection
// scaffold: "Both scaffolds produce packages that go build and whose
// generated tests pass." Unlike the device scaffold, no separate harness
// package is needed to prove registration end to end: the generated
// starter test itself (Test<Function>_Registered) already imports
// pkg/collection and asserts collection.Lookup finds the freshly
// registered name, so running the generated package's own `go test` is
// already the end-to-end proof that init()'s collection.MustRegister ran
// without panicking. This runs as a real subprocess against the actual go
// toolchain and the real repository tree, matching this project's RULE 0
// ("representative or nothing").
func TestGenerate_ReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping release gate test in -short mode")
	}

	root := repoRoot(t)
	name := fmt.Sprintf("test.relgate%d.check", os.Getpid())

	cfg := collectionscaffold.Config{
		Name:         name,
		Capabilities: []capability.Name{capability.NameSSHTransport},
		Transports:   []string{"ssh"},
	}
	files, err := collectionscaffold.Generate(cfg)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	pkgDir := filepath.Join(root, "internal", "catalog", "test", fmt.Sprintf("relgate%d", os.Getpid()))
	if err := os.MkdirAll(pkgDir, 0o755); err != nil { // #nosec G301 -- test-only, removed by t.Cleanup below
		t.Fatalf("MkdirAll(%s): %v", pkgDir, err)
	}
	// internal/catalog/test/ is entirely this test's own scratch space;
	// remove the "test" segment's parent too if it ends up empty, so
	// repeated runs never accumulate empty directories.
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(root, "internal", "catalog", "test")) })

	for _, f := range files {
		full := filepath.Join(root, f.Path)
		if err := os.WriteFile(full, f.Content, 0o644); err != nil { // #nosec G306 -- test-only, removed by t.Cleanup above
			t.Fatalf("WriteFile(%s): %v", full, err)
		}
	}

	pkgImportPath := "github.com/SubjectVoidLLC/the-pleiades/internal/catalog/test/" + fmt.Sprintf("relgate%d", os.Getpid())
	runGo(t, root, "build", pkgImportPath)
	runGo(t, root, "test", "-v", pkgImportPath)
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
