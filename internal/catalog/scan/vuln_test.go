// Tests for scan.vuln: the real OSV-Scanner, offline, on real files.
package scan_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/scan"
)

// vuln runs scan.vuln with extra params.
func vuln(h *harness, extra map[string]any) (map[string]any, error) {
	rc := h.fresh()
	_, err := scan.Vuln(context.Background(), rc, h.device, params(extra))
	return rc.stats, err
}

// TestVuln_GatesOnSeverityAndFixes: a critical vulnerability with a fix
// fails the default gate, named with its fix and the file it is in; a
// higher threshold passes it; a medium one with no fix fails only below
// fixable_only; an unscored one is gated as high.
func TestVuln_GatesOnSeverityAndFixes(t *testing.T) {
	h := newHarness(t)
	project := writeFiles(t, npmProject)
	db := offlineDB(t, map[string]string{"GHSA-crit": advisory("GHSA-crit", "1.3.1", criticalVector)})
	stats, err := vuln(h, map[string]any{"dir": project, "offline_db": db})
	if err == nil || !strings.Contains(err.Error(), "npm left-pad@1.3.0: GHSA-crit (critical); fixed in 1.3.1") {
		t.Fatalf("a critical vulnerability = %v", err)
	}
	if stats["failing"] != 1 {
		t.Errorf("failing = %v", stats["failing"])
	}

	medium := offlineDB(t, map[string]string{"GHSA-med": advisory("GHSA-med", "", mediumVector)})
	if _, err := vuln(h, map[string]any{"dir": project, "offline_db": medium}); err != nil {
		t.Errorf("a medium one under the default high gate = %v", err)
	}
	if _, err := vuln(h, map[string]any{"dir": project, "offline_db": medium, "fail_on": "medium"}); err == nil || !strings.Contains(err.Error(), "no fix known") {
		t.Errorf("a medium one at fail_on medium = %v", err)
	}
	if _, err := vuln(h, map[string]any{"dir": project, "offline_db": medium, "fail_on": "medium", "fixable_only": true}); err != nil {
		t.Errorf("an unfixable one with fixable_only = %v", err)
	}
	unscored := offlineDB(t, map[string]string{"GHSA-none": advisory("GHSA-none", "1.3.1", "")})
	if _, err := vuln(h, map[string]any{"dir": project, "offline_db": unscored}); err == nil || !strings.Contains(err.Error(), "GHSA-none (unknown)") {
		t.Errorf("an unscored one = %v", err)
	}

	// Named lockfiles, found under their own path on the device.
	stats, err = vuln(h, map[string]any{"lockfiles": []any{filepath.Join(project, "package-lock.json")}, "offline_db": db, "fail_on": "any"})
	if err == nil {
		t.Fatal("a lockfile with a critical vulnerability passed")
	}
	finding := stats["findings"].([]any)[0].(map[string]any)
	if finding["source"] != filepath.Join(project, "package-lock.json") || finding["severity"] != "critical" {
		t.Errorf("finding = %v", finding)
	}
}

