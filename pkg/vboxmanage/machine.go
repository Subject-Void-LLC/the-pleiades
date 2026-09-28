// Machines: what is registered, what state each is in, and starting and
// stopping them.
package vboxmanage

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Machine states, as showvminfo reports VMState.
const (
	StatePoweroff = "poweroff"
	StateRunning  = "running"
	StateSaved    = "saved"
	StatePaused   = "paused"
	StateAborted  = "aborted"
)

// Machine is a registered VM, as showvminfo reports it.
type Machine struct {
	Name       string
	UUID       string
	State      string
	ConfigFile string
	// OSType is the guest OS type as showvminfo describes it, such as
	// "Windows Server 2025 (64-bit)"; its ID is not in that answer.
	OSType string
	// Firmware is what the machine boots with: BIOS, or EFI.
	Firmware         string
	MemoryMB         int
	CPUs             int
	AutostartEnabled bool
	// Snapshots are every snapshot, in the order VBoxManage lists them
	// (a parent before its children).
	Snapshots []Snapshot
	// CurrentSnapshotUUID is the snapshot the machine's state descends
	// from, or "" when it has none.
	CurrentSnapshotUUID string
	// NICs are its network adapters by number, from 1.
	NICs map[int]NIC
	// Slots are its storage slots, by controller, port and device.
	Slots []Slot
	// ConsoleLog is the host file its first serial port writes to, or ""
	// when it writes to none.
	ConsoleLog string
	// Values is everything showvminfo said, for what the fields above
	// leave out.
	Values Values
}

// Off reports whether the machine is in a state no guest code runs in:
// powered off, saved, or aborted. A paused machine is not off; its guest
// is held, not stopped.
func (m Machine) Off() bool {
	switch m.State {
	case StatePoweroff, StateSaved, StateAborted:
		return true
	}
	return false
}

// Windows reports whether the machine's guest OS type is a Windows one.
// VirtualBox names every Windows type starting with the word.
func (m Machine) Windows() bool {
	return strings.HasPrefix(m.OSType, "Windows")
}

// FreeBSD reports whether m's guest OS type is a FreeBSD one, as
// showvminfo describes it ("FreeBSD (64-bit)").
func (m Machine) FreeBSD() bool {
	return strings.HasPrefix(m.OSType, "FreeBSD")
}

// Ref is a machine's name and UUID, as list vms reports them.
type Ref struct {
	Name string
	UUID string
}

// Machine returns name's state. A machine that is not registered is an
// error wrapping ErrNotFound.
func (h Host) Machine(ctx context.Context, name string) (Machine, error) {
	if err := CheckName("VM", name); err != nil {
		return Machine{}, err
	}
	out, err := h.run(ctx, "showvminfo", name, "--machinereadable")
	if err != nil {
		return Machine{}, err
	}
	values, err := ParseMachineReadable(out.Stdout)
	if err != nil {
		return Machine{}, err
	}
	return machineFrom(values)
}

// machineFrom reads a Machine out of showvminfo's values.
func machineFrom(values Values) (Machine, error) {
	m := Machine{Values: values}
	m.Name, _ = values.Get("name")
	m.UUID, _ = values.Get("UUID")
	m.State, _ = values.Get("VMState")
	m.ConfigFile, _ = values.Get("CfgFile")
	m.OSType, _ = values.Get("ostype")
	m.Firmware, _ = values.Get("firmware")
	if m.Name == "" || m.UUID == "" || m.State == "" {
		return Machine{}, fmt.Errorf("vboxmanage: showvminfo gave no name, UUID or state")
	}
	for key, target := range map[string]*int{"memory": &m.MemoryMB, "cpus": &m.CPUs} {
		if text, ok := values.Get(key); ok {
			n, err := strconv.Atoi(text)
			if err != nil {
				return Machine{}, fmt.Errorf("vboxmanage: showvminfo's %s is %q, not a number", key, text)
			}
			*target = n
		}
	}
	autostart, _ := values.Get("autostart-enabled")
	m.AutostartEnabled = autostart == "on"
	m.Snapshots = snapshotsFrom(values)
	m.CurrentSnapshotUUID, _ = values.Get("CurrentSnapshotUUID")
	hardwareFrom(&m)
	return m, nil
}

// refLine is one line of list vms or list runningvms: "name" {uuid}.
var refLine = regexp.MustCompile(`^"(.*)" \{([0-9a-fA-F-]{36})\}$`)

// List returns every registered machine.
func (h Host) List(ctx context.Context) ([]Ref, error) {
	return h.list(ctx, "vms")
}

// Running returns every running machine.
func (h Host) Running(ctx context.Context) ([]Ref, error) {
	return h.list(ctx, "runningvms")
}

// list reads list vms or list runningvms.
func (h Host) list(ctx context.Context, what string) ([]Ref, error) {
	out, err := h.run(ctx, "list", what)
	if err != nil {
		return nil, err
	}
	var refs []Ref
	for _, line := range strings.Split(out.Stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		m := refLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("vboxmanage: list %s gave a line it does not recognize: %q", what, line)
		}
		refs = append(refs, Ref{Name: m[1], UUID: m[2]})
	}
	return refs, nil
}

// Start starts name with no window, as VirtualBox's headless frontend.
func (h Host) Start(ctx context.Context, name string) error {
	if err := CheckName("VM", name); err != nil {
		return err
	}
	_, err := h.run(ctx, "startvm", name, "--type", "headless")
	return err
}

// PowerOff stops name at once, as pulling its power would: whatever the
// guest had not written is lost. A machine that is not running is an
// error wrapping ErrNotRunning.
func (h Host) PowerOff(ctx context.Context, name string) error {
	return h.control(ctx, name, "poweroff")
}

// ACPIShutdown presses name's power button, asking the guest to shut down;
// it returns once the request is sent, not once the guest is off.
func (h Host) ACPIShutdown(ctx context.Context, name string) error {
	return h.control(ctx, name, "acpipowerbutton")
}

// control runs controlvm name action.
func (h Host) control(ctx context.Context, name, action string) error {
	if err := CheckName("VM", name); err != nil {
		return err
	}
	_, err := h.run(ctx, "controlvm", name, action)
	return err
}

// SetAutostart marks name to be started, or not, when the host's
// VirtualBox autostart service runs for its owner.
func (h Host) SetAutostart(ctx context.Context, name string, on bool) error {
	if err := CheckName("VM", name); err != nil {
		return err
	}
	value := "off"
	if on {
		value = "on"
	}
	_, err := h.run(ctx, "modifyvm", name, "--autostart-enabled", value, "--autostart-delay", "0")
	return err
}
