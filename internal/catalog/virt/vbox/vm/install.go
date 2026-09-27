// The "virt.vbox.vm.install" method: Windows installed from its ISO, unattended, and generalized as a base.
package vm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winunattend"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.vbox.vm.install",
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
			Reversibility:   collection.Reversibility{Reversible: true, Notes: "A run that installed a VM emits virt.vbox.vm.delete naming it, which deletes its disk with it; the ISO is never changed. One that found an installed VM under the name emits nothing."},
			SupportsCheck:   true,
			Doc: collection.Doc{
				Summary:     "Makes a Windows VM by installing Windows from its installation ISO, unattended, and generalizing it as a base to clone.",
				Description: "Makes sure a VM of this name exists, creating it by installing Windows from an installation ISO already on the host, with no one at the keyboard. The VM gets a new disk of disk_gb, BIOS firmware, the size asked for, os_type, and no network adapter, so setup downloads nothing. Its DVD drives hold the ISO and an answer file Pleiades builds (Autounattend.xml), which partitions the disk, installs image and accepts its license, then takes the new Windows through audit mode, where it deletes the copy of itself Windows cached, points the registry (HKLM\\SYSTEM\\Setup, UnattendFile) at D:\\Autounattend.xml, and runs sysprep to generalize the installation and shut it down. A generalized Windows looks for its answer file at first boot only there and in its own folders, never on a DVD, so the pointer is how a clone reads its seed. The task waits for the shutdown: about six to eight minutes on the lab host. When the VM is off, both DVDs are taken out, the answer file is deleted, and the VM is marked installed (the extradata pleiades/installed). The answer file holds a password only for audit mode's sign-in, made at random for this install and kept nowhere, which Windows forgets when it is generalized. The VM is meant as a base to snapshot and clone: virt.vbox.vm.clone gives each clone its own name, address and password. A VM already under the name reports no change when it is marked installed, and is refused when it is not, since an install that did not finish is no base: delete it with virt.vbox.vm.delete and run again. An install still running at the timeout is left running, with a picture of its screen saved in its folder to show where it stopped. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the host's VMs and memory, and sends nothing.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
					{Name: "iso", Type: "string", Required: true, Description: "The installation ISO's absolute path on the host, such as Microsoft's Windows Server evaluation ISO. It is attached and then taken out again, never changed or deleted."},
					{Name: "image", Type: "string", Required: true, Description: "The edition to install: its name in the ISO's install.wim, such as Windows Server 2025 Standard Evaluation, which is Server Core, or Windows Server 2025 Datacenter Evaluation (Desktop Experience); or its index there, 1 for the first."},
					{Name: "os_type", Type: "string", Required: true, Description: "The VM's guest OS type, as VBoxManage list ostypes names it: Windows2025_64 for Windows Server 2025, Windows2022_64 for 2022, Ubuntu_64 for Ubuntu. VirtualBox tunes the VM to it, and virt.vbox.vm.clone seeds a clone of a Windows type with a Windows answer file."},
					{Name: "disk_gb", Type: "int", Default: "64", Description: "The size of the VM's new disk, in gigabytes. It grows on the host as Windows writes to it."},
					{Name: "size", Type: "string", Default: "medium", Choices: []string{"xsmall", "small", "medium", "large", "xlarge"}, Description: "The VM's size while it installs, a T-shirt size, which sets its CPUs and memory together as a cloud's instance type does: xsmall is 1 CPU and 1024 MB, small 1 CPU and 2048 MB, medium 2 CPUs and 4096 MB, large 4 CPUs and 8192 MB, and xlarge 8 CPUs and 16384 MB. Windows Server needs at least small."},
					{Name: "language", Type: "string", Default: "en-US", Description: "The installation's language and locale, as a tag such as en-US. The ISO must hold it."},
					{Name: "timeout", Type: "int", Default: "7200", Description: "How many seconds to wait for the install to finish and the VM to shut itself down."},
				},
				Returns: []collection.ReturnField{
					{Name: "uuid", Type: "string", Returned: "when not a check that would install", Description: "The VM's UUID."},
					{Name: "size", Type: "string", Returned: "always", Description: "The VM's T-shirt size, or empty when its CPUs and memory are not exactly one size's."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "Whether a VM of the name existed before this task and after it."},
				},
				Examples: []collection.Example{
					{Name: "Install Windows Server Core as a base", RunbookYAML: "- name: Install Windows Server 2025 Core from the evaluation ISO\n  virt.vbox.vm.install:\n    name: ws2025-core-base\n    iso: G:\\iso\\server-2025-eval.iso\n    image: Windows Server 2025 Standard Evaluation\n    os_type: Windows2025_64\n\n- name: Snapshot the generalized install\n  virt.vbox.snapshot.take:\n    vm: ws2025-core-base\n    name: base\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.import_disk", "virt.vbox.snapshot.take", "virt.vbox.vm.clone", "virt.vbox.vm.delete"},
			},
		},
		Invoke: Install,
		Check:  CheckInstall,
	})
}

