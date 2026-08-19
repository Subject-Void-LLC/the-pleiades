package pkg

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "pkg.upgrade",
		Manifest: genericManifest(
			collection.Reversibility{
				Reversible: false,
				Notes: "Every concrete upgrade method this resolves to is itself irreversible: downgrading a package " +
					"is not something apt or dnf reliably support once the previous version has left the local cache " +
					"and the repository's own metadata, so no method in this namespace can promise a safe undo for it.",
			},
			upgradeDoc(),
		),
		Invoke: Upgrade,
	})
}

func upgradeDoc() collection.Doc {
	return genericDoc(
		"Makes sure the newest available version of a package is installed, whichever package manager the device runs.",
		"Makes sure a package is at its newest available version, without caring which package manager the device uses. This is ansible.builtin.package with state=latest for one named package, not a full-system upgrade: it resolves the device's package manager and hands the call to that manager's concrete method, so on an APT host it runs pkg.apt.upgrade and behaves exactly as that method does, including installing the package fresh when it is absent and reporting no change when it is already current. Use the concrete method instead when a runbook is written for one platform and should say so.",
		[]collection.Example{
			{
				Name:        "Keep a package current without naming the package manager",
				RunbookYAML: "- name: Keep openssl at its newest available version\n  fqcn: pkg.upgrade\n  params:\n    name: openssl\n",
			},
		},
		[]string{"pkg.install", "pkg.remove", "pkg.apt.upgrade", "pkg.dnf.upgrade"},
	)
}

// Upgrade implements "pkg.upgrade" by resolving the device's package
// manager and handing the call to that manager's concrete method.
func Upgrade(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return dispatch(ctx, rc, device, params, "upgrade")
}
