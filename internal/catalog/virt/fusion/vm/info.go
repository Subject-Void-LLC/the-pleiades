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
				Notes:      "Reading is not changing: this runs vmcli's power query and vmrun's getGuestIPAddress and alters nothing, so there is nothing an undo could restore.",
				ReadOnly:   true,
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Reports a VMware Fusion VM's power state and its guest's address.",
				Description: "Reads a VM on a Mac running VMware Fusion, through vmcli's power query and vmrun's getGuestIPAddress, and changes nothing. A VM whose vmx file is not there is not a failure: exists is false and nothing else is reported, so a later task can decide what to do. The task's target is the Mac (a device onboarding found VMware Fusion answering on), not the VM, which is a resource on it, named by the path of its vmx file. A check is the same read.",
				Params: []collection.Param{
					{Name: "vmx", Type: "string", Required: true, Description: "The absolute path of the VM's .vmx file on the Mac, such as /Users/me/pleiades-vms/win11-arm/win11-arm.vmx. Spaces are allowed; a path that is not absolute, does not end in .vmx, or holds a control character is refused."},
				},
				Returns: []collection.ReturnField{
					{Name: "exists", Type: "bool", Returned: "always", Description: "Whether the VM's vmx file is on the Mac."},
					{Name: "state", Type: "string", Returned: "when exists", Description: "Fusion's power state for it: on, off, suspended or paused."},
					{Name: "guest_ip", Type: "string", Returned: "when the VM is on and VMware Tools in its guest answers", Description: "The guest's IP address, as VMware Tools reports it."},
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
