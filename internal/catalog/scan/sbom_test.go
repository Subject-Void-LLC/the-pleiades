// Tests for scan.sbom: the real Syft on real files.
package scan_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/scan"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sbomcheck"
)

// TestSBOM_WritesAValidatedSBOMIntoANewDirectory: a check writes nothing;
// a run writes the SBOM and a SHA256SUMS sha256sum -c accepts, the
// operator's supplier on the subject alone, and records the directory's
// removal; the same directory again is refused; strict_suppliers fails on
// a dependency Syft could name no supplier for.
func TestSBOM_WritesAValidatedSBOMIntoANewDirectory(t *testing.T) {
	h := newHarness(t)
	project := writeFiles(t, npmProject)
	dest := filepath.Join(t.TempDir(), "sbom-2.4.1")
	p := map[string]any{"dir": project, "dest": dest, "name": "demo-app", "version": "2.4.1", "supplier": "Organization: Example Co"}

	rc := h.fresh()
	if _, err := scan.CheckSBOM(context.Background(), rc, h.device, params(p)); err != nil {
		t.Fatalf("the check: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("a check wrote %s", dest)
	}
	rc = h.fresh()
	if _, err := scan.SBOM(context.Background(), rc, h.device, params(p)); err != nil {
		t.Fatalf("the run: %v", err)
	}
	cmd := exec.Command("sha256sum", "-c", "SHA256SUMS")
	cmd.Dir = dest
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "sbom.spdx.json: OK") {
		t.Fatalf("sha256sum -c: %v\n%s", err, out)
	}
	// Reports meant to be read: the directory and both files are
	// readable by anyone who may read where they were written.
	for name, want := range map[string]os.FileMode{"": 0o755, "sbom.spdx.json": 0o644, "SHA256SUMS": 0o644} {
		if info, err := os.Stat(filepath.Join(dest, name)); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: mode %v, %v; want %v", filepath.Join(dest, name), info.Mode().Perm(), err, want)
		}
	}
	data, err := os.ReadFile(filepath.Join(dest, "sbom.spdx.json")) // #nosec G304 -- this test's own directory
	if err != nil {
		t.Fatal(err)
	}
	doc, err := sbomcheck.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if r := doc.Validate(); len(r.Problems) != 0 {
		t.Errorf("the written SBOM: %v", r.Problems)
	}
	for _, pkg := range doc.Packages {
		if pkg.Name == "left-pad" && pkg.Supplier != "NOASSERTION" {
			t.Errorf("a dependency was given the operator's supplier: %+v", pkg)
		}
		if pkg.Name == "demo-app" && (pkg.Version != "2.4.1" || pkg.Supplier != "Organization: Example Co") {
			t.Errorf("the subject = %+v", pkg)
		}
	}
	inverse := rc.stats["inverse"].(map[string]any)
	if inverse["fqcn"] != "file.remove" || inverse["params"].(map[string]any)["path"] != dest || inverse["params"].(map[string]any)["recurse"] != true {
		t.Errorf("the undo = %v", inverse)
	}
	if warnings, _ := rc.stats["warnings"].([]string); len(warnings) != 1 || !strings.Contains(warnings[0], "have no supplier") {
		t.Errorf("warnings = %v", rc.stats["warnings"])
	}

	if _, err := scan.SBOM(context.Background(), h.fresh(), h.device, params(p)); err == nil || !strings.Contains(err.Error(), "never replaces one") {
		t.Errorf("a second SBOM into the same directory = %v", err)
	}
	p["dest"] = filepath.Join(t.TempDir(), "strict")
	p["strict_suppliers"] = true
	if _, err := scan.SBOM(context.Background(), h.fresh(), h.device, params(p)); err == nil || !strings.Contains(err.Error(), "have no supplier (strict_suppliers)") {
		t.Errorf("strict suppliers = %v", err)
	}
	if _, err := os.Stat(p["dest"].(string)); !os.IsNotExist(err) {
		t.Error("a refused SBOM left its directory")
	}
	p["supplier"] = "Example Co"
	if _, err := scan.SBOM(context.Background(), h.fresh(), h.device, params(p)); err == nil || !strings.Contains(err.Error(), "\"Organization: name\"") {
		t.Errorf("a supplier without its kind = %v", err)
	}
}

// TestSBOM_NothingButItsSubjectFails: Syft finds no component in a
// directory with no dependency, and its SBOM, holding only the subject,
// meets every element; the task fails rather than write it, leaving no
// directory, unless allow_empty says that is expected.
func TestSBOM_NothingButItsSubjectFails(t *testing.T) {
	h := newHarness(t)
	empty := writeFiles(t, map[string]string{"README.md": "no dependencies here\n"})
	dest := filepath.Join(t.TempDir(), "sbom")
	p := map[string]any{"dir": empty, "dest": dest, "name": "demo-app", "version": "1.0.0", "supplier": "Organization: Example Co"}
	if _, err := scan.SBOM(context.Background(), h.fresh(), h.device, params(p)); err == nil || !strings.Contains(err.Error(), "no component but its subject") {
		t.Fatalf("an SBOM of nothing = %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("a refused SBOM left %s", dest)
	}
	p["allow_empty"] = true
	rc := h.fresh()
	if _, err := scan.SBOM(context.Background(), rc, h.device, params(p)); err != nil {
		t.Fatalf("an SBOM of nothing, allowed = %v", err)
	}
	if rc.stats["components"] != 1 {
		t.Errorf("components = %v", rc.stats["components"])
	}
}
