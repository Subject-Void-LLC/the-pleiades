// The "virt.vbox.vm.attach_disk" method: a second disk for a stopped VM,
// made new on the host when it is not there yet.
package vm

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// The disk methods' parameter and stat names (statPath and statDeleted are
// shared with virt.vbox.vm.log and eject_seed).
const (
	paramSizeGB  = "size_gb"
	paramDelete  = "delete"
	statPort     = "port"
	statCreated  = "created"
	statDetached = "detached"
	// diffAttached is the key a disk's diff records.
	diffAttached = "attached"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.vbox.vm.attach_disk",
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
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "A run that attached the disk emits virt.vbox.vm.detach_disk, deleting the disk from the host only when this task made it; one that found it attached emits nothing.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "virt.vbox.vm.detach_disk", Record: []string{"name", "path", "delete"}, MayKeepOnFailure: true},
				},
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Gives a stopped VirtualBox VM a second disk, made new on the host when it is not there yet.",
				Description: "Makes sure a VM has the disk at path attached, as a hard disk in its first free SATA port (or, with none free, the port holding an empty DVD drive, which a Windows clone keeps once its seed is ejected; the drive is removed for the disk): one already attached reports no change; one the host has registered but not attached is attached; otherwise a new, empty VDI of size_gb is made at path (the VM's own folder and <name>-data.vdi when path is not given), growing on the host as the guest writes to it, and attached. The guest sees a blank disk it must partition and format itself; nothing here touches the guest. VirtualBox attaches a disk only while a VM is powered off, so a running, paused or saved VM is refused: stop it first with virt.vbox.vm.stop. A run that attached the disk emits virt.vbox.vm.detach_disk, deleting the disk only when this task made it. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and the host's disks, and sends nothing.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
					{Name: "path", Type: "string", Description: "The disk's path on the host, an absolute drive path such as G:\\PleiadesLab\\bsd-check\\bsd-check-data.vdi; when not given, the VM's own folder and <name>-data.vdi."},
					{Name: "size_gb", Type: "int", Description: "How big a disk to make, in gigabytes, when the host has none at path; a disk already there keeps its size. Required unless the disk exists."},
				},
				Returns: []collection.ReturnField{
					{Name: "path", Type: "string", Returned: "always", Description: "The disk's path on the host."},
					{Name: "port", Type: "int", Returned: "always", Description: "The SATA port the disk is attached at (predicted, in a check)."},
					{Name: "created", Type: "bool", Returned: "always", Description: "Whether this task made the disk (predicted, in a check)."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "Whether the disk was attached before this task and after it."},
					{Name: "warnings", Type: "list", Returned: "when the VM could not be read back after the disk was attached", Description: "What the task could not confirm: the disk is attached, and the port is the one it was put in rather than the one read back."},
				},
				Examples: []collection.Example{
					{Name: "Give a lab VM a data disk", RunbookYAML: "- name: Shut the check VM down\n  virt.vbox.vm.stop:\n    name: bsd-check\n\n- name: Give it a 40 GB data disk\n  virt.vbox.vm.attach_disk:\n    name: bsd-check\n    size_gb: 40\n\n- name: Start it again\n  virt.vbox.vm.start:\n    name: bsd-check\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.detach_disk", "virt.vbox.vm.stop", "virt.vbox.vm.info"},
			},
		},
		Invoke: AttachDisk,
		Check:  CheckAttachDisk,
	})
}

// AttachDisk implements "virt.vbox.vm.attach_disk".
func AttachDisk(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runAttachDisk(ctx, rc, device, params, collection.ModeExecute)
}

// CheckAttachDisk is "virt.vbox.vm.attach_disk"'s check: it reads the VM
// and the host's disks, and sends nothing.
func CheckAttachDisk(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runAttachDisk(ctx, rc, device, params, collection.ModeCheck)
}

