// Package vm implements the virt.vbox.vm.* methods: reading, starting and
// stopping a VM on a VirtualBox host, through pkg/vboxmanage. The target
// device is the host; the VM is a resource on it, named by a parameter.
package vm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// Parameter and stat names.
const (
	paramName    = "name"
	paramMode    = "mode"
	paramTimeout = "timeout"

	statExists              = "exists"
	statUUID                = "uuid"
	statState               = "state"
	statMemoryMB            = "memory_mb"
	statCPUs                = "cpus"
	statAutostartEnabled    = "autostart_enabled"
	statSnapshots           = "snapshots"
	statCurrentSnapshotUUID = "current_snapshot_uuid"
	statViaAutostartService = "via_autostart_service"
)

// callTimeout bounds each VBoxManage call. A start through the autostart
// service makes several, each bounded on its own.
const callTimeout = 5 * time.Minute

// hostFunc builds the VirtualBox host a task targets from its device and
// credential. A test swaps it for a host whose runner answers from a
// script; the Release Gate in cmd/pleiades runs the real one.
var hostFunc = func(rc sdk.RunbookContext, device inventory.InventoryItem) (vboxmanage.Host, error) {
	return vboxmanage.ForDevice(device, rc.InjectSecrets(), callTimeout)
}

// target reads a method's name parameter, checks it, and builds the host.
func target(rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (vboxmanage.Host, string, error) {
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return vboxmanage.Host{}, "", fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := vboxmanage.CheckName("VM", name); err != nil {
		return vboxmanage.Host{}, "", fmt.Errorf("%s: %w", fqcn, err)
	}
	h, err := hostFunc(rc, device)
	if err != nil {
		return vboxmanage.Host{}, "", fmt.Errorf("%s: %w", fqcn, err)
	}
	return h, name, nil
}

// read returns name's machine, and false when no VM of that name is
// registered, which is an answer rather than a failure.
func read(ctx context.Context, h vboxmanage.Host, name string) (vboxmanage.Machine, bool, error) {
	m, err := h.Machine(ctx, name)
	if errors.Is(err, vboxmanage.ErrNotFound) {
		return vboxmanage.Machine{}, false, nil
	}
	if err != nil {
		return vboxmanage.Machine{}, false, err
	}
	return m, true, nil
}

// mustRead is read for a method that acts on the VM, where a missing one
// is refused, naming the host.
func mustRead(ctx context.Context, h vboxmanage.Host, name string, device inventory.InventoryItem, fqcn string) (vboxmanage.Machine, error) {
	m, exists, err := read(ctx, h, name)
	if err != nil {
		return vboxmanage.Machine{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !exists {
		return vboxmanage.Machine{}, fmt.Errorf("%s: host %q has no VM named %q", fqcn, device.Name(), name)
	}
	return m, nil
}

// stateView is the part of a machine a start or stop diff records.
func stateView(m vboxmanage.Machine) map[string]any {
	return map[string]any{statState: m.State, statAutostartEnabled: m.AutostartEnabled}
}
