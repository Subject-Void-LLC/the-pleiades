// Package inventory_test: ExecIsPOSIX, which decides whether a device's SSH
// commands are handed to sh, against stub devices.
package inventory_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

func TestExecIsPOSIX_OnlyANetworkCLIOrAProvedNonPOSIXShellIsNot(t *testing.T) {
	discovered := func(shell string) map[string]inventory.PropertyValue {
		d := inventory.Discovery{Protocol: "ssh", Facts: map[string]any{inventory.ShellFact: shell}}
		return map[string]inventory.PropertyValue{inventory.DiscoveredProperty: d.Property()}
	}
	cases := []struct {
		name string
		item inventory.InventoryItem
		want bool
	}{
		{"no device", nil, true},
		{"a host", &inventorytest.Stub{StubName: "h", Caps: []capability.Name{capability.NameSSHTransport}}, true},
		{"an engine host, which declares no POSIX file system", &inventorytest.Stub{StubName: "e", Caps: []capability.Name{capability.NameContainerEngine}}, true},
		{"a network CLI", &inventorytest.Stub{StubName: "sw", Caps: []capability.Name{capability.NameSSHTransport, capability.NameNetworkCLI}}, false},
		{"onboarded posix", &inventorytest.Stub{StubName: "g", Props: discovered("posix")}, true},
		{"onboarded with no posix sh", &inventorytest.Stub{StubName: "w", Props: discovered(inventory.NotPOSIX)}, false},
		// An onboarding before the shell-agnostic change wrote "not posix" for
		// a fish or tcsh LOGIN on a device that has sh. Read as no sh, every
		// command went raw to fish, where a quoted value can run as code.
		{"onboarded before the change, with a fish login", &inventorytest.Stub{StubName: "f", Props: discovered("not posix")}, true},
	}
	for _, tc := range cases {
		if got := inventory.ExecIsPOSIX(tc.item); got != tc.want {
			t.Errorf("%s: ExecIsPOSIX = %v, want %v", tc.name, got, tc.want)
		}
	}
}
