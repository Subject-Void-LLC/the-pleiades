package collectionscaffold_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/gooverlay"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// repoRoot locates the module root from this test file's own package
// directory (internal/forge/collectionscaffold), so the release gate can
// build a generated package under internal/catalog/ with the real go
// toolchain, as a member of this module.
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
// toolchain, with the generated package a member of this module, matching
// this project's RULE 0 ("representative or nothing").
func TestGenerate_ReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping release gate test in -short mode")
	}

	root := repoRoot(t)
	// The second method is a Go keyword, which the method segment may be:
	// it is a file name and, capitalized, a function, never a package.
	for _, method := range []string{"check", "import"} {
		t.Run(method, func(t *testing.T) { releaseGate(t, root, method) })
	}
}

// releaseGate generates the method named method in a package of its own,
// builds it and runs its generated tests.
func releaseGate(t *testing.T, root, method string) {
	t.Helper()
	pkg := fmt.Sprintf("relgate%d%s", os.Getpid(), method)
	name := "test." + pkg + "." + method

	cfg := collectionscaffold.Config{
		Name:         name,
		Capabilities: []capability.Name{capability.NameSSHTransport},
		Transports:   []string{"ssh"},
	}
	files, err := collectionscaffold.Generate(cfg)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Generated into a stand-in for the module, never the module itself,
	// which every other test may be reading (gooverlay).
	tree := gooverlay.New(t, root)
	for _, f := range files {
		tree.Write(t, f.Path, f.Content)
	}

	pkgImportPath := "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/test/" + pkg
	tree.Build(t, pkgImportPath)
	tree.Test(t, pkgImportPath)
}
