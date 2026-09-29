// The "virt.vbox.vm.import_ova" method: a VM imported from an OVA file, with no network.
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
		Name: "virt.vbox.vm.import_ova",
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
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "A run that imported a VM emits virt.vbox.vm.delete naming it and pinning its UUID, so a VM made later under the name is refused rather than deleted; one that found a VM under the name emits nothing.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "virt.vbox.vm.delete", Record: []string{"name", "uuid"}},
				},
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Imports an OVA appliance on a VirtualBox host as a VM, with no network adapter.",
				Description: "Makes sure a VM of this name exists, importing it from an OVA file already on the host (win.file.download fetches one) into the host's vm_folder. A VM already under the name reports no change, and is not compared with the file. The imported VM is left with no network adapter, whatever the appliance asked for: Ubuntu's cloud image asks for a bridged one, which would put the VM on the host's own network. A VM made from it (virt.vbox.vm.clone) is given the networks it is meant to have. The VM is not started, and is meant as a base to snapshot and clone rather than to boot. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the host's VMs and sends nothing.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
					{Name: "path", Type: "string", Required: true, Description: "The OVA file's absolute path on the host. It may not hold a quote, a wildcard or a control character."},
				},
				Returns: []collection.ReturnField{
					{Name: "uuid", Type: "string", Returned: "when not a check that would import", Description: "The VM's UUID."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "Whether a VM of the name existed before this task and after it."},
				},
				Examples: []collection.Example{
					{Name: "Import the Ubuntu cloud image", RunbookYAML: "- name: Import the Ubuntu 24.04 cloud image as a base\n  virt.vbox.vm.import_ova:\n    name: ubuntu-2404-base\n    path: G:\\PleiadesLab\\ubuntu-24.04-server-cloudimg-amd64.ova\n"},
				},
				SeeAlso: []string{"win.file.download", "virt.vbox.snapshot.take", "virt.vbox.vm.clone"},
			},
		},
		Invoke: ImportOva,
		Check:  CheckImportOva,
	})
}

// ImportOva implements "virt.vbox.vm.import_ova".
func ImportOva(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runImportOva(ctx, rc, device, params, collection.ModeExecute)
}

// CheckImportOva is "virt.vbox.vm.import_ova"'s check: it reads the
// host's VMs and sends nothing.
func CheckImportOva(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runImportOva(ctx, rc, device, params, collection.ModeCheck)
}

// runImportOva imports the OVA as the VM unless one of the name exists.
func runImportOva(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.import_ova"
	path, err := sdk.RequiredStringParam(params, paramPath)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := vboxmanage.CheckPath("appliance", path); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	h, name, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	existing, exists, err := read(ctx, h, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if exists {
		if err := rc.SetStat(statUUID, existing.UUID); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{}, recordExists(rc, fqcn, true, true)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, recordExists(rc, fqcn, false, true)
	}
	if err := h.Import(ctx, path, name, vmFolder(device)); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	imported, err := mustRead(ctx, h, name, device, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	if err := rc.SetStat(statUUID, imported.UUID); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := recordExists(rc, fqcn, false, true); err != nil {
		return collection.Result{}, err
	}
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN: "virt.vbox.vm.delete",
		// The UUID pins the undo to the VM this task made: a VM made later
		// under the same name is refused rather than deleted.
		Params:      map[string]any{paramName: name, paramUUID: imported.UUID},
		Description: fmt.Sprintf("Delete %s, which this task imported.", name),
	}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}
