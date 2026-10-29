// The "virt.vbox.vm.detach_disk" method: a disk taken off a stopped VM,
// and deleted from the host when asked.
package vm

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
		Name: "virt.vbox.vm.detach_disk",
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
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "A run that detached a disk it kept emits virt.vbox.vm.attach_disk back; one that deleted the disk emits nothing, since what it held is gone; one that found it detached emits nothing.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "virt.vbox.vm.attach_disk", Record: []string{"name", "path"}, MayKeepOnFailure: true},
				},
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Takes a disk off a stopped VirtualBox VM, deleting it from the host when asked.",
				Description: "Makes sure the disk at path is not attached to a VM: one attached is taken out of its slot, and with delete true forgotten and deleted from the host too, with everything the guest wrote on it; one not attached reports no change, except that with delete true a disk the host still holds, attached to nothing, is deleted. VirtualBox detaches a disk only while a VM is powered off, so a running, paused or saved VM is refused: stop it first with virt.vbox.vm.stop. A run that detached a disk it kept emits virt.vbox.vm.attach_disk back; one that deleted it emits nothing, since what the disk held is gone. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and the host's disks, and sends nothing.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
					{Name: "path", Type: "string", Required: true, Description: "The disk's path on the host, as virt.vbox.vm.attach_disk reported it."},
					{Name: "delete", Type: "bool", Default: "false", Description: "Whether to forget the disk and delete its file from the host once it is off the VM, losing what the guest wrote on it."},
				},
				Returns: []collection.ReturnField{
					{Name: "detached", Type: "bool", Returned: "always", Description: "Whether this task took the disk off the VM (predicted, in a check)."},
					{Name: "deleted", Type: "bool", Returned: "always", Description: "Whether this task deleted the disk from the host (predicted, in a check)."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "Whether the disk was attached before this task and after it."},
					{Name: "warnings", Type: "list", Returned: "when the VM could not be read back after the disk was taken off", Description: "What the task could not confirm: the disk is off the VM, which the task could not read again."},
				},
				Examples: []collection.Example{
					{Name: "Throw a lab VM's data disk away and start over", RunbookYAML: "- name: Shut the check VM down\n  virt.vbox.vm.stop:\n    name: bsd-check\n\n- name: Delete its data disk\n  virt.vbox.vm.detach_disk:\n    name: bsd-check\n    path: G:\\PleiadesLab\\bsd-check\\bsd-check-data.vdi\n    delete: true\n\n- name: Give it a fresh one\n  virt.vbox.vm.attach_disk:\n    name: bsd-check\n    size_gb: 40\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.attach_disk", "virt.vbox.vm.stop", "virt.vbox.vm.info"},
			},
		},
		Invoke: DetachDisk,
		Check:  CheckDetachDisk,
	})
}

// DetachDisk implements "virt.vbox.vm.detach_disk".
func DetachDisk(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runDetachDisk(ctx, rc, device, params, collection.ModeExecute)
}

// CheckDetachDisk is "virt.vbox.vm.detach_disk"'s check: it reads the VM
// and the host's disks, and sends nothing.
func CheckDetachDisk(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runDetachDisk(ctx, rc, device, params, collection.ModeCheck)
}

// runDetachDisk takes the disk off the VM, and deletes it when asked.
func runDetachDisk(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.detach_disk"
	h, name, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	path, err := sdk.RequiredStringParam(params, paramPath)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := vboxmanage.CheckPath("disk", path); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	del, err := sdk.BoolParamOr(params, paramDelete, false)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	m, err := mustReadFor(ctx, h, name, device, fqcn, mode)
	if err != nil {
		return collection.Result{}, err
	}
	slot, attached := slotHolding(m, path)
	if attached {
		if err := mustBeOff(m, name, fqcn, mode, "detaches a disk from"); err != nil {
			return collection.Result{}, err
		}
	}
	registered := false
	if del {
		if registered, err = h.HasDisk(ctx, path); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	deleting := del && registered
	if mode == collection.ModeCheck {
		return collection.Result{Changed: attached || deleting}, recordDetach(rc, fqcn, attached, deleting, attached)
	}
	var warnings []string
	if attached {
		if err := h.Detach(ctx, name, slot); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		// The disk is off the VM: unless the delete below removes it, the undo
		// stands from here whatever follows.
		if deleting {
			if err := h.DeleteDisk(ctx, path); err != nil {
				if rerr := recordAttachBack(rc, fqcn, name, path); rerr != nil {
					return collection.Result{}, rerr
				}
				return collection.Result{}, fmt.Errorf("%s: %s is off %q and could not be deleted from the host: %w", fqcn, path, name, err)
			}
		} else if err := recordAttachBack(rc, fqcn, name, path); err != nil {
			return collection.Result{}, err
		}
		if m, err = mustRead(ctx, h, name, device, fqcn); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s was taken off %q, and the VM could not be read back afterwards: %v", path, name, err))
		} else if _, still := slotHolding(m, path); still {
			return collection.Result{}, fmt.Errorf("%s: %s was taken off %q and still reads back in one of its slots", fqcn, path, name)
		}
	} else if deleting {
		if err := h.DeleteDisk(ctx, path); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	if err := recordDetach(rc, fqcn, attached, deleting, attached); err != nil {
		return collection.Result{}, err
	}
	if err := sdk.RecordWarnings(rc, warnings); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: attached || deleting}, nil
}

// recordAttachBack records the attach that undoes a detach of a disk still on
// the host, kept through a later failure since the disk is already off the VM.
func recordAttachBack(rc sdk.RunbookContext, fqcn, name, path string) error {
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:          "virt.vbox.vm.attach_disk",
		Params:        map[string]any{paramName: name, paramPath: path},
		Description:   fmt.Sprintf("Attach %s to %s again, as it was before this task.", path, name),
		KeepOnFailure: true,
	}); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	return nil
}

// recordDetach sets a detach's stats and its diff.
func recordDetach(rc sdk.RunbookContext, fqcn string, detached, deleted, before bool) error {
	for key, value := range map[string]any{statDetached: detached, statDeleted: deleted} {
		if err := rc.SetStat(key, value); err != nil {
			return fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: map[string]any{diffAttached: before}, After: map[string]any{diffAttached: false}}); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	return nil
}
