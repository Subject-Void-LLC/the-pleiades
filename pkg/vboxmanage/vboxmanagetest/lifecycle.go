// The model's machine lifecycle and hardware: importing an appliance,
// linked cloning, changing settings, attaching media and unregistering, and
// writing that hardware the way showvminfo does. A refusal's wording is the
// model's own unless it says it was captured.
package vboxmanagetest

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// flags reads a VBoxManage command's --flag value pairs, taking the
// flags that name two values (--uart1 0x3F8 4, --uart-mode1 file PATH)
// as one value joined by a space.
func flags(args []string) (map[string]string, error) {
	two := map[string]bool{"--uart1": true, "--uart-mode1": true}
	out := map[string]string{}
	for i := 0; i < len(args); i++ {
		name := args[i]
		if !strings.HasPrefix(name, "--") {
			return nil, fmt.Errorf("unexpected %q", name)
		}
		if name == "--register" {
			out[name] = ""
			continue
		}
		n := 1
		if two[name] {
			n = 2
		}
		if i+n > len(args)-1 {
			return nil, fmt.Errorf("%s needs %d values", name, n)
		}
		out[name] = strings.Join(args[i+1:i+1+n], " ")
		i += n
	}
	return out, nil
}

// newMAC returns the next MAC address the model hands out, in VirtualBox's
// own prefix.
func (h *Host) newMAC() string {
	h.macs++
	return fmt.Sprintf("080027%06X", h.macs)
}

