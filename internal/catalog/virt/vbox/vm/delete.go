// The "virt.vbox.vm.delete" method: a stopped VM and its disks, deleted.
package vm

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.vbox.vm.delete",
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
			Reversibility:   collection.Reversibility{Reversible: false, Notes: "Deleting a VM deletes its disks, and nothing keeps what was on them, so there is no VM an undo could bring back. Take a snapshot and keep the base it was cloned from to make it again."},
			SupportsCheck:   true,
			Doc: collection.Doc{
				Summary:     "Deletes a stopped VirtualBox VM and its disks.",
				Description: "Makes sure no VM of this name exists. None reports no change. A running or paused VM is refused: stop it first with virt.vbox.vm.stop. The VM is unregistered and its disks deleted, along with a seed ISO or console log virt.vbox.vm.clone put in its folder; install media attached from anywhere else (a shared ISO) is detached, never deleted. A VM that others were linked-cloned from is refused by VirtualBox while they exist. This cannot be undone. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and sends nothing.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
				},
				Returns: []collection.ReturnField{
					{Name: "uuid", Type: "string", Returned: "when a VM was deleted", Description: "The VM deleted."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "Whether a VM of the name existed before this task and after it."},
				},
				Examples: []collection.Example{
					{Name: "Throw a lab VM away", RunbookYAML: "- name: Power the lab VM off\n  virt.vbox.vm.stop:\n    name: ubuntu-lab\n    mode: poweroff\n\n- name: Delete it\n  virt.vbox.vm.delete:\n    name: ubuntu-lab\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.clone", "virt.vbox.vm.stop"},
			},
		},
		Invoke: Delete,
		Check:  CheckDelete,
	})
}

// Delete implements "virt.vbox.vm.delete".
func Delete(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runDelete(ctx, rc, device, params, collection.ModeExecute)
}

// CheckDelete is "virt.vbox.vm.delete"'s check: it reads the VM and sends
// nothing.
func CheckDelete(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runDelete(ctx, rc, device, params, collection.ModeCheck)
}

// runDelete deletes the VM when there is one and it is not running.
func runDelete(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.delete"
	h, name, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	m, exists, err := read(ctx, h, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !exists {
		return collection.Result{}, recordExists(rc, fqcn, false, false)
	}
	if !m.Off() {
		return collection.Result{}, fmt.Errorf("%s: %q is %s; stop it first with virt.vbox.vm.stop", fqcn, name, m.State)
	}
	if err := rc.SetStat(statUUID, m.UUID); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, recordExists(rc, fqcn, true, false)
	}
	if err := deleteVM(ctx, h, name); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, recordExists(rc, fqcn, true, false)
}

// deleteVM deletes a VM that is not running. Each DVD image is detached,
// and deleted only when it is in the VM's own folder, since one anywhere
// else (an installer ISO) may be shared. The VM is then unregistered with
// its disks, and the seed and console log virt.vbox.vm.clone leaves in its
// folder are removed, with the folder when that leaves it empty.
func deleteVM(ctx context.Context, h vboxmanage.Host, name string) error {
	m, err := h.Machine(ctx, name)
	if err != nil {
		return err
	}
	dir, err := vmDir(m)
	if err != nil {
		return err
	}
	for _, slot := range m.Slots {
		if !strings.HasSuffix(strings.ToLower(slot.Medium), ".iso") {
			continue
		}
		if err := h.Detach(ctx, name, slot); err != nil {
			return err
		}
		if under(dir, slot.Medium) {
			if err := h.CloseDVD(ctx, slot.Medium, true); err != nil {
				return err
			}
		}
	}
	if err := h.Unregister(ctx, name); err != nil {
		return err
	}
	if err := h.Remove(ctx, dir+`\`+seedFile, false); err != nil {
		return err
	}
	return h.Remove(ctx, dir+`\`+consoleFile, true)
}

// under reports whether path is inside dir, without regard to case or to
// which separator VirtualBox wrote.
func under(dir, path string) bool {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "/", `\`)) }
	return strings.HasPrefix(norm(path), norm(dir)+`\`)
}
