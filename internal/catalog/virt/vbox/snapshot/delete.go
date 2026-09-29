// The "virt.vbox.snapshot.delete" method: delete a snapshot of a VM.
package snapshot

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.vbox.snapshot.delete",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"winrm",
			},
			RequiredCapabilities: []capability.Name{
				capability.Name("VirtualBoxCapable"),
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			// TODO(forge): PlatformTargets narrows this manifest to a
			// specific vendor, model, firmware range, or deployment
			// context (pkg/collection.PlatformTarget). Left nil: thin
			// flag parsing only, per Phase 33's own checklist. Add real
			// entries by hand once this method's platform scope is known.
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility:   collection.Reversibility{Reversible: false, Notes: "Deleting merges the snapshot's saved disk state into what came after it and drops its saved memory, so the state it held is gone; a new snapshot taken under the same name would hold the VM as it is now, not as it was."},
			SupportsCheck:   true,
			Doc: collection.Doc{
				Summary:     "Deletes a snapshot of a VirtualBox VM.",
				Description: "Makes sure a VM has no snapshot with this name. None under the name reports no change. The snapshot's saved state is merged away and cannot be brought back, so this cannot be undone. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and sends nothing.",
				Params: []collection.Param{
					{Name: "vm", Type: "string", Required: true, Description: "The VM whose snapshot this is, named as for virt.vbox.vm.start."},
					{Name: "name", Type: "string", Required: true, Description: "The snapshot's name, under the same rule as a VM's. VirtualBox lets two snapshots share a name; virt.vbox.snapshot.take never makes a second, and the methods that find a snapshot by name refuse one that matches more than one unless uuid says which."},
					{Name: "uuid", Type: "string", Description: "The snapshot's UUID, needed only when more than one snapshot of the VM has this name (which only VirtualBox itself, or a tool other than this one, makes). It must be one of them."},
				},
				Returns: []collection.ReturnField{
					{Name: "uuid", Type: "string", Returned: "when a snapshot was deleted", Description: "The snapshot deleted."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "Whether a snapshot under the name existed before this task and after it."},
				},
				Examples: []collection.Example{
					{Name: "Drop a snapshot", RunbookYAML: "- name: Drop the old baseline\n  virt.vbox.snapshot.delete:\n    vm: ubuntu-lab\n    name: clean\n"},
				},
				SeeAlso: []string{"virt.vbox.snapshot.take"},
			},
		},
		Invoke: Delete,
		Check:  CheckDelete,
	})
}

// Delete implements "virt.vbox.snapshot.delete".
func Delete(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runDelete(ctx, rc, device, params, collection.ModeExecute)
}

// CheckDelete is "virt.vbox.snapshot.delete"'s check: it reads the VM and
// sends nothing.
func CheckDelete(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runDelete(ctx, rc, device, params, collection.ModeCheck)
}

// runDelete deletes the snapshot named, when there is one. VirtualBox
// deletes a running VM's snapshot too, merging its disk while the VM runs.
func runDelete(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.snapshot.delete"
	h, r, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	m, err := machine(ctx, h, r, device, fqcn, mode)
	if err != nil {
		return collection.Result{}, err
	}
	s, found, err := find(m, r)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !found {
		return collection.Result{}, recordExists(rc, fqcn, false, false)
	}
	if err := rc.SetStat(statUUID, s.UUID); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	// Another snapshot can share the name; after the delete, the name is
	// still taken when one does.
	remains := len(m.SnapshotsNamed(r.name)) > 1
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, recordExists(rc, fqcn, true, remains)
	}
	if err := h.DeleteSnapshot(ctx, r.vm, s.UUID); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, recordExists(rc, fqcn, true, remains)
}
