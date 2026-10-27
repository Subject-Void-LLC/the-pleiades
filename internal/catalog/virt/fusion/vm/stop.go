// The "virt.fusion.vm.stop" method: shut a VM down, power it off, or suspend it.
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
		Name: "virt.fusion.vm.stop",
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
				Notes:      "A run that stopped or suspended a running VM emits virt.fusion.vm.start naming it; one that found it already stopped emits nothing. Starting it again is not the state it had: a soft or hard stop loses what was in its memory.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "virt.fusion.vm.start", Record: []string{"vmx"}},
				},
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Stops or suspends a VMware Fusion VM.",
				Description: "Makes sure a VM is no longer running. Mode soft asks the guest to shut down, and needs VMware Tools running in the guest: vmrun refuses it without them. Mode hard powers it off at once, as pulling the plug would. Mode suspend writes its memory to disk, and virt.fusion.vm.start resumes it where it was. A VM that is already off reports no change, and so does a suspended one when the mode is suspend. A suspended VM asked for soft or hard is refused, since it is not running, and so is a paused one. The task's target is the Mac (a device onboarding found VMware Fusion answering on), not the VM, which is a resource on it, named by the path of its vmx file. A check reads the VM and sends nothing.",
				Params: []collection.Param{
					{Name: "vmx", Type: "string", Required: true, Description: "The absolute path of the VM's .vmx file on the Mac, such as /Users/me/pleiades-vms/win11-arm/win11-arm.vmx. Spaces are allowed; a path that is not absolute, does not end in .vmx, or holds a control character is refused."},
					{Name: "mode", Type: "string", Default: "soft", Choices: []string{"soft", "hard", "suspend"}, Description: "How to stop it: soft (the guest shuts down), hard (powered off at once) or suspend (its memory written to disk)."},
				},
				Returns: []collection.ReturnField{
					{Name: "state", Type: "string", Returned: "always", Description: "The VM's power state after this task, read back from the Mac."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "The VM's power state before this task and after it."},
				},
				Examples: []collection.Example{
					{Name: "Suspend a VM", RunbookYAML: "- name: Put the Windows VM away\n  virt.fusion.vm.stop:\n    vmx: /Users/me/pleiades-vms/win11-arm/win11-arm.vmx\n    mode: suspend\n"},
				},
				SeeAlso: []string{"virt.fusion.vm.start", "virt.fusion.vm.info"},
			},
		},
		Invoke: Stop,
		Check:  CheckStop,
	})
}

// Stop implements "virt.fusion.vm.stop".
func Stop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runStop(ctx, rc, device, params, collection.ModeExecute)
}

// CheckStop is "virt.fusion.vm.stop"'s check: it reads the VM and sends
// nothing.
func CheckStop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runStop(ctx, rc, device, params, collection.ModeCheck)
}

// runStop stops or suspends a running VM.
func runStop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.fusion.vm.stop"
	how, err := sdk.OptionalStringParam(params, paramMode)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if how == "" {
		how = "soft"
	}
	want := stateOff
	switch how {
	case "soft", "hard":
	case "suspend":
		want = stateSuspended
	default:
		return collection.Result{}, fmt.Errorf("%s: mode %q is not soft, hard or suspend", fqcn, how)
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
	switch {
	case before == stateOff, before == want:
		return collection.Result{}, recordState(rc, fqcn, before, before)
	case before == statePaused:
		return collection.Result{}, fmt.Errorf("%s: %s is paused; this method does not stop a paused VM", fqcn, vmx)
	case before != stateOn:
		return collection.Result{}, fmt.Errorf("%s: %s is %s, not running, so there is nothing for mode %s to stop", fqcn, vmx, before, how)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, recordState(rc, fqcn, before, want)
	}
	if how == "suspend" {
		err = h.Suspend(ctx, vmx, false)
	} else {
		err = h.Stop(ctx, vmx, how == "hard")
	}
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:        "virt.fusion.vm.start",
		Params:      map[string]any{paramVMX: vmx},
		Description: fmt.Sprintf("Start %s again, as it was running before this task stopped it.", vmx),
	}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	after, err := h.PowerState(ctx, vmx)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, recordState(rc, fqcn, before, after)
}