// The install's parameters, and the files it keeps in the VM's folder.
const (
	paramISO      = "iso"
	paramImage    = "image"
	paramDiskGB   = "disk_gb"
	paramLanguage = "language"

	answerFile     = "answer.iso"
	screenshotFile = "install-timeout.png"
)

// installShape is the size an install runs at when the task asks for
// none: medium.
var installShape = shape{memoryMB: 4096, cpus: 2}

// installPoll is how often an install looks at the VM while it waits; a
// test shortens it.
var installPoll = 15 * time.Second

// minDiskGB is the smallest disk Windows Server installs on.
const minDiskGB = 32

// Install implements "virt.vbox.vm.install".
func Install(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runInstall(ctx, rc, device, params, collection.ModeExecute)
}

// CheckInstall is "virt.vbox.vm.install"'s check: it reads the host's VMs
// and memory, and sends nothing.
func CheckInstall(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runInstall(ctx, rc, device, params, collection.ModeCheck)
}

// installRequest is one install task's validated parameters.
type installRequest struct {
	iso, osType string
	diskGB      int
	shape       shape
	answer      winunattend.Install
	timeout     time.Duration
}

// readInstall validates an install task's parameters other than name, and
// makes the answer file's audit password.
func readInstall(params map[string]any) (installRequest, error) {
	var r installRequest
	var err error
	if r.iso, err = sdk.RequiredStringParam(params, paramISO); err != nil {
		return r, err
	}
	if err := vboxmanage.CheckPath("installation ISO", r.iso); err != nil {
		return r, err
	}
	if r.osType, err = readOSType(params); err != nil {
		return r, err
	}
	gb, set, err := sdk.IntParam(params, paramDiskGB)
	if err != nil {
		return r, err
	}
	if r.diskGB = 64; set {
		r.diskGB = gb
	}
	if r.diskGB < minDiskGB || r.diskGB > 65536 {
		return r, fmt.Errorf("disk_gb %d is not %d to 65536: Windows Server needs at least %d GB", r.diskGB, minDiskGB, minDiskGB)
	}
	if r.shape, _, err = readShape(params, installShape); err != nil {
		return r, err
	}
	if r.timeout, err = seconds(params, paramTimeout, 7200); err != nil {
		return r, err
	}
	image, err := sdk.RequiredStringParam(params, paramImage)
	if err != nil {
		return r, err
	}
	r.answer = winunattend.Install{Image: image, Language: sdk.StringParam(params, paramLanguage)}
	if r.answer.AuditPassword, err = winunattend.NewPassword(); err != nil {
		return r, err
	}
	return r, r.answer.Validate()
}

// runInstall installs the VM unless an installed one of the name exists.
func runInstall(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.install"
	r, err := readInstall(params)
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
		return resumeInstall(ctx, rc, h, fqcn, existing, r, mode)
	}
	if err := fitHost(ctx, h, r.shape); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := fitsNow(ctx, h, vboxmanage.Machine{Name: name, MemoryMB: r.shape.memoryMB}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statSize, r.shape.size()); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, recordExists(rc, fqcn, false, true)
	}
	uuid, err := install(ctx, h, name, vmFolder(device), r)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statUUID, uuid); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := recordExists(rc, fqcn, false, true); err != nil {
		return collection.Result{}, err
	}
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:        "virt.vbox.vm.delete",
		Params:      map[string]any{paramName: name},
		Description: fmt.Sprintf("Delete %s and its disk, which this task installed.", name),
	}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}

