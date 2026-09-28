// The "virt.vbox.vm.import_disk" method: a VM made from a disk image on the host, copied into its folder.
package vm

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.vbox.vm.import_disk",
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
			Reversibility:   collection.Reversibility{Reversible: true, Notes: "A run that made a VM emits virt.vbox.vm.delete naming it, which deletes the copied disk with it; the image copied from is never changed. One that found a VM under the name emits nothing."},
			SupportsCheck:   true,
			Doc: collection.Doc{
				Summary:     "Makes a VirtualBox VM from a disk image on the host, such as Microsoft's Windows Server evaluation VHDX.",
				Description: "Makes sure a VM of this name exists, creating it from a disk image already on the host: a VHDX, VHD, VMDK or VDI file, such as the evaluation VHDX Microsoft publishes for Windows Server. The image is copied into the VM's folder as a VDI that grows as the guest writes to it, and the image itself is left as it was, so one image can make many VMs. The copy is the VM's disk, and virt.vbox.vm.delete removes it with the VM. The VM gets os_type; a SATA controller holding the disk and an IDE controller for the DVD a clone's seed goes in; network cards Windows has a driver for, with nothing attached; and the firmware the disk boots with, read from its first sectors: EFI for a GUID partition table, BIOS for a master boot record. A VM already under the name reports no change and is not compared with the image. The VM is not started, and is meant as a base to snapshot and clone. For Windows the image must be generalized (by sysprep), and virt.vbox.vm.clone then gives each clone its own name, address and password on a DVD. Microsoft's Windows Server evaluation VHDX is generalized but never reads that DVD at its first boot (measured on the lab host), so its clones stop at the first-boot screens; virt.vbox.vm.install makes a Windows base whose clones read theirs. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the host's VMs and sends nothing; it does not read the disk, so it reports the firmware only when firmware names one.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
					{Name: "path", Type: "string", Required: true, Description: "The disk image's absolute path on the host. It may not hold a quote, a wildcard or a control character."},
					{Name: "os_type", Type: "string", Required: true, Description: "The VM's guest OS type, as VBoxManage list ostypes names it: Windows2025_64 for Windows Server 2025, Windows2022_64 for 2022, Ubuntu_64 for Ubuntu. VirtualBox tunes the VM to it, and virt.vbox.vm.clone seeds a clone of a Windows type with a Windows answer file."},
					{Name: "firmware", Type: "string", Default: "auto", Choices: []string{"auto", "bios", "efi"}, Description: "What the VM boots with. auto reads the disk's first sectors: EFI for a GUID partition table, BIOS for a master boot record."},
					{Name: "timeout", Type: "int", Default: "3600", Description: "How many seconds the copy may take. A disk of many gigabytes takes minutes."},
				},
				Returns: []collection.ReturnField{
					{Name: "uuid", Type: "string", Returned: "when not a check that would import", Description: "The VM's UUID."},
					{Name: "firmware", Type: "string", Returned: "when not a check with firmware auto", Description: "What the VM boots with: bios or efi."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "Whether a VM of the name existed before this task and after it."},
				},
				Examples: []collection.Example{
					{Name: "Make a Windows Server base from Microsoft's VHDX", RunbookYAML: "- name: Make a Windows Server 2025 base from the evaluation VHDX\n  virt.vbox.vm.import_disk:\n    name: ws2025-base\n    path: G:\\iso\\server-2025-datacenter-eval.vhdx\n    os_type: Windows2025_64\n\n- name: Snapshot it, never booted\n  virt.vbox.snapshot.take:\n    vm: ws2025-base\n    name: base\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.install", "virt.vbox.snapshot.take", "virt.vbox.vm.clone", "virt.vbox.vm.import_ova"},
			},
		},
		Invoke: ImportDisk,
		Check:  CheckImportDisk,
	})
}

// The import's parameters and stats beyond the shared ones.
const (
	paramOSType   = "os_type"
	paramFirmware = "firmware"
	statFirmware  = "firmware"
	firmwareAuto  = "auto"
)

// baseShape is the memory and CPUs a VM made from nothing is given. A base
// is cloned rather than started, and each clone gets its own size.
var baseShape = windowsShape

// ImportDisk implements "virt.vbox.vm.import_disk".
func ImportDisk(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runImportDisk(ctx, rc, device, params, collection.ModeExecute)
}

// CheckImportDisk is "virt.vbox.vm.import_disk"'s check: it reads the
// host's VMs and sends nothing.
func CheckImportDisk(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runImportDisk(ctx, rc, device, params, collection.ModeCheck)
}

// importRequest is one import task's validated parameters.
type importRequest struct {
	path, osType, firmware string
	timeout                time.Duration
}

