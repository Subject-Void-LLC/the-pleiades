// The "virt.vbox.vm.stop" method: stop a VM by its power button or its power.
package vm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.vbox.vm.stop",
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
			Reversibility:   collection.Reversibility{Reversible: true, Notes: "A run that stopped a running VM emits virt.vbox.vm.start naming it; one that found it stopped emits nothing. Starting it again does not bring back what a power cut lost."},
			SupportsCheck:   true,
			Doc: collection.Doc{
				Summary:     "Stops a VirtualBox VM, by its power button or by cutting its power.",
				Description: "Makes sure a VM is not running. A VM that is already off, saved or aborted reports no change. mode acpi presses the VM's power button and waits for the guest to shut itself down, failing, rather than cutting the power, if it has not within timeout; mode poweroff stops it at once, as pulling its plug would, and loses whatever the guest had not written. Once the VM is off, its autostart mark is cleared, which virt.vbox.vm.start sets on a Windows host. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and sends nothing.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
					{Name: "mode", Type: "string", Default: "acpi", Choices: []string{"acpi", "poweroff"}, Description: "acpi asks the guest to shut down; poweroff cuts its power."},
					{Name: "timeout", Type: "int", Default: "120", Description: "With mode acpi, how many seconds to wait for the guest to shut down before failing."},
				},
				Returns: []collection.ReturnField{
					{Name: "state", Type: "string", Returned: "always", Description: "The VM's state after this task, read back from the host."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "The VM's state before this task and after it."},
				},
				Examples: []collection.Example{
					{Name: "Shut a VM down", RunbookYAML: "- name: Shut the lab VM down\n  virt.vbox.vm.stop:\n    name: ubuntu-lab\n"},
					{Name: "Cut a VM's power", RunbookYAML: "- name: Power the lab VM off now\n  virt.vbox.vm.stop:\n    name: ubuntu-lab\n    mode: poweroff\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.start"},
			},
		},
		Invoke: Stop,
		Check:  CheckStop,
	})
}

// The stop modes.
const (
	modeACPI     = "acpi"
	modePowerOff = "poweroff"
)

// pollInterval is how often a stop looks at the VM again; a test shortens
// it.
var pollInterval = 2 * time.Second

// Stop implements "virt.vbox.vm.stop".
func Stop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runStop(ctx, rc, device, params, collection.ModeExecute)
}

// CheckStop is "virt.vbox.vm.stop"'s check: it reads the VM and sends
// nothing.
func CheckStop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runStop(ctx, rc, device, params, collection.ModeCheck)
}

// runStop stops a running VM and clears the autostart mark
// virt.vbox.vm.start leaves on, so the VM ends not running and not set to
// start with the host.
func runStop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.stop"
	stopMode := modeACPI
	if text := sdk.StringParam(params, paramMode); text != "" {
		stopMode = text
	}
	if stopMode != modeACPI && stopMode != modePowerOff {
		return collection.Result{}, fmt.Errorf("%s: mode %q is not acpi or poweroff", fqcn, stopMode)
	}
	timeout, set, err := sdk.IntParam(params, paramTimeout)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !set {
		timeout = 120
	}
	if timeout <= 0 {
		return collection.Result{}, fmt.Errorf("%s: timeout must be a positive number of seconds", fqcn)
	}
	h, name, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	before, err := mustRead(ctx, h, name, device, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	running := !before.Off()
	if running && before.State == vboxmanage.StatePaused && stopMode == modeACPI {
		return collection.Result{}, fmt.Errorf("%s: %q is paused, so its guest cannot answer the power button; use mode: poweroff", fqcn, name)
	}
	changed := running || before.AutostartEnabled
	if mode == collection.ModeCheck {
		predicted := before
		if running {
			predicted.State = vboxmanage.StatePoweroff
		}
		predicted.AutostartEnabled = false
		return collection.Result{Changed: changed}, recordStop(rc, fqcn, before, predicted)
	}
	if running {
		if err := stopVM(ctx, h, name, stopMode, time.Duration(timeout)*time.Second); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	if before.AutostartEnabled {
		if err := clearAutostart(ctx, h, name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	after, err := mustRead(ctx, h, name, device, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	if err := recordStop(rc, fqcn, before, after); err != nil {
		return collection.Result{}, err
	}
	if running {
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:        "virt.vbox.vm.start",
			Params:      map[string]any{paramName: name},
			Description: fmt.Sprintf("Start %s again, as it was running before this task stopped it.", name),
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	return collection.Result{Changed: changed}, nil
}

// stopVM stops a running VM. With acpi it presses the power button and
// waits for the guest to shut down, failing rather than cutting the power
// when it has not within timeout.
func stopVM(ctx context.Context, h vboxmanage.Host, name, stopMode string, timeout time.Duration) error {
	if stopMode == modePowerOff {
		err := h.PowerOff(ctx, name)
		if errors.Is(err, vboxmanage.ErrNotRunning) {
			return nil
		}
		return err
	}
	if err := h.ACPIShutdown(ctx, name); err != nil && !errors.Is(err, vboxmanage.ErrNotRunning) {
		return err
	}
	deadline := time.Now().Add(timeout)
	for {
		m, err := h.Machine(ctx, name)
		if err != nil {
			return err
		}
		if m.Off() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%q was asked to shut down and is still %s after %s; its guest may not answer the power button, and mode: poweroff cuts its power", name, m.State, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// clearAutostart clears the VM's autostart mark. VirtualBox refuses to
// change a machine's settings until the session a stop ends has let go of
// it, which can take a moment after the VM is off, so a refusal is tried
// again a few times.
func clearAutostart(ctx context.Context, h vboxmanage.Host, name string) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = h.SetAutostart(ctx, name, false); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
	return err
}

// recordStop sets a stop's stats and diff.
func recordStop(rc sdk.RunbookContext, fqcn string, before, after vboxmanage.Machine) error {
	if err := rc.SetStat(statState, after.State); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: stateView(before), After: stateView(after)}); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	return nil
}
