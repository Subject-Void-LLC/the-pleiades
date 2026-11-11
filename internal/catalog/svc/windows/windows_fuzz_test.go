// FuzzWindowsServiceName is the svc.windows parameter fuzz target, which
// Phase 38 requires before a status flips, in the shape of exec's
// FuzzCommandParams.
//
// The boundary is the service name, out of a runbook file and parsed before
// anything connects. The seeds include a name holding a wildcard, which the
// Get/Start/Stop/Restart-Service cmdlets would expand (FAILURE_PATTERNS
// 1174): it must be refused, never panic. The device is nil, so any input
// that parses stops when the session cannot be built, which keeps every
// iteration off the network. The property is no panic, and no change
// claimed beside an error.
package windows_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/svc/windows"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func FuzzWindowsServiceName(f *testing.F) {
	f.Add("Spooler")
	f.Add("")
	f.Add("*")
	f.Add("Win[a-z]*")
	f.Add("-Force")
	f.Add("\x00\n'")

	methods := []func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error){
		windows.Start, windows.Stop, windows.Restart, windows.Enable, windows.Disable,
		windows.CheckStart, windows.CheckStop, windows.CheckRestart, windows.CheckEnable, windows.CheckDisable,
	}
	f.Fuzz(func(t *testing.T, name string) {
		params := map[string]any{"name": name}
		for _, fn := range methods {
			result, err := fn(context.Background(), &ctxStub{stats: map[string]any{}}, nil, params)
			if err == nil {
				t.Fatalf("a svc.windows method with no target device returned no error for name %q", name)
			}
			if result.Changed {
				t.Fatalf("a svc.windows method reported a change alongside an error for name %q", name)
			}
		}
	})
}
