// The "virt.fusion.vm.info" method: a VM's power state and its guest's address.
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
		Name: "virt.fusion.vm.info",
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
				Reversible: false,
				Notes:      "Reading is not changing: this runs vmcli's power and configuration queries and vmrun's listSnapshots, checkToolsState and getGuestIPAddress and alters nothing, so there is nothing an undo could restore.",
				ReadOnly:   true,
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Reports a VMware Fusion VM's power state and its guest's address.",
				Description: "Reads a VM on a Mac running VMware Fusion and changes nothing: its power state (vmcli's power query), the identifiers and hardware its vmx keys name (vmcli's ConfigParams query, an allowlist, never a key's raw value, since a vmx can carry a secret), its snapshots (vmrun listSnapshots), and when it is on VMware Tools' state and the guest's address (vmrun getGuestIPAddress). A read past the power state that fails is named under warnings and the rest is returned. A VM whose vmx file is not there is not a failure: exists is false and nothing else is reported, so a later task can decide what to do. The task's target is the Mac (a device onboarding found VMware Fusion answering on), not the VM, which is a resource on it, named by the path of its vmx file. A check is the same read.",
				Params: []collection.Param{
					{Name: "vmx", Type: "string", Required: true, Description: "The absolute path of the VM's .vmx file on the Mac, such as /Users/me/pleiades-vms/win11-arm/win11-arm.vmx. Spaces are allowed; a path that is not absolute, does not end in .vmx, or holds a control character is refused."},
				},
				Returns: []collection.ReturnField{
					{Name: "exists", Type: "bool", Returned: "always", Description: "Whether the VM's vmx file is on the Mac."},
					{Name: "state", Type: "string", Returned: "when exists", Description: "Fusion's power state for it: on, off, suspended or paused."},
					{Name: "guest_ip", Type: "string", Returned: "when the VM is on and VMware Tools in its guest answers", Description: "The guest's IP address, as VMware Tools reports it."},
					{Name: "display_name", Type: "string", Returned: "when exists and the vmx sets it", Description: "The name Fusion shows for the VM (displayName)."},
					{Name: "guest_os", Type: "string", Returned: "when exists and the vmx sets it", Description: "The guest type the vmx declares (guestOS), such as arm-windows11-64."},
					{Name: "memory_mb", Type: "int", Returned: "when exists and the vmx sets it", Description: "Its memory in megabytes (memsize)."},
					{Name: "cpus", Type: "int", Returned: "when exists and the vmx sets it", Description: "Its virtual CPU count (numvcpus); a vmx that leaves it out runs one."},
					{Name: "firmware", Type: "string", Returned: "when exists and the vmx sets it", Description: "What it boots with: efi or bios."},
					{Name: "hardware_version", Type: "int", Returned: "when exists and the vmx sets it", Description: "Its virtual hardware version (virtualHW.version)."},
					{Name: "nics", Type: "list of dict", Returned: "when exists and the configuration was read", Description: "Each Ethernet adapter the vmx names and does not mark absent, up to 10: number, and connection_type, virtual_dev, address_type and mac when the vmx sets them."},
					{Name: "disks", Type: "list of dict", Returned: "when exists and the configuration was read", Description: "Each disk or drive file the vmx names, up to 32, sorted by slot: slot (nvme0:0), file, and device_type when set."},
					{Name: "snapshots", Type: "list of string", Returned: "when exists and the snapshots were read", Description: "The VM's snapshot names, oldest first."},
					{Name: "tools_state", Type: "string", Returned: "when the VM is on", Description: "VMware Tools in the guest as vmrun names it: running, installed or unknown."},
					{Name: "warnings", Type: "list of string", Returned: "when a read beyond the power state failed", Description: "Which read failed (the configuration, the snapshots or VMware Tools' state) and the tool's words; the rest is still returned."},
				},
				Examples: []collection.Example{
					{Name: "Read a VM", RunbookYAML: "- name: Read the Windows VM\n  virt.fusion.vm.info:\n    vmx: /Users/me/pleiades-vms/win11-arm/win11-arm.vmx\n  register: vm\n"},
				},
				SeeAlso: []string{"virt.fusion.vm.start", "virt.fusion.vm.stop"},
			},
		},
		Invoke: Info,
		Check:  Info,
	})
}

// Info implements "virt.fusion.vm.info": it reads the VM and reports it. A
// check is the same read.
func Info(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "virt.fusion.vm.info"
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	h, vmx, done, err := target(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer done()
	state, exists, err := read(ctx, h, vmx)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statExists, exists); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !exists {
		return collection.Result{}, nil
	}
	if err := rc.SetStat(statState, state); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	// The rest of the VM, from its vmx keys, its snapshots and VMware
	// Tools, so one call describes it whole (Phase 131's R5).
	more, warnings := detail(ctx, h, vmx, state)
	for _, key := range detailKeys {
		if value, ok := more[key]; ok {
			if err := rc.SetStat(key, value); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
	}
	if len(warnings) > 0 {
		if err := sdk.RecordWarnings(rc, warnings); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	if state != stateOn {
		return collection.Result{}, nil
	}
	ip, err := guestIP(ctx, h, vmx)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if ip != "" {
		if err := rc.SetStat(statGuestIP, ip); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	return collection.Result{}, nil
}
