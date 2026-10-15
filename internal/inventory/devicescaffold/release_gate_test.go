package devicescaffold_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devicescaffold"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/gooverlay"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// repoRoot locates the module root from this test file's own package
// directory (internal/inventory/devicescaffold), so the release gate can
// build generated packages under internal/inventory/devices/ and
// internal/inventory/ with the real go toolchain, as members of this
// module.
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
// toolchain, with the generated packages members of this module, matching
// this project's RULE 0
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

	// Generated into a stand-in for the module, never the module itself,
	// which every other test may be reading (gooverlay).
	tree := gooverlay.New(t, root)
	for _, f := range files {
		tree.Write(t, f.Path, f.Content)
	}

	pkgImportPath := "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/" + vendor
	tree.Build(t, pkgImportPath)
	tree.Test(t, pkgImportPath)

	// Prove end-to-end registration and resolution: a temporary harness
	// package blank-imports the generated package (the same composition-
	// root edit internal/inventory/builtins.go's own doc comment
	// describes as the one thing standing between a generated type and
	// the stock binary) and resolves it through the real factory.
	harnessName := "devicescaffoldharness" + vendor

	harnessSrc := fmt.Sprintf(`package %s

import (
	"testing"

	_ %q
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
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

	tree.Write(t, "internal/inventory/"+harnessName+"/harness_test.go", []byte(harnessSrc))

	harnessImportPath := "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/" + harnessName
	if out := tree.Test(t, harnessImportPath, "-test.run", "TestResolvesEndToEnd"); !strings.Contains(out, "--- PASS: TestResolvesEndToEnd") {
		t.Fatalf("the harness did not resolve the generated type:\n%s", out)
	}
}
