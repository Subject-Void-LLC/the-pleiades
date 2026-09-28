// What a machine's hardware looks like to showvminfo (its network
// adapters, storage slots and serial console) and the verbs that change it.
package vboxmanage

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// NIC is one network adapter, as showvminfo reports it.
type NIC struct {
	// Kind is VirtualBox's attachment: nat, hostonly, bridged, or none.
	Kind string
	// MAC is its hardware address as VirtualBox writes it, 12 hex digits.
	MAC string
	// HostOnlyAdapter is the host's adapter a hostonly NIC is attached to.
	HostOnlyAdapter string
}

// Slot is one storage slot and what is in it.
type Slot struct {
	Controller string
	// ControllerType is VirtualBox's name for the controller's chip,
	// such as PIIX4 for IDE.
	ControllerType string
	Port, Device   int
	// Medium is a file's path, "emptydrive" for a drive with nothing in
	// it, or "none" for a slot with no drive.
	Medium string
}

// slotKey is how showvminfo names a slot: "<controller>-<port>-<device>".
var slotKey = regexp.MustCompile(`^(.+)-([0-9]+)-([0-9]+)$`)

// indexedKey is a key ending in its index: storagecontrollername0, nic1.
var indexedKey = regexp.MustCompile(`^([a-z]+?)([0-9]+)$`)

// hardwareFrom reads m's NICs, storage slots and console log out of its
// showvminfo values.
func hardwareFrom(m *Machine) {
	m.NICs = map[int]NIC{}
	controllers := map[string]string{}
	names := map[int]string{}
	types := map[int]string{}
	for _, p := range m.Values {
		k := indexedKey.FindStringSubmatch(p.Key)
		if k == nil {
			continue
		}
		n, _ := strconv.Atoi(k[2])
		switch k[1] {
		case "nic":
			nic := m.NICs[n]
			nic.Kind = p.Value
			m.NICs[n] = nic
		case "macaddress":
			nic := m.NICs[n]
			nic.MAC = p.Value
			m.NICs[n] = nic
		case "hostonlyadapter":
			nic := m.NICs[n]
			nic.HostOnlyAdapter = p.Value
			m.NICs[n] = nic
		case "storagecontrollername":
			names[n] = p.Value
		case "storagecontrollertype":
			types[n] = p.Value
		case "uartmode":
			if path, ok := strings.CutPrefix(p.Value, "file,"); ok && n == 1 {
				m.ConsoleLog = path
			}
		}
	}
	for n, name := range names {
		controllers[name] = types[n]
	}
	for _, p := range m.Values {
		s := slotKey.FindStringSubmatch(p.Key)
		if s == nil {
			continue
		}
		kind, known := controllers[s[1]]
		if !known {
			continue
		}
		port, _ := strconv.Atoi(s[2])
		device, _ := strconv.Atoi(s[3])
		m.Slots = append(m.Slots, Slot{Controller: s[1], ControllerType: kind, Port: port, Device: device, Medium: p.Value})
	}
	sort.SliceStable(m.Slots, func(i, j int) bool {
		a, b := m.Slots[i], m.Slots[j]
		if a.Controller != b.Controller {
			return a.Controller < b.Controller
		}
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		return a.Device < b.Device
	})
}

// ideTypes are the controller chips VirtualBox emulates as IDE.
var ideTypes = map[string]bool{"PIIX3": true, "PIIX4": true, "ICH6": true}

// FreeIDESlot returns the first IDE slot with no drive in it, where a
// DVD can be attached, and false when there is none.
func (m Machine) FreeIDESlot() (Slot, bool) {
	for _, s := range m.Slots {
		if ideTypes[s.ControllerType] && s.Medium == "none" {
			return s, true
		}
	}
	return Slot{}, false
}