// readImport validates an import task's parameters other than name.
func readImport(params map[string]any) (importRequest, error) {
	var r importRequest
	var err error
	if r.path, err = sdk.RequiredStringParam(params, paramPath); err != nil {
		return r, err
	}
	if err := vboxmanage.CheckPath("disk image", r.path); err != nil {
		return r, err
	}
	if r.osType, err = readOSType(params); err != nil {
		return r, err
	}
	if r.firmware = sdk.StringParam(params, paramFirmware); r.firmware == "" {
		r.firmware = firmwareAuto
	}
	if !slices.Contains([]string{firmwareAuto, vboxmanage.FirmwareBIOS, vboxmanage.FirmwareEFI}, r.firmware) {
		return r, fmt.Errorf("firmware %q is not auto, bios or efi", r.firmware)
	}
	r.timeout, err = seconds(params, paramTimeout, 3600)
	return r, err
}

// readOSType reads and checks the os_type parameter.
func readOSType(params map[string]any) (string, error) {
	osType, err := sdk.RequiredStringParam(params, paramOSType)
	if err != nil {
		return "", err
	}
	return osType, vboxmanage.CheckOSType(osType)
}

// runImportDisk makes the VM from the image unless one of the name exists.
func runImportDisk(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.import_disk"
	r, err := readImport(params)
	if err != nil {
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
		if err := recordMade(rc, fqcn, existing.UUID, strings.ToLower(existing.Firmware)); err != nil {
			return collection.Result{}, err
		}
		return collection.Result{}, recordExists(rc, fqcn, true, true)
	}
	if mode == collection.ModeCheck {
		// Reading the disk would register it with VirtualBox, which a
		// check does not do, so auto's answer waits for the run.
		if r.firmware != firmwareAuto {
			if err := rc.SetStat(statFirmware, r.firmware); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
		return collection.Result{Changed: true}, recordExists(rc, fqcn, false, true)
	}
	if r.firmware == firmwareAuto {
		if r.firmware, err = firmwareOf(ctx, h, r.path); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	uuid, err := importDisk(ctx, h, name, vmFolder(device), r)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := recordMade(rc, fqcn, uuid, r.firmware); err != nil {
		return collection.Result{}, err
	}
	if err := recordExists(rc, fqcn, false, true); err != nil {
		return collection.Result{}, err
	}
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:        "virt.vbox.vm.delete",
		Params:      map[string]any{paramName: name},
		Description: fmt.Sprintf("Delete %s and the disk copied for it, which this task made.", name),
	}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}

// recordMade sets an import's uuid and firmware stats.
func recordMade(rc sdk.RunbookContext, fqcn, uuid, firmware string) error {
	if err := rc.SetStat(statUUID, uuid); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statFirmware, firmware); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	return nil
}

// firmwareOf reads which firmware the disk at path boots with: EFI for a
// GUID partition table, BIOS for a master boot record.
func firmwareOf(ctx context.Context, h vboxmanage.Host, path string) (string, error) {
	style, err := h.PartitionStyle(ctx, path)
	if err != nil {
		return "", err
	}
	if style == vboxmanage.PartitionsGPT {
		return vboxmanage.FirmwareEFI, nil
	}
	return vboxmanage.FirmwareBIOS, nil
}

// importDisk registers the VM, copies the image into its folder and
// attaches the copy, returning the VM's UUID. A failure after the VM is
// registered deletes what was made, so a later run starts over rather
// than finding a VM of the name and taking it for a finished one.
func importDisk(ctx context.Context, h vboxmanage.Host, name, folder string, r importRequest) (string, error) {
	made := vboxmanage.NewMachine{Name: name, OSType: r.osType, Folder: folder, Firmware: r.firmware, MemoryMB: baseShape.memoryMB, CPUs: baseShape.cpus}
	if err := h.CreateVM(ctx, made); err != nil {
		return "", undoMake(ctx, h, name, "", err)
	}
	m, err := h.Machine(ctx, name)
	if err != nil {
		return "", undoMake(ctx, h, name, "", err)
	}
	dir, err := vmDir(m)
	if err != nil {
		return "", undoMake(ctx, h, name, "", err)
	}
	disk := dir + `\` + name + ".vdi"
	if err := h.WithTimeout(r.timeout).CopyDisk(ctx, r.path, disk); err != nil {
		return "", undoMake(ctx, h, name, disk, err)
	}
	if err := h.AttachDisk(ctx, name, disk); err != nil {
		return "", undoMake(ctx, h, name, disk, err)
	}
	return m.UUID, nil
}

// undoMake deletes what a failed make left behind: the disk at disk when
// VirtualBox registered it and no machine holds it, then the VM when it
// was registered. It returns cause, saying whether the cleanup worked.
func undoMake(ctx context.Context, h vboxmanage.Host, name, disk string, cause error) error {
	var failures []string
	if _, exists, err := read(ctx, h, name); err != nil {
		failures = append(failures, err.Error())
	} else if exists {
		if err := deleteVM(ctx, h, name); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if disk != "" {
		if known, err := h.Disks(ctx); err != nil {
			failures = append(failures, err.Error())
		} else if slices.ContainsFunc(known, func(p string) bool { return strings.EqualFold(p, disk) }) {
			if err := h.DeleteDisk(ctx, disk); err != nil {
				failures = append(failures, err.Error())
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%w; deleting the half-made %s then failed too: %s", cause, name, strings.Join(failures, "; "))
	}
	return fmt.Errorf("%w; what was made of %s was deleted", cause, name)
}
