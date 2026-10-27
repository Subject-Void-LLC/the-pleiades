// Package vm implements the virt.fusion.vm.* methods: reading, starting and
// stopping a VM on a Mac that runs VMware Fusion, through pkg/vmrun over the
// Mac's own SSH connection. The target device is the Mac; the VM is a
// resource on it, named by the path of its vmx file.
package vm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vmrun"
)

// Parameter and stat names.
const (
	paramVMX       = "vmx"
	paramMode      = "mode"
	paramKeepAwake = "keep_awake"

	statExists    = "exists"
	statState     = "state"
	statGuestIP   = "guest_ip"
	statKeptAwake = "kept_awake"
	statPID       = "vmx_pid"
)

// Fusion's power states, as vmcli's power query names them.
const (
	stateOn        = "on"
	stateOff       = "off"
	stateSuspended = "suspended"
	statePaused    = "paused"
)

// callTimeout bounds one method's work on the Mac. A soft stop waits for the
// guest to shut down, which a Windows guest can take minutes to do.
const callTimeout = 10 * time.Minute

// hostFunc builds the Fusion host a task targets from the Mac's SSH
// connection, and the function that closes it. A test swaps it for a host
// whose runner answers from a model; the lab runs the real one.
var hostFunc = func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (vmrun.Host, func(), error) {
	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return vmrun.Host{}, nil, err
	}
	h := vmrun.Host{Runner: vmrun.SSHRunner{Conn: conn}, Product: vmrun.Fusion, VMRun: vmrun.FusionVMRun, VMCLI: vmrun.FusionVMCLI}
	return h, func() { _ = conn.Close() }, nil
}

// target reads a method's vmx parameter, checks it, and builds the host.
func target(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (vmrun.Host, string, func(), error) {
	vmx, err := sdk.RequiredStringParam(params, paramVMX)
	if err != nil {
		return vmrun.Host{}, "", nil, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := vmrun.CheckVMX(vmx); err != nil {
		return vmrun.Host{}, "", nil, fmt.Errorf("%s: %w", fqcn, err)
	}
	h, done, err := hostFunc(ctx, rc, device, params, fqcn)
	if err != nil {
		return vmrun.Host{}, "", nil, fmt.Errorf("%s: %w", fqcn, err)
	}
	return h, vmx, done, nil
}

// read returns the VM's power state, and false when its vmx file is not on
// the Mac, which is an answer rather than a failure.
func read(ctx context.Context, h vmrun.Host, vmx string) (string, bool, error) {
	exists, err := h.VMXExists(ctx, vmx)
	if err != nil || !exists {
		return "", false, err
	}
	state, err := h.PowerState(ctx, vmx)
	if err != nil {
		return "", false, err
	}
	return state, true, nil
}

// readFor is read for a method that acts on the VM. A missing VM fails it,
// naming the Mac, except in a check, where an earlier task in the same run
// may be what makes it, so the call is unchecked instead.
func readFor(ctx context.Context, h vmrun.Host, vmx string, device inventory.InventoryItem, fqcn string, mode collection.Mode) (string, error) {
	state, exists, err := read(ctx, h, vmx)
	if err != nil {
		return "", fmt.Errorf("%s: %w", fqcn, err)
	}
	if !exists {
		if mode == collection.ModeCheck {
			return "", collection.CannotCheck(fmt.Sprintf("%q has no VM at %s yet; a real run fails on it unless an earlier task in the run makes it", device.Name(), vmx))
		}
		return "", fmt.Errorf("%s: %q has no VM at %s", fqcn, device.Name(), vmx)
	}
	return state, nil
}

// recordState sets a start's or stop's state stat and its diff.
func recordState(rc sdk.RunbookContext, fqcn, before, after string) error {
	if err := rc.SetStat(statState, after); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: map[string]any{statState: before}, After: map[string]any{statState: after}}); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	return nil
}

// guestIP is the guest's address when VMware Tools answers, and "" when the
// tools are not running, which is not a failure of a read.
func guestIP(ctx context.Context, h vmrun.Host, vmx string) (string, error) {
	ip, err := h.GuestIP(ctx, vmx)
	if errors.Is(err, vmrun.ErrNoTools) || errors.Is(err, vmrun.ErrNotRunning) {
		return "", nil
	}
	return ip, err
}
