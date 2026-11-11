// FuzzSystemdUnitName is the svc.systemd parameter fuzz target, which Phase
// 38 requires before a status flips, in the shape of exec's
// FuzzCommandParams.
//
// The boundary is the unit name, out of a runbook file and parsed before
// anything connects. The seeds include a name beginning with a dash and
// one holding a wildcard, the hole FAILURE_PATTERNS 1174 named: they must
// be refused, never panic. The device is nil, so any input that parses
// stops at the connection, which keeps every iteration to microseconds and
// off the network. The property is no panic, and no change claimed beside
// an error.
package systemd_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/svc/systemd"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func FuzzSystemdUnitName(f *testing.F) {
	f.Add("nginx")
	f.Add("")
	f.Add("getty@tty1.service")
	f.Add("-H")
	f.Add("s*")
	f.Add("unit; touch /tmp/x")
	f.Add("\x00\n'")

	methods := []func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error){
		systemd.Start, systemd.Stop, systemd.Restart, systemd.Enable, systemd.Disable,
		systemd.CheckStart, systemd.CheckStop, systemd.CheckRestart, systemd.CheckEnable, systemd.CheckDisable,
	}
	f.Fuzz(func(t *testing.T, name string) {
		params := map[string]any{"name": name, "insecure_skip_host_key_verify": true}
		for _, fn := range methods {
			result, err := fn(context.Background(), &svcContext{stats: map[string]any{}}, nil, params)
			if err == nil {
				t.Fatalf("a svc.systemd method with no target device returned no error for name %q", name)
			}
			if result.Changed {
				t.Fatalf("a svc.systemd method reported a change alongside an error for name %q", name)
			}
		}
	})
}
