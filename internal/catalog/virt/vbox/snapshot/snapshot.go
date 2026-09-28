// Package snapshot implements the virt.vbox.snapshot.* methods: taking,
// restoring and deleting a VM's snapshots on a VirtualBox host, through
// pkg/vboxmanage. The target device is the host; the VM and its snapshots
// are resources on it, named by parameters.
package snapshot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// Parameter, stat and diff names.
const (
	paramVM          = "vm"
	paramName        = "name"
	paramUUID        = "uuid"
	paramDescription = "description"

	statUUID = "uuid"

	diffExists = "exists"
)

// callTimeout bounds each VBoxManage call. Taking a running VM's snapshot
// writes its memory out, and deleting one merges a disk that can be many
// gigabytes, so this is longer than a VM method's.
const callTimeout = 30 * time.Minute

// hostFunc builds the VirtualBox host a task targets from its device and
// credential. A test swaps it for a host whose runner is a model of one;
// the Release Gate in cmd/pleiades runs the real one.
var hostFunc = func(rc sdk.RunbookContext, device inventory.InventoryItem) (vboxmanage.Host, error) {
	return vboxmanage.ForDevice(device, rc.InjectSecrets(), callTimeout)
}

// ref names a snapshot: the VM, the snapshot's name, and, when several
// share the name, which one.
type ref struct {
	vm, name, uuid string
}

// target reads a method's vm, name and uuid parameters, checks them, and
// builds the host.
func target(rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (vboxmanage.Host, ref, error) {
	var r ref
	var err error
	if r.vm, err = sdk.RequiredStringParam(params, paramVM); err != nil {
		return vboxmanage.Host{}, ref{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if r.name, err = sdk.RequiredStringParam(params, paramName); err != nil {
		return vboxmanage.Host{}, ref{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := vboxmanage.CheckName("VM", r.vm); err != nil {
		return vboxmanage.Host{}, ref{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := vboxmanage.CheckName("snapshot", r.name); err != nil {
		return vboxmanage.Host{}, ref{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if r.uuid = sdk.StringParam(params, paramUUID); r.uuid != "" {
		if err := vboxmanage.CheckUUID(r.uuid); err != nil {
			return vboxmanage.Host{}, ref{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	h, err := hostFunc(rc, device)
	if err != nil {
		return vboxmanage.Host{}, ref{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return h, r, nil
}

// machine reads the VM, refusing one that is not registered, naming the
// host. In a check, a VM not there yet makes the call unchecked rather
// than failed, since an earlier task in the same run may create it.
func machine(ctx context.Context, h vboxmanage.Host, r ref, device inventory.InventoryItem, fqcn string, mode collection.Mode) (vboxmanage.Machine, error) {
	m, err := h.Machine(ctx, r.vm)
	if errors.Is(err, vboxmanage.ErrNotFound) {
		if mode == collection.ModeCheck {
			return vboxmanage.Machine{}, collection.CannotCheck(fmt.Sprintf("host %q has no VM named %q yet; a real run fails on it unless an earlier task in the run creates it", device.Name(), r.vm))
		}
		return vboxmanage.Machine{}, fmt.Errorf("%s: host %q has no VM named %q", fqcn, device.Name(), r.vm)
	}
	if err != nil {
		return vboxmanage.Machine{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return m, nil
}

// find returns the snapshot of m that r names, and false when m has none
// under r's name. When several share the name, r's uuid must say which,
// and a uuid that is none of them is refused rather than taken to mean
// there is nothing to do: the snapshot it named is not the one under the
// name now.
func find(m vboxmanage.Machine, r ref) (vboxmanage.Snapshot, bool, error) {
	named := m.SnapshotsNamed(r.name)
	if len(named) == 0 {
		return vboxmanage.Snapshot{}, false, nil
	}
	if r.uuid != "" {
		for _, s := range named {
			if strings.EqualFold(s.UUID, r.uuid) {
				return s, true, nil
			}
		}
		return vboxmanage.Snapshot{}, false, fmt.Errorf("no snapshot of %q named %q has UUID %s; the ones named so are %s",
			r.vm, r.name, r.uuid, uuids(named))
	}
	if len(named) > 1 {
		return vboxmanage.Snapshot{}, false, fmt.Errorf("%d snapshots of %q are named %q (%s); say which with uuid",
			len(named), r.vm, r.name, uuids(named))
	}
	return named[0], true, nil
}

// uuids lists snapshots' UUIDs for a message.
func uuids(snapshots []vboxmanage.Snapshot) string {
	list := make([]string, 0, len(snapshots))
	for _, s := range snapshots {
		list = append(list, s.UUID)
	}
	return strings.Join(list, ", ")
}

// recordExists sets a take's or a delete's diff: whether a snapshot under
// the name existed before the task and after it.
func recordExists(rc sdk.RunbookContext, fqcn string, before, after bool) error {
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: map[string]any{diffExists: before}, After: map[string]any{diffExists: after}}); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	return nil
}
