// Tests for what a check of each scan does when the scanner's image is
// not on the engine.
package scan_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/scan"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// absentImage is a digest-pinned image no engine has and no registry
// serves (.invalid never resolves), so a pull of it can only fail.
var absentImage = "registry.invalid/pleiades/absent@sha256:" + strings.Repeat("0", 64)

// TestCheck_NeverPullsTheScanner: a check of each scan, through the
// method its descriptor registers, whose scanner's image the engine
// lacks, answers that it cannot check, naming the image a run would pull,
// rather than pull it; with pull false it is refused as a run is, naming
// the image.
func TestCheck_NeverPullsTheScanner(t *testing.T) {
	h := newHarness(t)
	scan.UseImages(t, absentImage, absentImage, absentImage)
	project := writeFiles(t, npmProject)
	for name, p := range map[string]map[string]any{
		"scan.vuln":    {"dir": project},
		"scan.secrets": {"dir": project},
		"scan.sbom":    {"dir": project, "dest": filepath.Join(t.TempDir(), "sbom"), "name": "app", "version": "1.0.0", "supplier": "Organization: Example Co"},
	} {
		t.Run(name, func(t *testing.T) {
			d, ok := collection.Lookup(name)
			if !ok {
				t.Fatalf("%s is not registered", name)
			}
			_, err := d.Check(context.Background(), h.fresh(), h.device, params(p))
			var cannot *collection.CannotCheckError
			if !errors.As(err, &cannot) || !strings.Contains(cannot.Reason, absentImage) || !strings.Contains(cannot.Reason, "a check pulls nothing") {
				t.Fatalf("a check without the scanner's image = %v", err)
			}
			p["pull"] = false
			_, err = d.Check(context.Background(), h.fresh(), h.device, params(p))
			if errors.As(err, &cannot) || err == nil || !strings.Contains(err.Error(), absentImage) || !strings.Contains(err.Error(), "pull is false") {
				t.Fatalf("a check without the image, pull false = %v", err)
			}
		})
	}
}
