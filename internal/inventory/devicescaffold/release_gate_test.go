package devicescaffold_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/devicescaffold"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

// repoRoot locates the module root from this test file's own package
// directory (internal/inventory/devicescaffold), so the release gate can
// write real, temporary sibling packages under internal/inventory/devices/
// and internal/inventory/ and invoke the real go toolchain against them.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

// TestGenerate_ReleaseGate is Phase 33's Release Gate for the device
// scaffold: "Both scaffolds produce packages that go build and whose
// generated tests pass, and a generated device type registers and
// resolves end to end through" the real registration mechanism
// (record.RegisterType, not the retired ItemFactory.Register the
// checklist's own text still names -- see IMPLEMENTATION.md's Phase 33
// correction note). This runs as a real subprocess against the actual go
// toolchain and the real repository tree, matching this project's RULE 0
// ("representative or nothing"): an in-process check would only prove the
// template renders, not that the generated package genuinely compiles,
// links, and registers against this module.
func TestGenerate_ReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping release gate test in -short mode")
	}

	root := repoRoot(t)
	vendor := fmt.Sprintf("relgate%d", os.Getpid())
	typeKey := vendor + "_widget"

	cfg := devicescaffold.Config{
		Vendor:       vendor,
		TypeKey:      typeKey,
		Capabilities: []capability.Name{capability.NameSSHTransport},
	}
	files, err := devicescaffold.Generate(cfg)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	vendorDir := filepath.Join(root, "internal", "inventory", "devices", vendor)
	if err := os.MkdirAll(vendorDir, 0o755); err != nil { // #nosec G301 -- test-only, removed by t.Cleanup below
		t.Fatalf("MkdirAll(%s): %v", vendorDir, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(vendorDir) })

	for _, f := range files {
		full := filepath.Join(root, f.Path)
		if err := os.WriteFile(full, f.Content, 0o644); err != nil { // #nosec G306 -- test-only, removed by t.Cleanup above
			t.Fatalf("WriteFile(%s): %v", full, err)
		}
	}

	pkgImportPath := "github.com/SubjectVoidLLC/the-pleiades/internal/inventory/devices/" + vendor
	runGo(t, root, "build", pkgImportPath)
	runGo(t, root, "test", pkgImportPath)

	// Prove end-to-end registration and resolution: a temporary harness
	// package blank-imports the generated package (the same composition-
	// root edit internal/inventory/builtins.go's own doc comment
	// describes as the one thing standing between a generated type and
	// the stock binary) and resolves it through the real factory.
	harnessName := "devicescaffoldharness" + vendor
	harnessDir := filepath.Join(root, "internal", "inventory", harnessName)
	if err := os.MkdirAll(harnessDir, 0o755); err != nil { // #nosec G301 -- test-only, removed by t.Cleanup below
		t.Fatalf("MkdirAll(%s): %v", harnessDir, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(harnessDir) })

	harnessSrc := fmt.Sprintf(`package %s

import (
	"testing"

	_ %q
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/record"
)

func TestResolvesEndToEnd(t *testing.T) {
	factory := inventory.NewItemFactoryWithConstructors(record.AllTypes())
	item, err := factory.Build(record.Record{ID: "relgate", Name: "relgate", Type: %q})
	if err != nil {
		t.Fatalf("Build: %%v", err)
	}
	if item == nil {
		t.Fatal("Build returned a nil item")
	}
	if item.ID() != "relgate" {
		t.Fatalf("ID() = %%q, want %%q", item.ID(), "relgate")
	}
}
`, harnessName, pkgImportPath, typeKey)

	harnessFile := filepath.Join(harnessDir, "harness_test.go")
	if err := os.WriteFile(harnessFile, []byte(harnessSrc), 0o644); err != nil { // #nosec G306 -- test-only, removed by t.Cleanup above
		t.Fatalf("WriteFile(%s): %v", harnessFile, err)
	}

	harnessImportPath := "github.com/SubjectVoidLLC/the-pleiades/internal/inventory/" + harnessName
	runGo(t, root, "test", "-run", "TestResolvesEndToEnd", "-v", harnessImportPath)
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
