package svc

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "svc.restart",
		Manifest: genericManifest(
			collection.Reversibility{
				Reversible: false,
				Notes: "A restart's effect is the interruption itself, and no instruction un-interrupts a service. " +
					"Restarting a second time repeats this method rather than reversing it. The concrete method " +
					"still records the service's state under diff, so a rollback reaching this task can see what " +
					"was running and decide for itself.",
			},
			restartDoc(),
		),
		Invoke: Restart,
	})
}

func restartDoc() collection.Doc {
	return genericDoc(
		"Restarts a service, whichever service manager the device runs.",
		"Restarts a service, starting it if it was not running. This is ansible.builtin.service with state=restarted: it resolves the device's service manager and hands the call to that manager's concrete method. It is the one method here that is never converged, since restarting a running service is the point rather than a no-op, so it always reports changed. That makes it the one most worth putting behind a when condition, so a service is only bounced when something it reads actually changed.",
		[]collection.Example{
			{
				Name:        "Restart only when the config changed",
				RunbookYAML: "- name: Write the config\n  file.copy:\n    src: ./app.conf\n    dest: /etc/app/app.conf\n  register: app_config\n\n- name: Restart the service if the config changed\n  svc.restart:\n    name: app\n  when:\n    - app_config.changed\n",
			},
		},
		[]string{"svc.start", "svc.stop", "svc.systemd.restart"},
	)
}

// Restart implements "svc.restart" by resolving the device's service
// manager and handing the call to that manager's concrete method.
func Restart(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return dispatch(ctx, rc, device, params, "restart")
}
