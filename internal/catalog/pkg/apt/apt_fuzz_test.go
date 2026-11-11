// FuzzAptParams is the pkg.apt parameter fuzz target, which Phase 38
// requires before a status flips, in the shape of exec's FuzzCommandParams.
//
// The boundary is the package name and version, out of a runbook file and
// parsed before anything connects. The seeds include a name beginning with
// a dash, one holding a wildcard, and one ending in a dash, the three holes
// FAILURE_PATTERNS 1174 named for APT: each must be refused, never panic.
// The device is nil, so any input that parses stops at the connection,
// which keeps every iteration off the network. The property is no panic,
// and no change claimed beside an error.
package apt_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/apt"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func FuzzAptParams(f *testing.F) {
	f.Add("curl", "")
	f.Add("", "")
	f.Add("nginx", "1.24.0-2ubuntu7")
	f.Add("-oDPkg::Pre-Invoke::=touch /tmp/x", "")
	f.Add("lib*", "")
	f.Add("diffutils-", "")
	f.Add("g++", "\x00")
	f.Add("openssh-server", "; rm -rf /")

	methods := []func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error){
		apt.Install, apt.CheckInstall, apt.Remove, apt.CheckRemove, apt.Upgrade, apt.CheckUpgrade,
	}
	f.Fuzz(func(t *testing.T, name, version string) {
		params := map[string]any{"name": name, "version": version, "insecure_skip_host_key_verify": true}
		for _, fn := range methods {
			result, err := fn(context.Background(), &ctxStub{stats: map[string]any{}}, nil, params)
			if err == nil {
				t.Fatalf("a pkg.apt method with no target device returned no error for name %q version %q", name, version)
			}
			if result.Changed {
				t.Fatalf("a pkg.apt method reported a change alongside an error for name %q version %q", name, version)
			}
		}
	})
}