// resumeInstall answers for a VM already under the name. One marked
// installed is reported with no change. One this method started that is
// not finished (its answer file is still in a drive) is waited for again
// and finished, so a run that stopped waiting (a timeout, a failed read)
// is continued by running the task again. Any other VM is refused.
func resumeInstall(ctx context.Context, rc sdk.RunbookContext, h vboxmanage.Host, fqcn string, m vboxmanage.Machine, r installRequest, mode collection.Mode) (collection.Result, error) {
	extra, err := h.ExtraData(ctx, m.Name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if extra[vboxmanage.ExtraInstalled] != "" {
		return collection.Result{}, recordInstalled(rc, fqcn, m)
	}
	dir, err := vmDir(m)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if len(holding(m, dir+`\`+answerFile)) == 0 {
		return collection.Result{}, fmt.Errorf("%s: %q exists but was not installed by this method, or its install was taken apart; delete it with virt.vbox.vm.delete and run this again", fqcn, m.Name)
	}
	if err := rc.SetStat(statSize, shapeOf(m).size()); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, recordExists(rc, fqcn, true, true)
	}
	if err := waitForInstall(ctx, h, m.Name, dir, r.timeout); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := finishInstall(ctx, h, m.Name, dir, r.iso); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statUUID, m.UUID); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, recordExists(rc, fqcn, true, true)
}

// recordInstalled reports a VM marked installed.
func recordInstalled(rc sdk.RunbookContext, fqcn string, m vboxmanage.Machine) error {
	if err := rc.SetStat(statUUID, m.UUID); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statSize, shapeOf(m).size()); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	return recordExists(rc, fqcn, true, true)
}

// install makes the VM, starts its install, waits for it to shut itself
// down generalized, and takes its DVDs out. A failure before the VM starts
// deletes what was made; one after leaves the VM for its owner to see.
func install(ctx context.Context, h vboxmanage.Host, name, folder string, r installRequest) (string, error) {
	image, err := r.answer.ISO(now().UTC())
	if err != nil {
		return "", err
	}
	m, err := prepareInstall(ctx, h, name, folder, r, image)
	if err != nil {
		return "", undoMake(ctx, h, name, "", err)
	}
	if _, err := (vboxmanage.WindowsAutostart{Host: h}).Start(ctx, name); err != nil {
		return "", undoMake(ctx, h, name, "", err)
	}
	dir, _ := vmDir(m)
	if err := waitForInstall(ctx, h, name, dir, r.timeout); err != nil {
		return "", err
	}
	if err := finishInstall(ctx, h, name, dir, r.iso); err != nil {
		return "", err
	}
	return m.UUID, nil
}

// prepareInstall registers the VM, gives it a new disk, and puts the ISO
// and the answer file in its DVD drives.
func prepareInstall(ctx context.Context, h vboxmanage.Host, name, folder string, r installRequest, answer []byte) (vboxmanage.Machine, error) {
	made := vboxmanage.NewMachine{Name: name, OSType: r.osType, Folder: folder, Firmware: vboxmanage.FirmwareBIOS, MemoryMB: r.shape.memoryMB, CPUs: r.shape.cpus}
	if err := h.CreateVM(ctx, made); err != nil {
		return vboxmanage.Machine{}, err
	}
	m, err := h.Machine(ctx, name)
	if err != nil {
		return m, err
	}
	dir, err := vmDir(m)
	if err != nil {
		return m, err
	}
	disk := dir + `\` + name + ".vdi"
	if err := h.CreateDisk(ctx, disk, r.diskGB*1024); err != nil {
		return m, err
	}
	if err := h.AttachDisk(ctx, name, disk); err != nil {
		return m, errors.Join(err, h.DeleteDisk(ctx, disk))
	}
	if err := h.Upload(ctx, dir+`\`+answerFile, answer); err != nil {
		return m, err
	}
	for _, medium := range []string{r.iso, dir + `\` + answerFile} {
		if m, err = h.Machine(ctx, name); err != nil {
			return m, err
		}
		slot, ok := m.FreeIDESlot()
		if !ok {
			return m, fmt.Errorf("%s has no free IDE slot for %s", name, medium)
		}
		if err := h.AttachDVD(ctx, name, slot, medium); err != nil {
			return m, err
		}
	}
	return m, nil
}

// waitForInstall waits for the VM to shut itself down, which the answer
// file's last step does once Windows is generalized. A VM still running
// at the timeout is left running, with a picture of its screen saved in
// its folder.
func waitForInstall(ctx context.Context, h vboxmanage.Host, name, dir string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		m, err := h.Machine(ctx, name)
		// Another client's lock (a screenshot, a log read) is a moment,
		// not an answer: look again.
		if errors.Is(err, vboxmanage.ErrLocked) {
			m.State = vboxmanage.StateRunning
		} else if err != nil {
			return err
		}
		switch m.State {
		case vboxmanage.StatePoweroff:
			return nil
		case vboxmanage.StateAborted:
			return fmt.Errorf("%s stopped abnormally (aborted) during its install; its VBox.log in %s says why", name, dir)
		}
		if time.Now().After(deadline) {
			picture := dir + `\` + screenshotFile
			if err := h.Screenshot(ctx, name, picture); err != nil {
				return fmt.Errorf("%s's install had not finished within %s, and its screen could not be saved: %w", name, timeout, err)
			}
			return fmt.Errorf("%s's install had not finished within %s; it is left %s, and a picture of its screen is at %s", name, timeout, m.State, picture)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(installPoll):
		}
	}
}

// finishInstall clears the autostart mark the start left, takes the ISO
// and the answer file out of the drives, deletes the answer file, and
// marks the VM installed.
func finishInstall(ctx context.Context, h vboxmanage.Host, name, dir, iso string) error {
	if err := clearAutostart(ctx, h, name); err != nil {
		return err
	}
	m, err := h.Machine(ctx, name)
	if err != nil {
		return err
	}
	answer := dir + `\` + answerFile
	for _, slot := range m.Slots {
		if strings.EqualFold(slot.Medium, iso) || strings.EqualFold(slot.Medium, answer) {
			if err := h.Detach(ctx, name, slot); err != nil {
				return err
			}
		}
	}
	if err := h.CloseDVD(ctx, answer, true); err != nil {
		return err
	}
	return h.SetExtraData(ctx, name, vboxmanage.ExtraInstalled, now().UTC().Format(time.RFC3339))
}
