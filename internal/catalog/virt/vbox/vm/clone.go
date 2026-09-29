// The "virt.vbox.vm.clone" method: a linked clone, seeded with a device's login by cloud-init or a Windows answer file.
package vm

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vmsize"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.vbox.vm.clone",
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
				Notes:      "A run that made a VM emits virt.vbox.vm.delete naming it and pinning its UUID, so a VM made later under the name is refused rather than deleted; one that found a VM under the name emits nothing.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "virt.vbox.vm.delete", Record: []string{"name", "uuid"}},
				},
			},
			SupportsCheck: true,
			SeedsLogin:    paramLogin,
			// A Windows VM admits the Administrator's password, not a key.
			SeedsLoginPassword: true,
			Doc: collection.Doc{
				Summary:     "Makes a VM as a linked clone of another's snapshot, seeded with a device's login by cloud-init or a Windows answer file.",
				Description: "Makes sure a VM of this name exists, creating it as a linked clone of a snapshot of another VM, so it takes little space and starts from that snapshot's disk. A VM already under the name reports no change and is not reconfigured or reseeded; when its CPUs or memory are not those asked for, the task says so in a warning, and virt.vbox.vm.resize changes them. The new VM gets the size asked for, or the memory and CPUs, and one larger than the host is refused: more CPUs than it has processors online, or more memory than it has. It also gets a NAT adapter for the internet, a host-only adapter at a fixed address, a serial console written to console.log in its folder (virt.vbox.vm.host_keys reads a Linux VM's SSH host keys from there), and a seed on its DVD drive: a cloud-init NoCloud seed, or for a VM of a Windows OS type a Windows answer file (Autounattend.xml). The seed is built by Pleiades, not on the host, and reaches the host on the command's standard input, never on a command line. For a Linux VM it carries login's user name, the public half of its key and a salted hash of its password, taken from the vault by pleiades add-credential login --generate; never the key or the password. SSH then admits the key only; the password is for the VM's console. A login of root may log in by key; any other user gets passwordless sudo. A Windows VM is reached over WinRM by the built-in Administrator's password, and an answer file can hold nothing else, so for one the login must be a device reached over WinRM whose credential names Administrator, and the answer file carries that password itself, beside the computer name, the fixed address and the first-boot screens it skips. Windows reads that answer file at first boot only from a base virt.vbox.vm.install made, which points it at the clone's one DVD (D:); a clone of another generalized image, such as Microsoft's evaluation VHDX, stops at its first-boot screens. A Windows VM given no size is small rather than xsmall. The seed stays in the VM's folder until virt.vbox.vm.eject_seed or virt.vbox.vm.delete removes it: eject it once the first boot has read it. The address and login are recorded on the VM as VirtualBox extradata (pleiades/address, pleiades/device), which virt.vbox.vm.list reports, since VirtualBox cannot know a guest's address without its Guest Additions. The VM is not started. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the host's VMs and the snapshot, and sends nothing.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
					{Name: "from", Type: "string", Required: true, Description: "The VM to clone, named as name is."},
					{Name: "snapshot", Type: "string", Required: true, Description: "The snapshot of from to clone. Exactly one of from's snapshots may have this name."},
					{Name: "login", Type: "string", Required: true, Description: "The inventory device whose stored login the VM is seeded with, usually the device that stands for this VM. For a Linux VM, one reached over SSH: its user name, its key's public half and a hash of its password. For a Windows VM, one reached over WinRM whose credential names Administrator: its password."},
					{Name: "address", Type: "string", Required: true, Description: "The host-only adapter's address with its prefix, as 192.168.56.10/24. Keep it out of the host-only DHCP server's range."},
					{Name: "hostname", Type: "string", Description: "The VM's host name, or a Windows VM's computer name, which is at most 15 letters, digits and hyphens. Defaults to name, which must then be a valid one."},
					{Name: "size", Type: "string", Choices: vmsize.Names(), Description: "The VM's size, a T-shirt size, which sets its CPUs and memory together as a cloud's instance type does: " + vmsize.Describe() + ". Give size, or memory_mb and cpus, not both; with none of the three, the VM is xsmall, or small for a Windows VM."},
					{Name: "memory_mb", Type: "int", Default: "1024", Description: "Its memory, in megabytes, when size is not given."},
					{Name: "cpus", Type: "int", Default: "1", Description: "Its virtual CPU count, when size is not given."},
					{Name: "paravirt_provider", Type: "string", Choices: vboxmanage.ParavirtProviders, Description: "The paravirtualization interface the guest is offered, as VBoxManage modifyvm --paravirt-provider takes it: default picks one by the guest's OS type (kvm for Linux, hyperv for Windows), and none offers nothing, so the guest keeps time by its own clocks. Left out, the VM keeps from's."},
					{Name: "host_only_adapter", Type: "string", Default: "VirtualBox Host-Only Ethernet Adapter", Description: "The host's host-only adapter, as VBoxManage list hostonlyifs names it."},
				},
				Returns: []collection.ReturnField{
					{Name: "uuid", Type: "string", Returned: "when not a check that would clone", Description: "The VM's UUID, which is also a Linux VM's cloud-init instance ID."},
					{Name: "address", Type: "string", Returned: "always", Description: "The host-only address the VM was given, without its prefix."},
					{Name: "size", Type: "string", Returned: "always", Description: "The VM's T-shirt size, read from its CPUs and memory (those asked for, in a check that would clone), or empty when they are not exactly one size's."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "Whether a VM of the name existed before this task and after it."},
				},
				Examples: []collection.Example{
					{Name: "Clone and start a lab VM", RunbookYAML: "- name: Make the lab VM from the base's clean snapshot\n  virt.vbox.vm.clone:\n    name: ubuntu-lab\n    from: ubuntu-2404-base\n    snapshot: base\n    login: ubuntu-lab\n    address: 192.168.56.10/24\n    size: small\n\n- name: Start it\n  virt.vbox.vm.start:\n    name: ubuntu-lab\n"},
					{Name: "Clone a Windows VM", RunbookYAML: "- name: Make a Windows lab VM, seeded with win-lab's Administrator password\n  virt.vbox.vm.clone:\n    name: win-lab\n    from: ws2025-base\n    snapshot: base\n    login: win-lab\n    address: 192.168.56.30/24\n    size: medium\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.import_ova", "virt.vbox.vm.import_disk", "virt.vbox.vm.install", "virt.vbox.vm.start", "virt.vbox.vm.host_keys", "virt.vbox.vm.eject_seed", "virt.vbox.vm.resize", "virt.vbox.vm.delete", "virt.vbox.vm.list"},
			},
		},
		Invoke: Clone,
		Check:  CheckClone,
	})
}

