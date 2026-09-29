// The "virt.vbox.snapshot.restore" method: put a VM back to a snapshot.
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
		Name: "virt.vbox.snapshot.restore",
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
			Reversibility:   collection.Reversibility{Reversible: false, Notes: "Restoring discards everything the VM did since the snapshot, and nothing keeps what was discarded, so there is no state an undo could put back. Take a snapshot first to keep it."},
			SupportsCheck:   true,
			Doc: collection.Doc{
				Summary:     "Puts a VirtualBox VM back to a snapshot, discarding its current state.",
				Description: "Restores a VM to a snapshot. Everything the VM did since that snapshot is discarded, so this always reports a change and cannot be undone. A running VM is refused: stop it first with virt.vbox.vm.stop, so that discarding a running machine is a decision a runbook states rather than a side effect. A snapshot that does not exist is refused. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and sends nothing.",
				Params: []collection.Param{
					{Name: "vm", Type: "string", Required: true, Description: "The VM whose snapshot this is, named as for virt.vbox.vm.start."},
					{Name: "name", Type: "string", Required: true, Description: "The snapshot's name, under the same rule as a VM's. VirtualBox lets two snapshots share a name; virt.vbox.snapshot.take never makes a second, and the methods that find a snapshot by name refuse one that matches more than one unless uuid says which."},
					{Name: "uuid", Type: "string", Description: "The snapshot's UUID, needed only when more than one snapshot of the VM has this name (which only VirtualBox itself, or a tool other than this one, makes). It must be one of them."},
				},
				Returns: []collection.ReturnField{
					{Name: "uuid", Type: "string", Returned: "always", Description: "The snapshot restored."},
				},
				Examples: []collection.Example{
					{Name: "Reset to a clean install", RunbookYAML: "- name: Stop the lab VM\n  virt.vbox.vm.stop:\n    name: ubuntu-lab\n    mode: poweroff\n\n- name: Back to the clean install\n  virt.vbox.snapshot.restore:\n    vm: ubuntu-lab\n    name: clean\n\n- name: Start it again\n  virt.vbox.vm.start:\n    name: ubuntu-lab\n"},
				},
				SeeAlso: []string{"virt.vbox.snapshot.take", "virt.vbox.vm.stop"},
			},
		},
		Invoke: Restore,
		Check:  CheckRestore,
	})
}

// Restore implements "virt.vbox.snapshot.restore".
func Restore(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runRestore(ctx, rc, device, params, collection.ModeExecute)
}

// CheckRestore is "virt.vbox.snapshot.restore"'s check: it reads the VM,
// refuses what a real run would, and sends nothing.
func CheckRestore(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runRestore(ctx, rc, device, params, collection.ModeCheck)
}

// runRestore restores a VM that is not running to the snapshot named. It
// always changes something: even a VM whose state descends from that very
// snapshot loses whatever it did since.
func runRestore(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.snapshot.restore"
	h, r, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	m, err := machine(ctx, h, r, device, fqcn, mode)
	if err != nil {
		return collection.Result{}, err
	}
	if !m.Off() {
		return collection.Result{}, fmt.Errorf("%s: %q is %s; stop it first with virt.vbox.vm.stop, since a restore discards its state", fqcn, r.vm, m.State)
	}
	s, found, err := find(m, r)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !found {
		if mode == collection.ModeCheck {
			return collection.Result{}, collection.CannotCheck(fmt.Sprintf("%q has no snapshot named %q yet; a real run fails on it unless an earlier task in the run takes it", r.vm, r.name))
		}
		return collection.Result{}, fmt.Errorf("%s: %q has no snapshot named %q", fqcn, r.vm, r.name)
	}
	if err := rc.SetStat(statUUID, s.UUID); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	if err := h.RestoreSnapshot(ctx, r.vm, s.UUID); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}
