// The "virt.vbox.vm.info" method: a VM's state, hardware and snapshots.
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
		Name: "virt.vbox.vm.info",
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
			Reversibility:   collection.Reversibility{Reversible: false, Notes: "Reading is not changing: this runs VBoxManage showvminfo and alters nothing, so there is nothing an undo could restore."},
			SupportsCheck:   true,
			Doc: collection.Doc{
				Summary:     "Reports a VirtualBox VM's state, hardware and snapshots.",
				Description: "Reads a VM on a VirtualBox host with VBoxManage showvminfo and changes nothing. A VM that does not exist is not a failure: exists is false and nothing else is reported, so a later task can decide what to do. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check is the same read.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
				},
				Returns: []collection.ReturnField{
					{Name: "exists", Type: "bool", Returned: "always", Description: "Whether a VM with this name is registered on the host."},
					{Name: "uuid", Type: "string", Returned: "when exists", Description: "The VM's UUID."},
					{Name: "state", Type: "string", Returned: "when exists", Description: "VirtualBox's state for it: poweroff, running, saved, paused, aborted, or another VirtualBox reports."},
					{Name: "memory_mb", Type: "int", Returned: "when exists", Description: "Its memory, in megabytes."},
					{Name: "cpus", Type: "int", Returned: "when exists", Description: "Its virtual CPU count."},
					{Name: "autostart_enabled", Type: "bool", Returned: "when exists", Description: "Whether it is marked to start with the host account's autostart service. virt.vbox.vm.start sets this on a Windows host and virt.vbox.vm.stop clears it."},
					{Name: "snapshots", Type: "list", Returned: "when exists", Description: "Each snapshot as name, uuid and description, a parent before its children."},
					{Name: "current_snapshot_uuid", Type: "string", Returned: "when exists", Description: "The snapshot the VM's state descends from, or empty when it has none."},
				},
				Examples: []collection.Example{
					{Name: "Read a VM", RunbookYAML: "- name: Read the lab VM\n  virt.vbox.vm.info:\n    name: ubuntu-lab\n  register: vm\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.start", "virt.vbox.snapshot.take"},
			},
		},
		Invoke: Info,
		Check:  Info,
	})
}

// Info implements "virt.vbox.vm.info": it reads the VM and reports it. A
// check is the same read.
func Info(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.info"
	h, name, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	m, exists, err := read(ctx, h, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	stats := map[string]any{statExists: exists}
	if exists {
		snapshots := make([]any, 0, len(m.Snapshots))
		for _, s := range m.Snapshots {
			snapshots = append(snapshots, map[string]any{"name": s.Name, "uuid": s.UUID, "description": s.Description})
		}
		stats[statUUID] = m.UUID
		stats[statState] = m.State
		stats[statMemoryMB] = m.MemoryMB
		stats[statCPUs] = m.CPUs
		stats[statAutostartEnabled] = m.AutostartEnabled
		stats[statSnapshots] = snapshots
		stats[statCurrentSnapshotUUID] = m.CurrentSnapshotUUID
	}
	for _, key := range []string{statExists, statUUID, statState, statMemoryMB, statCPUs, statAutostartEnabled, statSnapshots, statCurrentSnapshotUUID} {
		if value, ok := stats[key]; ok {
			if err := rc.SetStat(key, value); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
	}
	return collection.Result{}, nil
}
