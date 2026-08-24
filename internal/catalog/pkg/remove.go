package pkg

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "pkg.remove",
		Manifest: genericManifest(
			collection.Reversibility{
				Reversible: true,
				Notes: "The inverse is whatever the concrete method records, so on an APT host a run that removed a " +
					"present package emits a pkg.apt.install pinned to the version it captured before removing, and a " +
					"run that found the package already absent emits nothing. The instruction names the concrete " +
					"method rather than pkg.install, for the same reason pkg.start's inverse names svc.systemd.stop " +
					"rather than svc.stop.",
			},
			removeDoc(),
		),
		Invoke: Remove,
	})
}

func removeDoc() collection.Doc {
	return genericDoc(
		"Makes sure a package is absent, whichever package manager the device runs.",
		"Makes sure a package is removed, without caring which package manager the device uses. This is ansible.builtin.package with state=absent: it resolves the device's package manager and hands the call to that manager's concrete method, so on an APT host it runs pkg.apt.remove and behaves exactly as that method does, including reporting no change when the package is already absent. Use the concrete method instead when a runbook is written for one platform and should say so.",
		[]collection.Example{
			{
				Name:        "Remove a package without naming the package manager",
				RunbookYAML: "- name: Make sure telnet is not installed\n  pkg.remove:\n    name: telnet\n",
			},
		},
		[]string{"pkg.install", "pkg.upgrade", "pkg.apt.remove", "pkg.dnf.remove"},
	)
}

// Remove implements "pkg.remove" by resolving the device's package
// manager and handing the call to that manager's concrete method.
func Remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return dispatch(ctx, rc, device, params, "remove")
}
