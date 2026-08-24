package svc

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "svc.stop",
		Manifest: genericManifest(
			collection.Reversibility{
				Reversible: true,
				Notes: "The inverse is whatever the concrete method records, so on a systemd host a run that stopped a " +
					"running service emits an svc.systemd.start and a run that found it already stopped emits nothing. " +
					"Starting it again restores the service, never what it missed while it was down.",
			},
			stopDoc(),
		),
		Invoke: Stop,
	})
}

func stopDoc() collection.Doc {
	return genericDoc(
		"Stops a service now, whichever service manager the device runs.",
		"Makes sure a service is not running right now, without caring which init system the device uses. This is ansible.builtin.service with state=stopped: it resolves the device's service manager and hands the call to that manager's concrete method. It stops the service now and leaves the boot-time setting alone, so a service stopped by this task starts again at the next reboot unless svc.disable is also run.",
		[]collection.Example{
			{
				Name:        "Stop a service without naming the init system",
				RunbookYAML: "- name: Stop nginx before maintenance\n  svc.stop:\n    name: nginx\n",
			},
		},
		[]string{"svc.start", "svc.disable", "svc.systemd.stop"},
	)
}

// Stop implements "svc.stop" by resolving the device's service manager
// and handing the call to that manager's concrete method.
func Stop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return dispatch(ctx, rc, device, params, "stop")
}
