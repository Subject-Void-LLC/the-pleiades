// The "virt.vbox.snapshot.take" method: a snapshot under a name no other has.
package snapshot

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.vbox.snapshot.take",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"winrm",
			},
			RequiredCapabilities: []capability.Name{
				capability.Name("VirtualBoxCapable"),
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
			},
			// TODO(forge): PlatformTargets narrows this manifest to a
			// specific vendor, model, firmware range, or deployment
			// context (pkg/collection.PlatformTarget). Left nil: thin
			// flag parsing only, per Phase 33's own checklist. Add real
			// entries by hand once this method's platform scope is known.
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "A run that took a snapshot emits virt.vbox.snapshot.delete naming it by UUID, so the undo deletes that snapshot and no other of the same name; one that found a snapshot under the name emits nothing.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "virt.vbox.snapshot.delete", Record: []string{"vm", "name", "uuid"}},
				},
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Takes a snapshot of a VirtualBox VM under a name no other snapshot of it has.",
				Description: "Makes sure a VM has a snapshot with this name. A snapshot already under the name reports no change and takes no second one, although VirtualBox itself would. A running VM can be snapshotted; a snapshot of a running VM holds its memory too. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and sends nothing.",
				Params: []collection.Param{
					{Name: "vm", Type: "string", Required: true, Description: "The VM whose snapshot this is, named as for virt.vbox.vm.start."},
					{Name: "name", Type: "string", Required: true, Description: "The snapshot's name, under the same rule as a VM's. VirtualBox lets two snapshots share a name; virt.vbox.snapshot.take never makes a second, and the methods that find a snapshot by name refuse one that matches more than one unless uuid says which."},
					{Name: "description", Type: "string", Description: "Text stored with the snapshot, at most 200 characters on one line."},
				},
				Returns: []collection.ReturnField{
					{Name: "uuid", Type: "string", Returned: "always", Description: "The snapshot's UUID: the one taken, or the one already under the name."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "Whether a snapshot under the name existed before this task and after it."},
				},
				Examples: []collection.Example{
					{Name: "Snapshot a clean install", RunbookYAML: "- name: Keep the clean install\n  virt.vbox.snapshot.take:\n    vm: ubuntu-lab\n    name: clean\n    description: fresh install, before any test\n"},
				},
				SeeAlso: []string{"virt.vbox.snapshot.restore", "virt.vbox.snapshot.delete"},
			},
		},
		Invoke: Take,
		Check:  CheckTake,
	})
}

// Take implements "virt.vbox.snapshot.take".
func Take(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runTake(ctx, rc, device, params, collection.ModeExecute)
}

// CheckTake is "virt.vbox.snapshot.take"'s check: it reads the VM and
// sends nothing.
func CheckTake(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runTake(ctx, rc, device, params, collection.ModeCheck)
}

// runTake takes a snapshot under the name unless the VM already has one
// there. It takes no uuid: a take names a snapshot that may not exist yet.
func runTake(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.snapshot.take"
	if _, set := params[paramUUID]; set {
		return collection.Result{}, fmt.Errorf("%s: takes no uuid; VirtualBox chooses a new snapshot's", fqcn)
	}
	description := sdk.StringParam(params, paramDescription)
	if err := vboxmanage.CheckDescription(description); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	h, r, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	m, err := machine(ctx, h, r, device, fqcn, mode)
	if err != nil {
		return collection.Result{}, err
	}
	// A snapshot already under the name is the one reported. When
	// something other than this method made several, that is the first
	// VBoxManage lists, the one nearest the tree's root.
	if named := m.SnapshotsNamed(r.name); len(named) > 0 {
		if err := rc.SetStat(statUUID, named[0].UUID); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{}, recordExists(rc, fqcn, true, true)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, recordExists(rc, fqcn, false, true)
	}
	uuid, err := h.TakeSnapshot(ctx, r.vm, r.name, description)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statUUID, uuid); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := recordExists(rc, fqcn, false, true); err != nil {
		return collection.Result{}, err
	}
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:        "virt.vbox.snapshot.delete",
		Params:      map[string]any{paramVM: r.vm, paramName: r.name, paramUUID: uuid},
		Description: fmt.Sprintf("Delete snapshot %s of %s, which this task took.", r.name, r.vm),
	}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}
