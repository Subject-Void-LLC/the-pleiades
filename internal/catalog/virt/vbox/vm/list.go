// The "virt.vbox.vm.list" method: every VM on the host, with what Pleiades knows of each.
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
		Name: "virt.vbox.vm.list",
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
			Reversibility:   collection.Reversibility{Reversible: false, Notes: "Reading is not changing: this lists VMs and alters nothing, so there is nothing an undo could restore."},
			SupportsCheck:   true,
			Doc: collection.Doc{
				Summary:     "Lists the VMs on a VirtualBox host, with their state and the address Pleiades gave them.",
				Description: "Reports every VM registered on the host for the account Pleiades reaches it as: its name, UUID, state, memory, CPUs and autostart mark, and, for a VM virt.vbox.vm.clone made, the host-only address it was given and the inventory device whose login it was seeded with. VirtualBox keeps a separate list of VMs for each Windows account, so these are not the VMs a person sees in their own VirtualBox Manager, and theirs are not listed here. Nothing is changed. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check is the same read.",
				Returns: []collection.ReturnField{
					{Name: "vms", Type: "list", Returned: "always", Description: "Each VM as name, uuid, state, memory_mb, cpus, autostart_enabled, size when its CPUs and memory are one T-shirt size's, and address and device when Pleiades made it, in the order VirtualBox lists them."},
				},
				Examples: []collection.Example{
					{Name: "List the lab's VMs", RunbookYAML: "- name: What runs on the lab host\n  virt.vbox.vm.list: {}\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.info", "virt.vbox.vm.clone"},
			},
		},
		Invoke: List,
		Check:  List,
	})
}

// statVMs is the stat the list is reported under.
const statVMs = "vms"

// List implements "virt.vbox.vm.list". A check is the same read.
func List(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.list"
	h, err := hostFunc(rc, device)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	refs, err := h.List(ctx)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	vms := make([]any, 0, len(refs))
	for _, ref := range refs {
		entry, err := describe(ctx, h, ref)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, ref.Name, err)
		}
		vms = append(vms, entry)
	}
	if err := rc.SetStat(statVMs, vms); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{}, nil
}

// describe is one VM as List reports it. A name VirtualBox lists that this
// package would refuse to send is reported by name and UUID alone, since
// reading it further would mean sending it.
func describe(ctx context.Context, h vboxmanage.Host, ref vboxmanage.Ref) (map[string]any, error) {
	entry := map[string]any{"name": ref.Name, "uuid": ref.UUID}
	if vboxmanage.CheckName("VM", ref.Name) != nil {
		return entry, nil
	}
	m, err := h.Machine(ctx, ref.Name)
	if err != nil {
		return nil, err
	}
	extra, err := h.ExtraData(ctx, ref.Name)
	if err != nil {
		return nil, err
	}
	entry[statState] = m.State
	entry[statMemoryMB] = m.MemoryMB
	entry[statCPUs] = m.CPUs
	if size := shapeOf(m).size(); size != "" {
		entry[statSize] = size
	}
	entry[statAutostartEnabled] = m.AutostartEnabled
	if address := extra[vboxmanage.ExtraAddress]; address != "" {
		entry["address"] = address
	}
	if device := extra[vboxmanage.ExtraDevice]; device != "" {
		entry["device"] = device
	}
	return entry, nil
}
