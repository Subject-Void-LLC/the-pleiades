// A real router whose frr.conf the login may write but whose directory it may
// not: Replace needs the directory (it writes a new file beside frr.conf), so
// the save is refused, and a check must refuse it before the reload as a real
// run does. This was the handoff's first question: run 1's check passed, the
// reload changed the router, and the save then failed on "mktemp: Permission
// denied".
package frr_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/frr"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestConfig_ASaveNeedsTheDirectoryToo checks that a check and a run both
// refuse a save into an unwritable /etc/frr before anything is reloaded, name
// the account that should connect, and leave the router as it was.
func TestConfig_ASaveNeedsTheDirectoryToo(t *testing.T) {
	l := startLab(t)
	ctx := context.Background()
	r2 := l.Nodes["r2"]

	// The setup the failing run had: the file is the login's to write (it
	// owns it), the directory is not.
	r2.Exec(t, "chmod", "g-w,o-w", "/etc/frr")
	t.Cleanup(func() { r2.Exec(t, "chmod", "g+w", "/etc/frr") })

	good := l.candidate(t, "r2", "!\ninterface "+l.link["r2"]+"\n description dir-probe\nexit\n!\n")
	before := l.running(t, "r2")
	for name, call := range map[string]func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error){"check": frr.CheckConfig, "run": frr.Config} {
		_, err := call(ctx, l.rc(), l.device("r2"), map[string]any{"config": good, "save": true})
		if err == nil {
			t.Fatalf("%s: a save into a directory the login may not write was accepted", name)
		}
		for _, want := range []string{"/etc/frr", "connect as", "nothing was changed"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q does not say %q", name, err, want)
			}
		}
		if got := l.running(t, "r2"); got != before {
			t.Fatalf("%s changed the router before refusing:\n%s", name, got)
		}
	}

	// Without save the directory is not needed, and the same candidate runs.
	if _, err := frr.CheckConfig(ctx, l.rc(), l.device("r2"), map[string]any{"config": good}); err != nil {
		t.Errorf("a check without save was refused over a directory it would not write: %v", err)
	}
}
