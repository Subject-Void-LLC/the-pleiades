// The "virt.vbox.vm.start" method: start a VM with no window.
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
		Name: "virt.vbox.vm.start",
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
			Reversibility:   collection.Reversibility{Reversible: true, Notes: "A run that started a stopped VM emits virt.vbox.vm.stop naming it; one that found it running emits nothing. Stopping it does not undo what the guest did while it ran."},
			SupportsCheck:   true,
			Doc: collection.Doc{
				Summary:     "Starts a VirtualBox VM with no window.",
				Description: "Makes sure a VM is running, started headless. A VM that is already running reports no change. A paused VM is refused, since starting is not resuming. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. On a Windows host, a VM cannot start from the WinRM logon this task uses: Windows' catalog signature check fails for a non-administrator logon that is not interactive, and VirtualBox's hardening then refuses the hypervisor (VirtualBox ticket 20341). So when none of the account's VMs is running, the VM is marked for autostart and started by the account's VirtualBox autostart service, which runs under a service logon; examples/windows_lab/winrm-cert-setup.ps1 -VirtualBoxAutostart installs it. While any of the account's VMs runs, a plain start works, because every client is then handed the VirtualBox server that VM keeps alive. The autostart mark stays on while the VM runs, since VirtualBox refuses to change a running VM's settings, so a host restart in that time starts it again; virt.vbox.vm.stop clears it. A check reads the VM and sends nothing.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
				},
				Returns: []collection.ReturnField{
					{Name: "state", Type: "string", Returned: "always", Description: "The VM's state after this task, read back from the host."},
					{Name: "via_autostart_service", Type: "bool", Returned: "always", Description: "Whether the start went through the account's autostart service. False when nothing was started."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "The VM's state before this task and after it."},
				},
				Examples: []collection.Example{
					{Name: "Start a VM", RunbookYAML: "- name: Start the lab VM\n  virt.vbox.vm.start:\n    name: ubuntu-lab\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.stop", "virt.vbox.vm.info"},
			},
		},
		Invoke: Start,
		Check:  CheckStart,
	})
}

// Start implements "virt.vbox.vm.start".
func Start(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runStart(ctx, rc, device, params, collection.ModeExecute)
}

// CheckStart is "virt.vbox.vm.start"'s check: it reads the VM, and the
// running VMs that decide how a start would go, and sends nothing.
func CheckStart(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runStart(ctx, rc, device, params, collection.ModeCheck)
}

// runStart starts a VM that is not running. On a Windows host with none of
// the account's VMs running, the start goes through the account's
// autostart service (vboxmanage.WindowsAutostart says why).
func runStart(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.start"
	h, name, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	before, err := mustRead(ctx, h, name, device, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	if before.State == vboxmanage.StatePaused {
		return collection.Result{}, fmt.Errorf("%s: %q is paused; starting is not resuming, and this method does not resume", fqcn, name)
	}
	if before.State == vboxmanage.StateRunning {
		return collection.Result{}, record(rc, fqcn, before, before, false)
	}
	if mode == collection.ModeCheck {
		running, err := h.Running(ctx)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		predicted := before
		predicted.State = vboxmanage.StateRunning
		predicted.AutostartEnabled = before.AutostartEnabled || len(running) == 0
		return collection.Result{Changed: true}, record(rc, fqcn, before, predicted, len(running) == 0)
	}
	via, err := vboxmanage.WindowsAutostart{Host: h}.Start(ctx, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	after, err := mustRead(ctx, h, name, device, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	if err := record(rc, fqcn, before, after, via); err != nil {
		return collection.Result{}, err
	}
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:        "virt.vbox.vm.stop",
		Params:      map[string]any{paramName: name},
		Description: fmt.Sprintf("Shut %s down by its power button, as it was before this task started it.", name),
	}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}

// record sets a start's stats and diff.
func record(rc sdk.RunbookContext, fqcn string, before, after vboxmanage.Machine, via bool) error {
	if err := rc.SetStat(statState, after.State); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statViaAutostartService, via); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: stateView(before), After: stateView(after)}); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	return nil
}
