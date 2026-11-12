// FuzzWindowsServiceName is the svc.windows parameter fuzz target, which
// Phase 38 requires before a status flips, in the shape of exec's
// FuzzCommandParams.
//
// The boundary is the service name, out of a runbook file and parsed before
// anything connects. The seeds include a name holding a wildcard, which the
// Get/Start/Stop/Restart-Service cmdlets would expand (FAILURE_PATTERNS
// 1174). The device is nil, so any input that parses stops when the session
// cannot be built, which keeps every iteration off the network. The
// properties are no panic, no change claimed beside an error, and
// refusedBeforeConnecting: a name the operand rule refuses never gets as far
// as building the session, which with no device fails saying it needs
// one, so a refusal removed from a method fails here rather than passing
// on the nil device's error.
package windows_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/svc/windows"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/operand"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// refusedBeforeConnecting fails t when a name the operand rule refuses got
// as far as building the session, which with no device fails saying it
// "needs a target device" (winrmSession).
func refusedBeforeConnecting(t *testing.T, err error, name string) {
	t.Helper()
	if operand.Problem(name) != "" && strings.Contains(err.Error(), "needs a target device") {
		t.Fatalf("service %q, which the operand rule refuses, reached the session: %v", name, err)
	}
}

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
			refusedBeforeConnecting(t, err, name)
		}
	})
}
