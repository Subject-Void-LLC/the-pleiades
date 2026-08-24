package svc

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "svc.disable",
		Manifest: genericManifest(
			collection.Reversibility{
				Reversible: true,
				Notes: "The inverse is whatever the concrete method records, so on a systemd host a run that disabled an " +
					"enabled service emits an svc.systemd.enable and a run that found it already disabled emits " +
					"nothing. That concrete method also records what its own undo cannot preserve, such as a unit " +
					"that was enabled only until the next reboot.",
			},
			disableDoc(),
		),
		Invoke: Disable,
	})
}

func disableDoc() collection.Doc {
	return genericDoc(
		"Stops a service starting at boot, whichever service manager the device runs.",
		"Makes sure a service is not set to start at boot, without caring which init system the device uses. This is ansible.builtin.service with enabled=no: it resolves the device's service manager and hands the call to that manager's concrete method. It changes the boot-time setting only, so a service disabled by this task keeps running until svc.stop runs or the device reboots.",
		[]collection.Example{
			{
				Name:        "Take a service out of the boot sequence",
				RunbookYAML: "- name: Stop the service coming back after a reboot\n  svc.disable:\n    name: legacy-app\n",
			},
		},
		[]string{"svc.enable", "svc.stop", "svc.systemd.disable"},
	)
}

// Disable implements "svc.disable" by resolving the device's service
// manager and handing the call to that manager's concrete method.
func Disable(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return dispatch(ctx, rc, device, params, "disable")
}
