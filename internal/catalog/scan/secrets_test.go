// Tests for scan.secrets: the real Gitleaks on real files.
package scan_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/scan"
)

// plantedKey is a credential-shaped value the test plants; it must appear
// nowhere a run leaves.
const plantedKey = "AKIAZ7QXPLMNB2VKD4RT"

// TestSecrets_NamesWhereNeverWhat: a planted credential fails the task
// named by file, line and rule, its value nowhere in the result or the
// error; ignored by its fingerprint, it passes.
func TestSecrets_NamesWhereNeverWhat(t *testing.T) {
	h := newHarness(t)
	tree := writeFiles(t, map[string]string{
		"conf/app.ini": "url: https://example.com\naws_access_key_id = " + plantedKey + "\n",
		"README.md":    "nothing here\n",
	})
	rc := h.fresh()
	_, err := scan.Secrets(context.Background(), rc, h.device, params(map[string]any{"dir": tree}))
	if err == nil || !strings.Contains(err.Error(), "conf/app.ini:2: aws-access-token") {
		t.Fatalf("a planted credential = %v", err)
	}
	if strings.Contains(err.Error(), plantedKey) || strings.Contains(fmt.Sprint(rc.stats), plantedKey) {
		t.Fatal("the credential's value reached the result or the error")
	}
	findings := rc.stats["findings"].([]any)
	if f := findings[0].(map[string]any); f["fingerprint"] != "conf/app.ini:aws-access-token:2" || f["line"] != 2 {
		t.Errorf("finding = %v", f)
	}

	ignore := filepath.Join(t.TempDir(), "ignore.yaml")
	body := "- id: conf/app.ini:aws-access-token:2\n  reason: a documented example key, never valid\n  until: " + time.Now().AddDate(0, 1, 0).Format("2006-01-02") + "\n"
	if err := os.WriteFile(ignore, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rc = h.fresh()
	if _, err := scan.Secrets(context.Background(), rc, h.device, params(map[string]any{"dir": tree, "ignore": ignore})); err != nil {
		t.Fatalf("an ignored finding = %v", err)
	}
	if rc.stats["failing"] != 0 {
		t.Errorf("failing = %v", rc.stats["failing"])
	}
	if _, err := scan.Secrets(context.Background(), h.fresh(), h.device, params(map[string]any{"image_archive": "/x.tar"})); err == nil || !strings.Contains(err.Error(), "scans a directory") {
		t.Errorf("an image archive = %v", err)
	}
}