// TestVuln_AnIgnoreFileNeedsAReasonAndAnEnd: an entry in date suppresses
// its finding, recorded with its reason; one past its date suppresses
// nothing and is warned about; one matching nothing is warned about; an
// entry with no reason refuses the file.
func TestVuln_AnIgnoreFileNeedsAReasonAndAnEnd(t *testing.T) {
	h := newHarness(t)
	project := writeFiles(t, npmProject)
	db := offlineDB(t, map[string]string{"GHSA-crit": advisory("GHSA-crit", "1.3.1", criticalVector)})
	future := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	past := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	ignore := func(body string) string {
		p := filepath.Join(t.TempDir(), "scan-ignore.yaml")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	stats, err := vuln(h, map[string]any{"dir": project, "offline_db": db, "ignore": ignore("- id: GHSA-crit\n  reason: left-pad is used only in a build script\n  until: " + future + "\n- id: GHSA-gone\n  reason: an old one\n  until: " + future + "\n")})
	if err != nil {
		t.Fatalf("an ignored vulnerability = %v", err)
	}
	finding := stats["findings"].([]any)[0].(map[string]any)
	if finding["ignored"] != "left-pad is used only in a build script" || stats["failing"] != 0 {
		t.Errorf("stats = %v", stats)
	}
	if w := strings.Join(stats["warnings"].([]string), "\n"); !strings.Contains(w, "GHSA-gone (line 4") || !strings.Contains(w, "matched nothing") {
		t.Errorf("warnings = %v", stats["warnings"])
	}

	if _, err := vuln(h, map[string]any{"dir": project, "offline_db": db, "ignore": ignore("- id: GHSA-crit\n  reason: accepted\n  until: " + past + "\n")}); err == nil || !strings.Contains(err.Error(), "GHSA-crit") {
		t.Fatalf("an expired ignore = %v", err)
	}
	if _, err := vuln(h, map[string]any{"dir": project, "offline_db": db, "ignore": ignore("- id: GHSA-crit\n  until: " + future + "\n")}); err == nil || !strings.Contains(err.Error(), "has no reason") {
		t.Fatalf("an ignore entry with no reason = %v", err)
	}
}

// TestVuln_RefusesBeforeTheScanner names each refusal.
func TestVuln_RefusesBeforeTheScanner(t *testing.T) {
	h := newHarness(t)
	project := writeFiles(t, npmProject)
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"nothing to scan":   {map[string]any{}, "give dir"},
		"two targets":       {map[string]any{"dir": project, "image_archive": "/x.tar"}, "not both"},
		"a relative path":   {map[string]any{"dir": "srv/app"}, "not an absolute, clean path"},
		"a file as dir":     {map[string]any{"dir": filepath.Join(project, "package.json")}, "is a file on the device, not a directory"},
		"an unknown gate":   {map[string]any{"dir": project, "fail_on": "severe"}, "is critical, high, medium, low or any"},
		"a missing db":      {map[string]any{"dir": project, "offline_db": "/nonexistent/osv"}, "is not a directory on the device"},
		"lockfiles and dir": {map[string]any{"dir": project, "lockfiles": []any{"/x/package-lock.json"}}, "only one"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := vuln(h, tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("vuln = %v, want %q", err, tc.want)
			}
		})
	}
	h.device.Caps = h.device.Caps[:2]
	if _, err := vuln(h, map[string]any{"dir": project}); err == nil || !strings.Contains(err.Error(), "none was onboarded") {
		t.Fatalf("a device with no engine = %v", err)
	}
}

// TestVuln_TheTreeCannotTurnOffTheGate: an osv-scanner.toml in a scanned
// directory, which OSV-Scanner would read as an ignore list for the
// lockfiles beside it (no reason, no end), is refused naming the file,
// however deep it is.
func TestVuln_TheTreeCannotTurnOffTheGate(t *testing.T) {
	h := newHarness(t)
	files := map[string]string{"app/osv-scanner.toml": "[[IgnoredVulns]]\nid = \"GHSA-crit\"\n"}
	for name, body := range npmProject {
		files["app/"+name] = body
	}
	project := writeFiles(t, files)
	db := offlineDB(t, map[string]string{"GHSA-crit": advisory("GHSA-crit", "1.3.1", criticalVector)})
	_, err := vuln(h, map[string]any{"dir": project, "offline_db": db})
	if err == nil || !strings.Contains(err.Error(), filepath.Join(project, "app", "osv-scanner.toml")) {
		t.Fatalf("a tree with its own osv-scanner.toml = %v", err)
	}

	// A directory the search cannot read could hide one the scanner, as
	// its container's root, would read: the scan is refused.
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory of mode 0000")
	}
	if err := os.Remove(filepath.Join(project, "app", "osv-scanner.toml")); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(project, "locked")
	if err := os.Mkdir(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := vuln(h, map[string]any{"dir": project, "offline_db": db}); err == nil || !strings.Contains(err.Error(), "could not be searched whole") {
		t.Fatalf("a tree with a directory the search cannot read = %v", err)
	}
}