// importOVA answers import, from the appliance the model holds at path.
func (h *Host) importOVA(path string, args []string) vboxmanage.Output {
	template, ok := h.Appliances[path]
	if !ok {
		return errorOutput(fmt.Sprintf("Cannot read the appliance '%s'", path))
	}
	f, err := flags(args)
	if err != nil {
		return errorOutput(err.Error())
	}
	name := f["--vmname"]
	if h.find(name) != nil {
		return errorOutput(fmt.Sprintf("Machine settings file '%s' already exists", name))
	}
	folder := f["--basefolder"]
	if folder == "" {
		folder = `C:\Users\pleiades-gate\VirtualBox VMs`
	}
	vm := *template
	vm.Name, vm.UUID, vm.Folder, vm.State = name, h.newUUID(), folder+`\`+name, vboxmanage.StatePoweroff
	vm.Snapshots, vm.Current = nil, ""
	vm.NICs = map[int]vboxmanage.NIC{}
	for n, nic := range template.NICs {
		nic.MAC = h.newMAC()
		vm.NICs[n] = nic
	}
	vm.Slots = append([]vboxmanage.Slot(nil), template.Slots...)
	h.vms = append(h.vms, &vm)
	return vboxmanage.Output{Stdout: "Successfully imported the appliance.\r\n", Stderr: progress}
}

// clonevm answers a linked clone of a snapshot.
func (h *Host) clonevm(from string, args []string) vboxmanage.Output {
	source := h.find(from)
	if source == nil {
		return notFound(from)
	}
	f, err := flags(args)
	if err != nil {
		return errorOutput(err.Error())
	}
	if f["--options"] != "link" {
		return errorOutput("the model makes linked clones only")
	}
	if source.snapshot(f["--snapshot"]) == nil {
		return errorOutput(fmt.Sprintf("Could not find a snapshot with UUID {%s}", f["--snapshot"]))
	}
	name := f["--name"]
	if h.find(name) != nil {
		return errorOutput(fmt.Sprintf("Machine settings file '%s' already exists", name))
	}
	folder := f["--basefolder"]
	if folder == "" {
		folder = `C:\Users\pleiades-gate\VirtualBox VMs`
	}
	vm := &VM{Name: name, UUID: h.newUUID(), State: vboxmanage.StatePoweroff, MemoryMB: source.MemoryMB, CPUs: source.CPUs,
		Folder: folder + `\` + name, LinkedFrom: source.Name, NICs: map[int]vboxmanage.NIC{}}
	for n, nic := range source.NICs {
		nic.MAC = h.newMAC()
		vm.NICs[n] = nic
	}
	for _, slot := range source.Slots {
		if strings.HasSuffix(strings.ToLower(slot.Medium), ".vmdk") {
			slot.Medium = vm.Folder + `\Snapshots/{` + h.newUUID() + `}.vmdk`
		}
		vm.Slots = append(vm.Slots, slot)
	}
	h.vms = append(h.vms, vm)
	return vboxmanage.Output{Stdout: fmt.Sprintf("Machine has been successfully cloned as %q\r\n", name), Stderr: progress}
}

// modifyvm answers the settings changes pkg/vboxmanage makes.
func (h *Host) modifyvm(name string, args []string) vboxmanage.Output {
	vm := h.find(name)
	if vm == nil {
		return notFound(name)
	}
	if !vm.off() {
		return locked(vm.Name)
	}
	f, err := flags(args)
	if err != nil {
		return errorOutput(err.Error())
	}
	_, memory := f["--memory"]
	_, cpus := f["--cpus"]
	if vm.State == vboxmanage.StateSaved && (memory || cpus) {
		return savedState(vm.Name)
	}
	if vm.NICs == nil {
		vm.NICs = map[int]vboxmanage.NIC{}
	}
	for key, value := range f {
		switch {
		case key == "--memory":
			vm.MemoryMB, _ = strconv.Atoi(value)
		case key == "--cpus":
			vm.CPUs, _ = strconv.Atoi(value)
		case key == "--autostart-enabled":
			vm.Autostart = value == "on"
		case key == "--autostart-delay", key == "--uart1", key == "--paravirt-provider":
		case key == "--uart-mode1":
			vm.ConsoleLog = strings.TrimPrefix(value, "file ")
		case strings.HasPrefix(key, "--nic"):
			n, _ := strconv.Atoi(strings.TrimPrefix(key, "--nic"))
			nic := vm.NICs[n]
			nic.Kind = value
			if value != "none" && nic.MAC == "" {
				nic.MAC = h.newMAC()
			}
			vm.NICs[n] = nic
		case strings.HasPrefix(key, "--hostonlyadapter"):
			n, _ := strconv.Atoi(strings.TrimPrefix(key, "--hostonlyadapter"))
			nic := vm.NICs[n]
			nic.HostOnlyAdapter = value
			vm.NICs[n] = nic
		default:
			return errorOutput("the model does not answer modifyvm " + key)
		}
	}
	return vboxmanage.Output{}
}

// savedState is the answer for a hardware change VirtualBox refuses while a
// machine's saved state holds its memory. It is the model's wording, not a
// captured one.
func savedState(name string) vboxmanage.Output {
	return errorOutput(
		fmt.Sprintf("The machine '%s' is in the Saved state; its memory and processors cannot be changed", name),
		"Details: code VBOX_E_INVALID_VM_STATE (0x80bb0002), component MachineWrap, interface IMachine")
}

// storageattach answers putting a DVD image in a slot, emptying one, or
// removing its drive.
func (h *Host) storageattach(name string, args []string) vboxmanage.Output {
	vm := h.find(name)
	if vm == nil {
		return notFound(name)
	}
	f, err := flags(args)
	if err != nil {
		return errorOutput(err.Error())
	}
	medium := f["--medium"]
	if medium != "none" && medium != "emptydrive" {
		if _, ok := h.Files[medium]; !ok {
			return errorOutput(fmt.Sprintf("Could not find file for the medium '%s' (VERR_FILE_NOT_FOUND)", medium))
		}
	}
	for i, slot := range vm.Slots {
		if slot.Controller == f["--storagectl"] && strconv.Itoa(slot.Port) == f["--port"] && strconv.Itoa(slot.Device) == f["--device"] {
			vm.Slots[i].Medium = medium
			return vboxmanage.Output{}
		}
	}
	return errorOutput(fmt.Sprintf("No storage device attached to device slot %s on port %s of controller '%s'", f["--device"], f["--port"], f["--storagectl"]))
}

// closemedium answers forgetting a DVD image, which VirtualBox refuses
// while a machine holds it.
func (h *Host) closemedium(path string, args []string) vboxmanage.Output {
	for _, vm := range h.vms {
		for _, slot := range vm.Slots {
			if slot.Medium == path {
				return errorOutput(fmt.Sprintf("Cannot close medium '%s' because it is attached to the machine '%s'", path, vm.Name))
			}
		}
	}
	if len(args) == 1 && args[0] == "--delete" {
		delete(h.Files, path)
	}
	return vboxmanage.Output{}
}

// unregistervm answers forgetting a machine and deleting its disks, which
// VirtualBox refuses while it runs or while a linked clone descends from
// it. Its DVD images and console log stay, as VirtualBox leaves them.
func (h *Host) unregistervm(name string) vboxmanage.Output {
	vm := h.find(name)
	if vm == nil {
		return notFound(name)
	}
	if !vm.off() {
		return locked(vm.Name)
	}
	for _, other := range h.vms {
		if other.LinkedFrom == vm.Name {
			return errorOutput(fmt.Sprintf("Cannot delete the disks of '%s': the machine '%s' is linked to them", vm.Name, other.Name))
		}
	}
	kept := h.vms[:0]
	for _, other := range h.vms {
		if other != vm {
			kept = append(kept, other)
		}
	}
	h.vms = kept
	return vboxmanage.Output{Stderr: progress}
}

// writeHardware writes vm's storage, network adapters and serial ports in
// the order and form a captured showvminfo holds them.
func (vm *VM) writeHardware(line func(key, value string)) {
	var controllers []vboxmanage.Slot
	seen := map[string]bool{}
	for _, s := range vm.Slots {
		if !seen[s.Controller] {
			seen[s.Controller] = true
			controllers = append(controllers, s)
		}
	}
	for i, c := range controllers {
		line(fmt.Sprintf("storagecontrollername%d", i), quote(c.Controller))
		line(fmt.Sprintf("storagecontrollertype%d", i), quote(c.ControllerType))
	}
	for _, s := range vm.Slots {
		line(quote(fmt.Sprintf("%s-%d-%d", s.Controller, s.Port, s.Device)), quote(s.Medium))
	}
	for n := 1; n <= 8; n++ {
		nic, ok := vm.NICs[n]
		if !ok || nic.Kind == "" || nic.Kind == "none" {
			line(fmt.Sprintf("nic%d", n), quote("none"))
			continue
		}
		line(fmt.Sprintf("macaddress%d", n), quote(nic.MAC))
		line(fmt.Sprintf("nic%d", n), quote(nic.Kind))
		if nic.Kind == "hostonly" {
			line(fmt.Sprintf("hostonlyadapter%d", n), quote(nic.HostOnlyAdapter))
		}
	}
	if vm.ConsoleLog != "" {
		line("uart1", quote("0x03f8,4"))
		// VBoxManage writes this value's backslashes as they are, unlike
		// CfgFile's (captured in showvminfo-clone.stdout).
		line("uartmode1", `"file,`+vm.ConsoleLog+`"`)
	} else {
		line("uart1", quote("off"))
	}
	for n := 2; n <= 4; n++ {
		line(fmt.Sprintf("uart%d", n), quote("off"))
	}
}

// getextradata answers enumerating a machine's extradata, one "Key: K,
// Value: V" line each as the captured answer holds them, sorted as
// VirtualBox sorts them.
func (h *Host) getextradata(name string) vboxmanage.Output {
	vm := h.find(name)
	if vm == nil {
		return notFound(name)
	}
	keys := make([]string, 0, len(vm.Extra))
	for k := range vm.Extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "Key: %s, Value: %s\r\n", k, vm.Extra[k])
	}
	return vboxmanage.Output{Stdout: b.String()}
}

// setextradata answers setting a machine's key, or deleting it when no
// value is given.
func (h *Host) setextradata(name, key string, value []string) vboxmanage.Output {
	vm := h.find(name)
	if vm == nil {
		return notFound(name)
	}
	if vm.Extra == nil {
		vm.Extra = map[string]string{}
	}
	if len(value) == 0 {
		delete(vm.Extra, key)
		return vboxmanage.Output{}
	}
	vm.Extra[key] = value[0]
	return vboxmanage.Output{}
}
