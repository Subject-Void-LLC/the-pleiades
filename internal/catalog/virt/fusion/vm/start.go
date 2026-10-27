// The "virt.fusion.vm.start" method: start a VM with no window and hold the
// Mac awake while it runs.
package vm

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
		Name: "virt.fusion.vm.start",
		Manifest: collection.Manifest{
			SupportedTransports: []string{"ssh"},
			RequiredCapabilities: []capability.Name{
				capability.NameVMwareFusion,
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			EngineVersion: ">=0.2.0",
			Status:        collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "A run that started a VM that was off emits virt.fusion.vm.stop with mode soft; one that resumed a suspended VM emits it with mode suspend; one that found it on emits nothing. Stopping it does not undo what the guest did while it ran.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "virt.fusion.vm.stop", Record: []string{"vmx", "mode"}},
				},
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Starts a VMware Fusion VM with no window, and holds the Mac awake while it runs.",
				Description: "Makes sure a VM is running, started headless with vmrun. A VM that is already on reports no change. A suspended VM resumes where it was. A paused VM is refused, since starting is not resuming. Unless keep_awake is false, the Mac is held awake for as long as the VM's process runs (caffeinate -w on that process): macOS throttles a VM started from an SSH session as background work, and a Windows guest started that way ran about five times slower than its own clock until it was held awake. vmrun's output is read from a file rather than a pipe, because the VM process it starts keeps that output open for its whole life. The task's target is the Mac (a device onboarding found VMware Fusion answering on), not the VM, which is a resource on it, named by the path of its vmx file. A check reads the VM and sends nothing.",
				Params: []collection.Param{
					{Name: "vmx", Type: "string", Required: true, Description: "The absolute path of the VM's .vmx file on the Mac, such as /Users/me/pleiades-vms/win11-arm/win11-arm.vmx. Spaces are allowed; a path that is not absolute, does not end in .vmx, or holds a control character is refused."},
					{Name: "keep_awake", Type: "bool", Default: "true", Description: "Hold the Mac awake while the VM runs. Set it false only for a Mac that is kept awake some other way: a throttled VM runs, slowly, and can stall its own guest's work."},
				},
				Returns: []collection.ReturnField{
					{Name: "state", Type: "string", Returned: "always", Description: "The VM's power state after this task, read back from the Mac."},
					{Name: "kept_awake", Type: "bool", Returned: "always", Description: "Whether this task started holding the Mac awake for the VM. False when nothing was started."},
					{Name: "vmx_pid", Type: "int", Returned: "when kept_awake", Description: "The process ID of the VM's vmware-vmx, which the hold on the Mac lasts as long as."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "The VM's power state before this task and after it."},
				},
				Examples: []collection.Example{
					{Name: "Start a VM", RunbookYAML: "- name: Start the Windows VM\n  virt.fusion.vm.start:\n    vmx: /Users/me/pleiades-vms/win11-arm/win11-arm.vmx\n"},
				},
				SeeAlso: []string{"virt.fusion.vm.stop", "virt.fusion.vm.info"},
			},
		},
		Invoke: Start,
		Check:  CheckStart,
	})
}

// Start implements "virt.fusion.vm.start".
func Start(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runStart(ctx, rc, device, params, collection.ModeExecute)
}

// CheckStart is "virt.fusion.vm.start"'s check: it reads the VM and sends
// nothing.
func CheckStart(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runStart(ctx, rc, device, params, collection.ModeCheck)
}

// runStart starts a VM that is off or suspended, then holds the Mac awake for
// the VM's process unless keep_awake is false.
func runStart(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.fusion.vm.start"
	keepAwake, err := sdk.BoolParamOr(params, paramKeepAwake, true)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	h, vmx, done, err := target(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer done()
	before, err := readFor(ctx, h, vmx, device, fqcn, mode)
	if err != nil {
		return collection.Result{}, err
	}
	switch before {
	case statePaused:
		return collection.Result{}, fmt.Errorf("%s: %s is paused; starting is not resuming, and this method does not unpause", fqcn, vmx)
	case stateOn:
		if err := rc.SetStat(statKeptAwake, false); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{}, recordState(rc, fqcn, before, before)
	}
	if mode == collection.ModeCheck {
		if err := rc.SetStat(statKeptAwake, keepAwake); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{Changed: true}, recordState(rc, fqcn, before, stateOn)
	}
	if err := h.Start(ctx, vmx, false); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	// The undo is recorded as soon as the VM runs, before anything else that
	// could fail, so a run that stops after the start can still put it back.
	undoMode := "soft"
	if before == stateSuspended {
		undoMode = "suspend"
	}
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:        "virt.fusion.vm.stop",
		Params:      map[string]any{paramVMX: vmx, paramMode: undoMode},
		Description: fmt.Sprintf("Put %s back as it was before this task started it (%s).", vmx, before),
	}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	kept := false
	if keepAwake {
		pid, err := h.KeepAwake(ctx, vmx)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: the VM started, and holding the Mac awake for it failed, so it may run throttled: %w", fqcn, err)
		}
		kept = true
		if err := rc.SetStat(statPID, pid); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	if err := rc.SetStat(statKeptAwake, kept); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	after, err := h.PowerState(ctx, vmx)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, recordState(rc, fqcn, before, after)
}