// The clone's parameters beyond the shared ones.
const (
	paramFrom            = "from"
	paramSnapshot        = "snapshot"
	paramLogin           = "login"
	paramAddress         = "address"
	paramHostname        = "hostname"
	paramHostOnlyAdapter = "host_only_adapter"
	paramParavirt        = "paravirt_provider"

	statAddress = "address"
)

// defaultHostOnlyAdapter is the adapter VirtualBox makes when installed
// on Windows.
const defaultHostOnlyAdapter = "VirtualBox Host-Only Ethernet Adapter"

// The files a clone keeps in its VM's folder.
const (
	seedFile    = "seed.iso"
	consoleFile = "console.log"
)

// now dates a seed image; a test fixes it.
var now = time.Now

// cloneRequest is one clone task's validated parameters.
type cloneRequest struct {
	from, snapshot, hostname, adapter, login string
	address                                  netip.Prefix
	shape                                    shape
	paravirt                                 string
	// sized says the task asked for a size, memory or CPUs, rather than
	// taking xsmall's by default.
	sized bool
}

// defaultShape is a clone's shape when the task asks for none: xsmall.
var defaultShape = shape{memoryMB: 1024, cpus: 1}

// windowsShape is a Windows clone's shape when the task asks for none:
// small, since Windows Server needs more than 1024 MB.
var windowsShape = shape{memoryMB: 2048, cpus: 1}

