package pkg

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "pkg.install",
		Manifest: genericManifest(
			collection.Reversibility{
				Reversible: true,
				Notes: "The inverse is whatever the concrete method records, so on an APT host a run that installed an " +
					"absent package emits a pkg.apt.remove and a run that found it already present emits nothing. The " +
					"instruction names the concrete method rather than pkg.remove, which is deliberate: by the time an " +
					"undo runs, the device that was resolved is the device that must be undone.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "pkg.apt.remove", Record: []string{"name"}},
					{FQCN: "pkg.dnf.remove", Record: []string{"name"}},
				},
			},
			installDoc(),
		),
		Invoke: Install,
		Check:  CheckInstall,
	})
}

func installDoc() collection.Doc {
	return genericDoc(
		"Makes sure a package is present, whichever package manager the device runs.",
		"Makes sure a package is installed, without caring which package manager the device uses. This is ansible.builtin.package with state=present: it resolves the device's package manager and hands the call to that manager's concrete method, so on an APT host it runs pkg.apt.install and behaves exactly as that method does, including reporting no change when the package is already present at the requested version. Use the concrete method instead when a runbook is written for one platform and should say so.",
		[]collection.Example{
			{
				Name:        "Install a package without naming the package manager",
				RunbookYAML: "- name: Make sure curl is installed\n  pkg.install:\n    name: curl\n",
			},
		},
		[]string{"pkg.remove", "pkg.upgrade", "pkg.apt.install", "pkg.dnf.install"},
	)
}

// Install implements "pkg.install" by resolving the device's package
// manager and handing the call to that manager's concrete method.
func Install(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return dispatch(ctx, rc, device, params, "install", collection.ModeExecute)
}

// CheckInstall is pkg.install's check: the device's package manager's own
// check (dispatch), or "cannot check this call" for a manager whose
// method has none.
func CheckInstall(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return dispatch(ctx, rc, device, params, "install", collection.ModeCheck)
}
