// The "virt.vbox.vm.info" method: a VM's state, hardware and snapshots.
package vm

import (
	"context"
	"fmt"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
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
				Reversible: false,
				Notes:      "Reading is not changing: this runs VBoxManage showvminfo and alters nothing, so there is nothing an undo could restore.",
				ReadOnly:   true,
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Reports a VirtualBox VM's state, hardware and snapshots.",
				Description: "Reads a VM on a VirtualBox host with VBoxManage showvminfo and changes nothing: its state, shape, snapshots, guest type, firmware, network adapters and storage, from one call. A VM that does not exist is not a failure: exists is false and nothing else is reported, so a later task can decide what to do. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check is the same read.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
				},
				Returns: []collection.ReturnField{
					{Name: "exists", Type: "bool", Returned: "always", Description: "Whether a VM with this name is registered on the host."},
					{Name: "uuid", Type: "string", Returned: "when exists", Description: "The VM's UUID."},
					{Name: "state", Type: "string", Returned: "when exists", Description: "VirtualBox's state for it: poweroff, running, saved, paused, aborted, or another VirtualBox reports."},
					{Name: "memory_mb", Type: "int", Returned: "when exists", Description: "Its memory, in megabytes."},
					{Name: "cpus", Type: "int", Returned: "when exists", Description: "Its virtual CPU count."},
					{Name: "size", Type: "string", Returned: "when exists", Description: "Its T-shirt size, read from its CPUs and memory: xsmall, small, medium, large or xlarge, or empty when they are not exactly one size's."},
					{Name: "autostart_enabled", Type: "bool", Returned: "when exists", Description: "Whether it is marked to start with the host account's autostart service. virt.vbox.vm.start sets this on a Windows host and virt.vbox.vm.stop clears it."},
					{Name: "snapshots", Type: "list", Returned: "when exists", Description: "Each snapshot as name, uuid and description, a parent before its children."},
					{Name: "current_snapshot_uuid", Type: "string", Returned: "when exists", Description: "The snapshot the VM's state descends from, or empty when it has none."},
					{Name: "os_type", Type: "string", Returned: "when exists", Description: "The guest OS type as showvminfo describes it, such as Ubuntu (64-bit)."},
					{Name: "firmware", Type: "string", Returned: "when exists", Description: "What it boots with: BIOS or EFI."},
					{Name: "config_file", Type: "string", Returned: "when exists and VirtualBox names it", Description: "The path of its settings file on the host."},
					{Name: "nics", Type: "list", Returned: "when exists", Description: "Each network adapter that is on, by number from 1: number, kind (nat, hostonly, bridged, ...), mac, and host_only_adapter for a hostonly one."},
					{Name: "storage", Type: "list", Returned: "when exists", Description: "Each storage slot as controller, controller_type, port, device and medium (a file's path, emptydrive, or none)."},
					{Name: "console_log", Type: "string", Returned: "when exists and its first serial port writes to a file", Description: "The host file its first serial port writes to."},
					{Name: "state_changed_at", Type: "string", Returned: "when exists and VirtualBox reports it", Description: "When it last changed state, as VirtualBox writes the time."},
					{Name: "guest_additions_version", Type: "string", Returned: "when exists and a running guest's additions report one", Description: "The VirtualBox Guest Additions version the guest reports."},
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
		stats[statSize] = shapeOf(m).size()
		stats[statAutostartEnabled] = m.AutostartEnabled
		stats[statSnapshots] = snapshots
		stats[statCurrentSnapshotUUID] = m.CurrentSnapshotUUID
		// The rest of the machine showvminfo already answered, so one call
		// describes it whole (Phase 131's R5).
		for key, value := range hardware(m) {
			stats[key] = value
		}
	}
	for _, key := range infoKeys {
		if value, ok := stats[key]; ok {
			if err := rc.SetStat(key, value); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
	}
	return collection.Result{}, nil
}

// The stats info reports beyond the shape and snapshots (firmware is
// import_disk.go's statFirmware, the same name).
const (
	statOSType          = "os_type"
	statConfigFile      = "config_file"
	statNICs            = "nics"
	statStorage         = "storage"
	statConsoleLog      = "console_log"
	statStateChangedAt  = "state_changed_at"
	statGuestAdditions  = "guest_additions_version"
	valueStateChange    = "VMStateChangeTime"
	valueGuestAdditions = "GuestAdditionsVersion"
)

// infoKeys is every stat info can report, in the order it records them.
var infoKeys = []string{
	statExists, statUUID, statState, statMemoryMB, statCPUs, statSize, statAutostartEnabled, statSnapshots, statCurrentSnapshotUUID,
	statOSType, statFirmware, statConfigFile, statNICs, statStorage, statConsoleLog, statStateChangedAt, statGuestAdditions,
}

// hardware is what showvminfo says of m beyond its shape: its guest type,
// firmware, settings file, network adapters and storage, and the optional
// values it reports only sometimes (a serial log, a state change time, the
// guest additions a running guest reports), each left out when empty.
func hardware(m vboxmanage.Machine) map[string]any {
	out := map[string]any{statOSType: m.OSType, statFirmware: m.Firmware}
	numbers := make([]int, 0, len(m.NICs))
	for n := range m.NICs {
		numbers = append(numbers, n)
	}
	slices.Sort(numbers)
	nics := make([]any, 0, len(numbers))
	for _, n := range numbers {
		nic := m.NICs[n]
		// showvminfo lists every adapter slot the chipset has; one that
		// is off says nothing about the machine.
		if nic.Kind == "none" || nic.Kind == "" {
			continue
		}
		entry := map[string]any{"number": n, "kind": nic.Kind, "mac": nic.MAC}
		if nic.HostOnlyAdapter != "" {
			entry["host_only_adapter"] = nic.HostOnlyAdapter
		}
		nics = append(nics, entry)
	}
	out[statNICs] = nics
	storage := make([]any, 0, len(m.Slots))
	for _, s := range m.Slots {
		storage = append(storage, map[string]any{"controller": s.Controller, "controller_type": s.ControllerType, "port": s.Port, "device": s.Device, "medium": s.Medium})
	}
	out[statStorage] = storage
	optional := map[string]string{statConfigFile: m.ConfigFile, statConsoleLog: m.ConsoleLog}
	if v, ok := m.Values.Get(valueStateChange); ok {
		optional[statStateChangedAt] = v
	}
	if v, ok := m.Values.Get(valueGuestAdditions); ok {
		optional[statGuestAdditions] = v
	}
	for k, v := range optional {
		if v != "" {
			out[k] = v
		}
	}
	return out
}
