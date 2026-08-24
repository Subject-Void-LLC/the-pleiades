package svc

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "svc.enable",
		Manifest: genericManifest(
			collection.Reversibility{
				Reversible: true,
				Notes: "The inverse is whatever the concrete method records, so on a systemd host a run that enabled a " +
					"disabled service emits an svc.systemd.disable and a run that found it already enabled emits " +
					"nothing. The undo restores the boot-time setting only, never stopping a service that is running.",
			},
			enableDoc(),
		),
		Invoke: Enable,
	})
}

func enableDoc() collection.Doc {
	return genericDoc(
		"Makes a service start at boot, whichever service manager the device runs.",
		"Makes sure a service is set to start at boot, without caring which init system the device uses. This is ansible.builtin.service with enabled=yes: it resolves the device's service manager and hands the call to that manager's concrete method. It changes the boot-time setting only, so a service enabled by this task is not running until svc.start runs or the device reboots.",
		[]collection.Example{
			{
				Name:        "Enable and start, in that order",
				RunbookYAML: "- name: Make sure the service comes back after a reboot\n  svc.enable:\n    name: app\n\n- name: And make sure it is running now\n  svc.start:\n    name: app\n",
			},
		},
		[]string{"svc.disable", "svc.start", "svc.systemd.enable"},
	)
}

// Enable implements "svc.enable" by resolving the device's service
// manager and handing the call to that manager's concrete method.
func Enable(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return dispatch(ctx, rc, device, params, "enable")
}