// runAttachDisk attaches the disk, making it first when the host has none.
func runAttachDisk(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.attach_disk"
	h, name, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	sizeGB, sizeSet, err := sdk.IntParam(params, paramSizeGB)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if sizeSet && sizeGB < 1 {
		return collection.Result{}, fmt.Errorf("%s: size_gb must be at least 1", fqcn)
	}
	m, err := mustReadFor(ctx, h, name, device, fqcn, mode)
	if err != nil {
		return collection.Result{}, err
	}
	path := sdk.StringParam(params, paramPath)
	if path == "" {
		if path = dataDiskPath(m, name); path == "" {
			return collection.Result{}, fmt.Errorf("%s: %q's folder on the host is not known, so give path", fqcn, name)
		}
	}
	if err := vboxmanage.CheckPath("disk", path); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if slot, ok := slotHolding(m, path); ok {
		return collection.Result{}, recordAttach(rc, fqcn, path, slot.Port, false, true, true)
	}
	if err := mustBeOff(m, name, fqcn, mode, "attaches a disk to"); err != nil {
		return collection.Result{}, err
	}
	registered, err := h.HasDisk(ctx, path)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	create := !registered
	if create && !sizeSet {
		return collection.Result{}, fmt.Errorf("%s: the host has no disk at %s, so size_gb says how big a one to make", fqcn, path)
	}
	slot, emptyDrive, ok := sataPortForDisk(m)
	if !ok {
		return collection.Result{}, fmt.Errorf("%s: %q has no free SATA port for another disk", fqcn, name)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, recordAttach(rc, fqcn, path, slot.Port, create, false, true)
	}
	if create {
		if err := h.CreateDisk(ctx, path, sizeGB*1024); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	// From here a disk this task made is on the host: a step that fails
	// before it is attached deletes it again, so a failed task leaves no disk
	// nothing journals (the shape of FAILURE_PATTERNS 638).
	unmade := func(stepErr error) error {
		if !create {
			return stepErr
		}
		if err := h.DeleteDisk(ctx, path); err != nil {
			return fmt.Errorf("%w; the disk this task made at %s could not be deleted again: %v", stepErr, path, err)
		}
		return fmt.Errorf("%w; the disk this task made at %s was deleted again", stepErr, path)
	}
	if emptyDrive {
		if err := h.Detach(ctx, name, slot); err != nil {
			return collection.Result{}, unmade(fmt.Errorf("%s: removing the empty DVD drive from SATA port %d: %w", fqcn, slot.Port, err))
		}
	}
	if err := h.AttachDiskAt(ctx, name, slot, path); err != nil {
		return collection.Result{}, unmade(fmt.Errorf("%s: %w", fqcn, err))
	}
	// The disk is on the VM: its undo stands from here whatever follows, so a
	// read-back that fails still leaves the detach in the journal.
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:          "virt.vbox.vm.detach_disk",
		Params:        map[string]any{paramName: name, paramPath: path, paramDelete: create},
		Description:   detachDescription(name, path, create),
		KeepOnFailure: true,
	}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	port := slot.Port
	var warnings []string
	if m, err = mustRead(ctx, h, name, device, fqcn); err != nil {
		warnings = append(warnings, fmt.Sprintf("%s is attached to %q at SATA port %d, and the VM could not be read back afterwards: %v", path, name, slot.Port, err))
	} else {
		held, ok := slotHolding(m, path)
		if !ok {
			return collection.Result{}, fmt.Errorf("%s: %s was attached to %q and does not read back in any of its slots", fqcn, path, name)
		}
		port = held.Port
	}
	if err := recordAttach(rc, fqcn, path, port, create, false, true); err != nil {
		return collection.Result{}, err
	}
	if err := sdk.RecordWarnings(rc, warnings); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}

// detachDescription says what the undo of an attach does.
func detachDescription(name, path string, created bool) string {
	if created {
		return fmt.Sprintf("Take %s off %s and delete it from the host, since this task made it.", path, name)
	}
	return fmt.Sprintf("Take %s off %s, keeping the disk on the host as it was before this task.", path, name)
}

// recordAttach sets an attach's stats and its diff.
func recordAttach(rc sdk.RunbookContext, fqcn, path string, port int, created, before, after bool) error {
	for key, value := range map[string]any{statPath: path, statPort: port, statCreated: created} {
		if err := rc.SetStat(key, value); err != nil {
			return fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: map[string]any{diffAttached: before}, After: map[string]any{diffAttached: after}}); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	return nil
}

// dataDiskPath is the disk a VM gets when no path is given: <name>-data.vdi
// in the VM's own folder, read from where its settings file is; "" when the
// host did not say where that is.
func dataDiskPath(m vboxmanage.Machine, name string) string {
	cfg := strings.ReplaceAll(m.ConfigFile, "/", `\`)
	i := strings.LastIndex(cfg, `\`)
	if i <= 0 {
		return ""
	}
	return cfg[:i] + `\` + name + "-data.vdi"
}

// sataPortForDisk is the SATA port a second disk goes in: the first with no
// drive (Machine.FreeSATASlot), else the first holding an empty DVD drive
// (emptyDrive true), which a Windows clone keeps on its SATA controller
// once virt.vbox.vm.eject_seed has emptied it and which holds nothing a
// disk cannot replace; false when there is neither.
func sataPortForDisk(m vboxmanage.Machine) (slot vboxmanage.Slot, emptyDrive, ok bool) {
	if s, ok := m.FreeSATASlot(); ok {
		return s, false, true
	}
	for _, s := range m.Slots {
		if s.ControllerType == "IntelAhci" && s.Medium == "emptydrive" {
			return s, true, true
		}
	}
	return vboxmanage.Slot{}, false, false
}

// slotHolding is the slot of m holding the medium at path, compared
// without regard to case or to which separator VirtualBox wrote.
func slotHolding(m vboxmanage.Machine, path string) (vboxmanage.Slot, bool) {
	want := strings.ToLower(strings.ReplaceAll(path, "/", `\`))
	for _, s := range m.Slots {
		if strings.ToLower(strings.ReplaceAll(s.Medium, "/", `\`)) == want {
			return s, true
		}
	}
	return vboxmanage.Slot{}, false
}

// mustBeOff refuses a VM that is not powered off (or aborted, which runs
// nothing): in a check, a running or paused one is unchecked rather than
// failed, since an earlier task in the run may stop it; a saved one is
// refused either way, since VirtualBox refuses a storage change while a
// saved state holds the machine. verb says what the method does to the VM.
func mustBeOff(m vboxmanage.Machine, name, fqcn string, mode collection.Mode, verb string) error {
	if m.State == vboxmanage.StatePoweroff || m.State == vboxmanage.StateAborted {
		return nil
	}
	if mode == collection.ModeCheck && (m.State == vboxmanage.StateRunning || m.State == vboxmanage.StatePaused) {
		return collection.CannotCheck(fmt.Sprintf("%q is %s now; a real run fails on it unless an earlier task stops it", name, m.State))
	}
	return fmt.Errorf("%s: %q is %s; VirtualBox %s a VM only while it is powered off, so stop it first with virt.vbox.vm.stop", fqcn, name, m.State, verb)
}