// readClone validates a clone task's parameters other than name.
func readClone(params map[string]any, name string) (cloneRequest, error) {
	var c cloneRequest
	var err error
	if c.from, err = sdk.RequiredStringParam(params, paramFrom); err != nil {
		return c, err
	}
	if err := vboxmanage.CheckName("VM", c.from); err != nil {
		return c, err
	}
	if c.snapshot, err = sdk.RequiredStringParam(params, paramSnapshot); err != nil {
		return c, err
	}
	if err := vboxmanage.CheckName("snapshot", c.snapshot); err != nil {
		return c, err
	}
	if c.login, err = sdk.RequiredStringParam(params, paramLogin); err != nil {
		return c, err
	}
	address, err := sdk.RequiredStringParam(params, paramAddress)
	if err != nil {
		return c, err
	}
	if c.address, err = netip.ParsePrefix(address); err != nil || !c.address.Addr().Is4() || c.address.Bits() < 8 || c.address.Bits() > 30 {
		return c, fmt.Errorf("address %q is not an IPv4 address with its prefix, as 192.168.56.10/24", address)
	}
	if c.hostname = sdk.StringParam(params, paramHostname); c.hostname == "" {
		c.hostname = name
	}
	if c.adapter = sdk.StringParam(params, paramHostOnlyAdapter); c.adapter == "" {
		c.adapter = defaultHostOnlyAdapter
	}
	if err := vboxmanage.CheckAdapter(c.adapter); err != nil {
		return c, err
	}
	if raw, present := params[paramParavirt]; present && raw != nil {
		c.paravirt, _ = raw.(string)
		if !slices.Contains(vboxmanage.ParavirtProviders, c.paravirt) {
			return c, fmt.Errorf("paravirt_provider %v is not one of %s", raw, strings.Join(vboxmanage.ParavirtProviders, ", "))
		}
	}
	c.shape, c.sized, err = readShape(params, defaultShape)
	return c, err
}

// Clone implements "virt.vbox.vm.clone".
func Clone(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runClone(ctx, rc, device, params, collection.ModeExecute)
}

// CheckClone is "virt.vbox.vm.clone"'s check: it reads the host's VMs and
// the snapshot, and sends nothing.
func CheckClone(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runClone(ctx, rc, device, params, collection.ModeCheck)
}

// runClone makes the VM unless one of the name exists.
func runClone(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.clone"
	h, name, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	c, err := readClone(params, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	login, err := readSeedLogin(rc.InjectSecrets())
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := login.check(c.hostname, c.address); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statAddress, c.address.Addr().String()); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	existing, exists, err := read(ctx, h, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if exists {
		if err := recordFound(rc, fqcn, existing, c); err != nil {
			return collection.Result{}, err
		}
		return collection.Result{}, recordExists(rc, fqcn, true, true)
	}
	source, err := mustReadFor(ctx, h, c.from, device, fqcn, mode)
	if err != nil {
		return collection.Result{}, err
	}
	if err := login.matches(source, c.login); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if source.Windows() && !c.sized {
		c.shape = windowsShape
	}
	if err := fitHost(ctx, h, c.shape); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statSize, c.shape.size()); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	named := source.SnapshotsNamed(c.snapshot)
	switch {
	case len(named) == 0 && mode == collection.ModeCheck:
		return collection.Result{}, collection.CannotCheck(fmt.Sprintf("%q has no snapshot named %q yet; a real run fails on it unless an earlier task in the run takes it", c.from, c.snapshot))
	case len(named) != 1:
		return collection.Result{}, fmt.Errorf("%s: %q has %d snapshots named %q; clone needs exactly one", fqcn, c.from, len(named), c.snapshot)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, recordExists(rc, fqcn, false, true)
	}
	made, err := makeClone(ctx, h, name, named[0].UUID, vmFolder(device), c, login)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statUUID, made.UUID); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := recordExists(rc, fqcn, false, true); err != nil {
		return collection.Result{}, err
	}
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN: "virt.vbox.vm.delete",
		// The UUID pins the undo to the VM this task made: a VM made later
		// under the same name is refused rather than deleted.
		Params:      map[string]any{paramName: name, paramUUID: made.UUID},
		Description: fmt.Sprintf("Delete %s, which this task made.", name),
	}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}

