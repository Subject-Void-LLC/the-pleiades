// The "virt.vbox.vm.resize" method: a stopped VM's CPUs and memory, by T-shirt size or by count.
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
		Name: "virt.vbox.vm.resize",
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
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "A run that changed the VM emits virt.vbox.vm.resize back to the memory and CPUs it had; one that found them already right emits nothing.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "virt.vbox.vm.resize", Record: []string{"name", "memory_mb", "cpus"}},
				},
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Changes a stopped VirtualBox VM's CPUs and memory, by T-shirt size or by count.",
				Description: "Makes sure a VM has the CPUs and memory asked for: a size, or memory_mb and cpus, either of which alone leaves the other as it is. A VM that has them already reports no change. VirtualBox changes them only while a VM is powered off, so a running, paused or saved VM is refused: stop it first with virt.vbox.vm.stop. A size larger than the host is refused: more CPUs than it has processors online, or more memory than it has. The guest sees the change at its next boot. A run that changed the VM emits virt.vbox.vm.resize back to the CPUs and memory it had. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and the host, and sends nothing.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
					{Name: "size", Type: "string", Choices: []string{"xsmall", "small", "medium", "large", "xlarge"}, Description: "The size to give the VM, a T-shirt size, which sets its CPUs and memory together as a cloud's instance type does: xsmall is 1 CPU and 1024 MB, small 1 CPU and 2048 MB, medium 2 CPUs and 4096 MB, large 4 CPUs and 8192 MB, and xlarge 8 CPUs and 16384 MB. Give size, or memory_mb, cpus or both, not size with either."},
					{Name: "memory_mb", Type: "int", Description: "The memory to give it, in megabytes."},
					{Name: "cpus", Type: "int", Description: "The virtual CPU count to give it."},
				},
				Returns: []collection.ReturnField{
					{Name: "memory_mb", Type: "int", Returned: "always", Description: "Its memory after this task, in megabytes (predicted, in a check)."},
					{Name: "cpus", Type: "int", Returned: "always", Description: "Its virtual CPU count after this task (predicted, in a check)."},
					{Name: "size", Type: "string", Returned: "always", Description: "Its T-shirt size after this task, or empty when its CPUs and memory are not exactly one size's."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "Its memory_mb, cpus and size before this task and after it."},
				},
				Examples: []collection.Example{
					{Name: "Make a lab VM bigger", RunbookYAML: "- name: Shut the lab VM down\n  virt.vbox.vm.stop:\n    name: ubuntu-lab\n\n- name: Make it medium\n  virt.vbox.vm.resize:\n    name: ubuntu-lab\n    size: medium\n\n- name: Start it again\n  virt.vbox.vm.start:\n    name: ubuntu-lab\n"},
					{Name: "Give a VM more memory only", RunbookYAML: "- name: Double the build VM's memory\n  virt.vbox.vm.resize:\n    name: build-vm\n    memory_mb: 8192\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.clone", "virt.vbox.vm.stop", "virt.vbox.vm.info"},
			},
		},
		Invoke: Resize,
		Check:  CheckResize,
	})
}

// Resize implements "virt.vbox.vm.resize".
func Resize(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runResize(ctx, rc, device, params, collection.ModeExecute)
}

// CheckResize is "virt.vbox.vm.resize"'s check: it reads the VM and the
// host, and sends nothing.
func CheckResize(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runResize(ctx, rc, device, params, collection.ModeCheck)
}

// runResize gives a powered-off VM the shape asked for.
func runResize(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.resize"
	h, name, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	m, err := mustReadFor(ctx, h, name, device, fqcn, mode)
	if err != nil {
		return collection.Result{}, err
	}
	before := shapeOf(m)
	want, asked, err := readShape(params, before)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !asked {
		return collection.Result{}, fmt.Errorf("%s: give size, or memory_mb, cpus or both", fqcn)
	}
	if want == before {
		return collection.Result{}, recordResize(rc, fqcn, before, before)
	}
	if err := fitHost(ctx, h, want); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if m.State != vboxmanage.StatePoweroff && m.State != vboxmanage.StateAborted {
		if mode == collection.ModeCheck && (m.State == vboxmanage.StateRunning || m.State == vboxmanage.StatePaused) {
			return collection.Result{}, collection.CannotCheck(fmt.Sprintf("%q is %s now; a real run fails on it unless an earlier task stops it", name, m.State))
		}
		return collection.Result{}, fmt.Errorf("%s: %q is %s; VirtualBox changes a VM's memory and CPUs only while it is powered off, so stop it first with virt.vbox.vm.stop", fqcn, name, m.State)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, recordResize(rc, fqcn, before, want)
	}
	if err := h.Resize(ctx, name, want.memoryMB, want.cpus); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	m, err = mustRead(ctx, h, name, device, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	after := shapeOf(m)
	if after != want {
		return collection.Result{}, fmt.Errorf("%s: %q was given %s and reads back as %s", fqcn, name, want, after)
	}
	if err := recordResize(rc, fqcn, before, after); err != nil {
		return collection.Result{}, err
	}
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:        "virt.vbox.vm.resize",
		Params:      map[string]any{paramName: name, paramMemoryMB: before.memoryMB, paramCPUs: before.cpus},
		Description: fmt.Sprintf("Give %s back the %s it had before this task.", name, before),
	}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}

// recordResize sets a resize's stats, the shape after the task, and its
// diff.
func recordResize(rc sdk.RunbookContext, fqcn string, before, after shape) error {
	for key, value := range after.view() {
		if err := rc.SetStat(key, value); err != nil {
			return fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: before.view(), After: after.view()}); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	return nil
}
