// Package ios_test: net.ios.replace's registration, and that net.ios.config
// declares it as its undo.
package ios_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestReplace_Registered: net.ios.replace is implemented, checks, and is
// the undo net.ios.config declares.
func TestReplace_Registered(t *testing.T) {
	d, ok := collection.Lookup("net.ios.replace")
	if !ok || d.Manifest.Status != collection.StatusImplemented || !d.Manifest.SupportsCheck || d.Invoke == nil || d.Check == nil {
		t.Fatalf("net.ios.replace = %+v", d.Manifest)
	}
	config, _ := collection.Lookup("net.ios.config")
	for _, spec := range config.Manifest.Reversibility.Inverses {
		if spec.FQCN == "net.ios.replace" {
			return
		}
	}
	t.Error("net.ios.config does not declare net.ios.replace as its undo")
}
