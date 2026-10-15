// Tests for scanning an image archive: the real OSV-Scanner and Syft on a
// real docker save of an Alpine image.
package scan_test

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/scan"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// TestImageArchive_VulnAndSBOM: an Alpine image saved to an archive is
// scanned offline against a database with one advisory for its busybox,
// each binary package named, the finding located within the archive; and
// described by an SBOM whose subject is the task's.
func TestImageArchive_VulnAndSBOM(t *testing.T) {
	h := newHarness(t)
	image := testsupport.NginxImage
	release, err := exec.Command("docker", "run", "--rm", "--network", "none", "--entrypoint", "cat", image, "/etc/alpine-release").Output() // #nosec G204 -- the test's own pinned image
	testsupport.Require(t, image, err == nil, "the archive is a docker save of it")
	parts := strings.SplitN(strings.TrimSpace(string(release)), ".", 3)
	ecosystem := "Alpine:v" + parts[0] + "." + parts[1]
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "image.tar")
	if out, err := exec.Command("docker", "save", "-o", archive, image).CombinedOutput(); err != nil { // #nosec G204 -- as above
		t.Fatalf("docker save: %v\n%s", err, out)
	}
	if err := os.Chmod(archive, 0o644); err != nil {
		t.Fatal(err)
	}

	db := t.TempDir()
	alpine := filepath.Join(db, "osv-scalibr", "Alpine")
	if err := os.MkdirAll(alpine, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(db, 0o755)
	_ = os.Chmod(filepath.Join(db, "osv-scalibr"), 0o755)
	adv, _ := json.Marshal(map[string]any{
		"schema_version": "1.6.0", "id": "ALPINE-TEST-1", "modified": "2026-01-01T00:00:00Z", "summary": "test",
		"affected": []any{map[string]any{"package": map[string]string{"ecosystem": ecosystem, "name": "busybox"},
			"ranges": []any{map[string]any{"type": "ECOSYSTEM", "events": []any{map[string]string{"introduced": "0"}, map[string]string{"fixed": "99.0.0-r0"}}}}}},
		"severity": []any{map[string]string{"type": "CVSS_V3", "score": criticalVector}},
	})
	f, err := os.Create(filepath.Join(alpine, "all.zip")) // #nosec G304 -- this test's own directory
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	w, _ := z.Create("ALPINE-TEST-1.json")
	_, _ = w.Write(adv)
	_ = z.Close()
	_ = f.Chmod(0o644)
	_ = f.Close()

	rc := h.fresh()
	_, err = scan.Vuln(context.Background(), rc, h.device, params(map[string]any{"image_archive": archive, "offline_db": db}))
	if err == nil || !strings.Contains(err.Error(), ecosystem+" busybox") || !strings.Contains(err.Error(), "ALPINE-TEST-1 (critical); fixed in 99.0.0-r0") {
		t.Fatalf("an image with a vulnerable busybox = %v", err)
	}
	finding := rc.stats["findings"].([]any)[0].(map[string]any)
	if source, _ := finding["source"].(string); !strings.HasPrefix(source, archive+":/") {
		t.Errorf("the finding is located at %v, want within %s", finding["source"], archive)
	}

	dest := filepath.Join(t.TempDir(), "sbom")
	rc = h.fresh()
	if _, err := scan.SBOM(context.Background(), rc, h.device, params(map[string]any{"image_archive": archive, "dest": dest, "name": "web", "version": "1.28.1", "supplier": "Organization: Example Co"})); err != nil {
		t.Fatalf("an SBOM of the image: %v", err)
	}
	if n, _ := rc.stats["components"].(int); n < 10 {
		t.Errorf("components = %v", rc.stats["components"])
	}
}
