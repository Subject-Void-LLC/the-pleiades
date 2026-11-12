// FuzzSystemdUnitName is the svc.systemd parameter fuzz target, which Phase
// 38 requires before a status flips, in the shape of exec's
// FuzzCommandParams.
//
// The boundary is the unit name, out of a runbook file and parsed before
// anything connects. The seeds include a name beginning with a dash and
// one holding a wildcard, the hole FAILURE_PATTERNS 1174 named, and
// systemd's two root units, whose names begin with a dash and are real. The
// device is nil, so any input that parses stops at the connection, which
// keeps every iteration to microseconds and off the network. The properties
// are no panic, no change claimed beside an error, and the unit rule held in
// both directions: a name operand.SystemdUnit refuses never gets as far as
// the connection (which with no device fails saying there is none), and one
// it admits always does, so -.mount and -.slice reach systemctl while -f
// does not.
package systemd_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/svc/systemd"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/operand"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// heldToTheUnitRule fails t when a name operand.SystemdUnit refuses got as
// far as the connection, or one it admits did not.
func heldToTheUnitRule(t *testing.T, err error, name string) {
	t.Helper()
	connected := strings.Contains(err.Error(), "no target device")
	switch refused := operand.SystemdUnit.Problem(name) != ""; {
	case refused && connected:
		t.Fatalf("unit %q, which the unit rule refuses, reached the connection: %v", name, err)
	case !refused && name != "" && !connected:
		t.Fatalf("unit %q, which the unit rule admits, was refused before the connection: %v", name, err)
	}
}

func FuzzSystemdUnitName(f *testing.F) {
	f.Add("nginx")
	f.Add("")
	f.Add("getty@tty1.service")
	f.Add("-H")
	f.Add("s*")
	f.Add("unit; touch /tmp/x")
	f.Add("\x00\n'")
	f.Add("-.mount")
	f.Add("-.slice")
	f.Add("-.swap")
	f.Add("-f")

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
			heldToTheUnitRule(t, err, name)
		}
	})
}