// FreeSATASlot returns the first SATA slot with no drive in it, and false
// when there is none. A Windows guest sees a DVD there from its first
// instant, since it boots from the same controller.
func (m Machine) FreeSATASlot() (Slot, bool) {
	for _, s := range m.Slots {
		if s.ControllerType == "IntelAhci" && s.Medium == "none" {
			return s, true
		}
	}
	return Slot{}, false
}

// SlotsHolding returns the slots holding a medium at a path under folder,
// compared without regard to case or to which separator VirtualBox wrote.
func (m Machine) SlotsHolding(folder string) []Slot {
	prefix := strings.ToLower(strings.ReplaceAll(folder, "/", `\`)) + `\`
	var held []Slot
	for _, s := range m.Slots {
		medium := strings.ToLower(strings.ReplaceAll(s.Medium, "/", `\`))
		if strings.HasPrefix(medium, prefix) {
			held = append(held, s)
		}
	}
	return held
}

// Hardware is what Configure sets on a machine.
type Hardware struct {
	MemoryMB, CPUs int
	// HostOnlyAdapter is the host's adapter NIC 2 is attached to; NIC 1
	// is NAT.
	HostOnlyAdapter string
	// ConsoleLog is the host file the first serial port is written to.
	ConsoleLog string
	// Paravirt is the paravirtualization interface the guest is offered,
	// one of ParavirtProviders; "" leaves the machine's as it is.
	Paravirt string
}

// ParavirtProviders are the paravirtualization interfaces VirtualBox
// offers a guest: default picks one by the guest's OS type (kvm for
// Linux), none offers nothing, so the guest keeps time by its own clock.
var ParavirtProviders = []string{"default", "legacy", "minimal", "hyperv", "kvm", "none"}

// adapterPattern is a host network adapter's name, such as "VirtualBox
// Host-Only Ethernet Adapter #2".
var adapterPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 #()._-]{0,127}$`)

// CheckAdapter refuses a host adapter name outside adapterPattern.
func CheckAdapter(name string) error {
	if !adapterPattern.MatchString(name) {
		return fmt.Errorf("vboxmanage: host adapter %q is not letters, digits, spaces and # ( ) . _ -", name)
	}
	return nil
}

// Configure sets vm's memory, CPUs, a NAT adapter, a host-only adapter
// and a serial console written to a file, and leaves it off autostart.
func (h Host) Configure(ctx context.Context, vm string, hw Hardware) error {
	if err := CheckName("VM", vm); err != nil {
		return err
	}
	if hw.MemoryMB < 4 || hw.CPUs < 1 {
		return fmt.Errorf("vboxmanage: %d MB and %d CPUs is not a machine", hw.MemoryMB, hw.CPUs)
	}
	if err := CheckAdapter(hw.HostOnlyAdapter); err != nil {
		return err
	}
	if err := CheckPath("console log", hw.ConsoleLog); err != nil {
		return err
	}
	args := []string{"modifyvm", vm,
		"--memory", strconv.Itoa(hw.MemoryMB), "--cpus", strconv.Itoa(hw.CPUs),
		"--nic1", "nat", "--nic2", "hostonly", "--hostonlyadapter2", hw.HostOnlyAdapter,
		"--uart1", "0x3F8", "4", "--uart-mode1", "file", hw.ConsoleLog,
		"--autostart-enabled", "off"}
	if hw.Paravirt != "" {
		if !slices.Contains(ParavirtProviders, hw.Paravirt) {
			return fmt.Errorf("vboxmanage: paravirtualization interface %q is not one of %s", hw.Paravirt, strings.Join(ParavirtProviders, ", "))
		}
		args = append(args, "--paravirt-provider", hw.Paravirt)
	}
	_, err := h.run(ctx, args...)
	return err
}

// Resize sets vm's memory and CPUs, which VirtualBox changes only while
// the machine is powered off.
func (h Host) Resize(ctx context.Context, vm string, memoryMB, cpus int) error {
	if err := CheckName("VM", vm); err != nil {
		return err
	}
	if memoryMB < 4 || cpus < 1 {
		return fmt.Errorf("vboxmanage: %d MB and %d CPUs is not a machine", memoryMB, cpus)
	}
	_, err := h.run(ctx, "modifyvm", vm, "--memory", strconv.Itoa(memoryMB), "--cpus", strconv.Itoa(cpus))
	return err
}

// CloneLinked makes name a linked clone of from's snapshot with UUID
// snapshot, registered, in folder when it is not empty. Its disk is a
// differencing image on the snapshot's, and every MAC address is new.
func (h Host) CloneLinked(ctx context.Context, from, snapshot, name, folder string) error {
	if err := CheckName("VM", from); err != nil {
		return err
	}
	if err := CheckName("VM", name); err != nil {
		return err
	}
	if err := CheckUUID(snapshot); err != nil {
		return err
	}
	args := []string{"clonevm", from, "--snapshot", snapshot, "--options", "link", "--name", name, "--register"}
	if folder != "" {
		if err := CheckPath("VM folder", folder); err != nil {
			return err
		}
		args = append(args, "--basefolder", folder)
	}
	_, err := h.run(ctx, args...)
	return err
}

// AttachDVD puts the image at medium in vm's slot, or empties the drive
// when medium is "emptydrive".
func (h Host) AttachDVD(ctx context.Context, vm string, slot Slot, medium string) error {
	if err := CheckName("VM", vm); err != nil {
		return err
	}
	if medium != "emptydrive" {
		if err := CheckPath("DVD image", medium); err != nil {
			return err
		}
	}
	if err := CheckAdapter(slot.Controller); err != nil {
		return fmt.Errorf("vboxmanage: storage controller: %w", err)
	}
	_, err := h.run(ctx, "storageattach", vm, "--storagectl", slot.Controller,
		"--port", strconv.Itoa(slot.Port), "--device", strconv.Itoa(slot.Device), "--type", "dvddrive", "--medium", medium)
	return err
}

// EjectDVD empties the DVD drive in vm's slot, running or not, even when
// the guest has locked its tray, as a running Windows guest does.
func (h Host) EjectDVD(ctx context.Context, vm string, slot Slot) error {
	if err := CheckName("VM", vm); err != nil {
		return err
	}
	if err := CheckAdapter(slot.Controller); err != nil {
		return fmt.Errorf("vboxmanage: storage controller: %w", err)
	}
	_, err := h.run(ctx, "storageattach", vm, "--storagectl", slot.Controller,
		"--port", strconv.Itoa(slot.Port), "--device", strconv.Itoa(slot.Device), "--type", "dvddrive", "--medium", "emptydrive", "--forceunmount")
	return err
}

// Detach removes whatever drive is in vm's slot.
func (h Host) Detach(ctx context.Context, vm string, slot Slot) error {
	if err := CheckName("VM", vm); err != nil {
		return err
	}
	if err := CheckAdapter(slot.Controller); err != nil {
		return fmt.Errorf("vboxmanage: storage controller: %w", err)
	}
	_, err := h.run(ctx, "storageattach", vm, "--storagectl", slot.Controller,
		"--port", strconv.Itoa(slot.Port), "--device", strconv.Itoa(slot.Device), "--medium", "none")
	return err
}

// CloseDVD forgets the DVD image at path, deleting the file too when
// remove is true. An image no machine uses is the only one it accepts.
func (h Host) CloseDVD(ctx context.Context, path string, remove bool) error {
	if err := CheckPath("DVD image", path); err != nil {
		return err
	}
	args := []string{"closemedium", "dvd", path}
	if remove {
		args = append(args, "--delete")
	}
	_, err := h.run(ctx, args...)
	return err
}

// Unregister forgets vm and deletes its settings, logs, saved states and
// hard disks. VirtualBox refuses when another machine's disk is linked to
// one of its own.
func (h Host) Unregister(ctx context.Context, vm string) error {
	if err := CheckName("VM", vm); err != nil {
		return err
	}
	_, err := h.run(ctx, "unregistervm", vm, "--delete")
	return err
}
