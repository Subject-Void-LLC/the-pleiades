package svc

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "svc.start",
		Manifest: genericManifest(
			collection.Reversibility{
				Reversible: true,
				Notes: "The inverse is whatever the concrete method records, so on a systemd host a run that started a " +
					"stopped service emits an svc.systemd.stop and a run that found it already running emits nothing. " +
					"The instruction names the concrete method rather than svc.stop, which is deliberate: by the time " +
					"an undo runs, the device that was resolved is the device that must be undone.",
			},
			startDoc(),
		),
		Invoke: Start,
	})
}

func startDoc() collection.Doc {
	return genericDoc(
		"Starts a service now, whichever service manager the device runs.",
		"Makes sure a service is running right now, without caring which init system the device uses. This is ansible.builtin.service with state=started: it resolves the device's service manager and hands the call to that manager's concrete method, so on a Linux host it runs svc.systemd.start and behaves exactly as that method does, including reporting no change when the service is already running. Use the concrete method instead when a runbook is written for one platform and should say so.",
		[]collection.Example{
			{
				Name:        "Start a service without naming the init system",
				RunbookYAML: "- name: Make sure nginx is running\n  svc.start:\n    name: nginx\n",
			},
		},
		[]string{"svc.stop", "svc.restart", "svc.enable", "svc.systemd.start"},
	)
}

// Start implements "svc.start" by resolving the device's service manager
// and handing the call to that manager's concrete method.
func Start(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return dispatch(ctx, rc, device, params, "start")
}