// recordFound reports a VM already under the name: its UUID and size,
// and a warning when the task asked for CPUs or memory it does not have,
// since the task changes neither and a run that reads "ok" would
// otherwise hide it.
func recordFound(rc sdk.RunbookContext, fqcn string, m vboxmanage.Machine, c cloneRequest) error {
	has := shapeOf(m)
	if err := rc.SetStat(statUUID, m.UUID); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statSize, has.size()); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	if !c.sized || has == c.shape {
		return nil
	}
	warning := fmt.Sprintf("%q already exists with %s, not the %s asked for; clone does not change a VM it finds, and virt.vbox.vm.resize does while it is off", m.Name, has, c.shape)
	if err := sdk.RecordWarnings(rc, []string{warning}); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	return nil
}

// makeClone clones, configures and seeds the VM. A failure after the
// clone deletes what was made, so a later run starts over rather than
// finding a VM of the name and taking it for a finished one.
func makeClone(ctx context.Context, h vboxmanage.Host, name, snapshotUUID, folder string, c cloneRequest, login seedLogin) (vboxmanage.Machine, error) {
	if err := h.CloneLinked(ctx, c.from, snapshotUUID, name, folder); err != nil {
		return vboxmanage.Machine{}, err
	}
	m, err := seedClone(ctx, h, name, c, login)
	if err != nil {
		if cleanup := deleteVM(ctx, h, name); cleanup != nil {
			return vboxmanage.Machine{}, fmt.Errorf("%w; deleting the half-made %s then failed too: %v", err, name, cleanup)
		}
		return vboxmanage.Machine{}, fmt.Errorf("%w; the half-made %s was deleted", err, name)
	}
	return m, nil
}

// seedClone gives a freshly cloned VM its hardware and its seed.
func seedClone(ctx context.Context, h vboxmanage.Host, name string, c cloneRequest, login seedLogin) (vboxmanage.Machine, error) {
	m, err := h.Machine(ctx, name)
	if err != nil {
		return m, err
	}
	dir, err := vmDir(m)
	if err != nil {
		return m, err
	}
	hw := vboxmanage.Hardware{MemoryMB: c.shape.memoryMB, CPUs: c.shape.cpus, HostOnlyAdapter: c.adapter, ConsoleLog: dir + `\` + consoleFile, Paravirt: c.paravirt}
	if err := h.Configure(ctx, name, hw); err != nil {
		return m, err
	}
	for key, value := range map[string]string{vboxmanage.ExtraAddress: c.address.Addr().String(), vboxmanage.ExtraDevice: c.login} {
		if err := h.SetExtraData(ctx, name, key, value); err != nil {
			return m, err
		}
	}
	if m, err = h.Machine(ctx, name); err != nil {
		return m, err
	}
	image, err := login.image(m, c, now().UTC())
	if err != nil {
		return m, err
	}
	slot, ok := login.slot(m)
	if !ok {
		return m, fmt.Errorf("%s has no free slot for its seed: %s", name, login.slotNeed())
	}
	iso := dir + `\` + seedFile
	if err := h.Upload(ctx, iso, image); err != nil {
		return m, err
	}
	if err := h.AttachDVD(ctx, name, slot, iso); err != nil {
		return m, err
	}
	return h.Machine(ctx, name)
}

// vmDir is the folder holding m's settings file.
func vmDir(m vboxmanage.Machine) (string, error) {
	i := strings.LastIndex(m.ConfigFile, `\`)
	if i <= 2 {
		return "", fmt.Errorf("%s's settings file %q is in no folder a path can name", m.Name, m.ConfigFile)
	}
	dir := m.ConfigFile[:i]
	if err := vboxmanage.CheckPath("VM folder", dir); err != nil {
		return "", err
	}
	return dir, nil
}
