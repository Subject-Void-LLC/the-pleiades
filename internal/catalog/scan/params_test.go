// Tests for what the scan methods refuse from their parameters alone,
// before they reach the device: none of them needs an engine.
package scan_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/scan"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestVuln_RespectGitignoreIsForADirectory: respect_gitignore narrows a
// directory's scan, and with an image archive or lockfiles, which no
// .gitignore can narrow, it is refused before the device is reached.
func TestVuln_RespectGitignoreIsForADirectory(t *testing.T) {
	device := &inventorytest.Stub{StubID: "scan-1", StubName: "scan-box", StubState: inventory.StateActive,
		Caps: []capability.Name{capability.NameCommandExec, capability.NameContainerEngine}}
	for name, p := range map[string]map[string]any{
		"an image archive": {"image_archive": "/srv/app.tar", "respect_gitignore": true},
		"lockfiles":        {"lockfiles": []any{"/srv/app/package-lock.json"}, "respect_gitignore": true},
	} {
		rc := &stubContext{secrets: map[string]string{}, stats: map[string]any{}}
		if _, err := scan.Vuln(context.Background(), rc, device, p); err == nil || !strings.Contains(err.Error(), "respect_gitignore narrows a dir") {
			t.Errorf("%s with respect_gitignore = %v", name, err)
		}
	}
}
