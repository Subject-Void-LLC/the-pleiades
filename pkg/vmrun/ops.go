// The operations a host offers: power, snapshots, guest address, networks,
// and creating and configuring a VM, each one vmrun or vmcli command.
package vmrun

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
)

// Running returns the vmx path of every VM the product is running.
func (h Host) Running(ctx context.Context) ([]string, error) {
	out, err := h.vmrun(ctx, "list", "list")
	if err != nil {
		return nil, err
	}
	return parseCounted(out.Stdout, "Total running VMs:")
}

// PowerState returns the VM's power state as vmcli names it ("off", "on",
// "suspended", "paused").
func (h Host) PowerState(ctx context.Context, vmx string) (string, error) {
	out, err := h.vmcli(ctx, "power", vmx, "Power", "query")
	if err != nil {
		return "", err
	}
	state := parseKeyValues(out.Stdout)["PowerState"]
	if state == "" {
		return "", fmt.Errorf("vmcli power query printed no PowerState: %s", farside.Output(out.Stdout))
	}
	return state, nil
}

// Start powers the VM on, without a window unless gui.
func (h Host) Start(ctx context.Context, vmx string, gui bool) error {
	if err := CheckVMX(vmx); err != nil {
		return err
	}
	mode := "nogui"
	if gui {
		mode = "gui"
	}
	_, err := h.detached(ctx, "start", "start", vmx, mode)
	return err
}

// Stop powers the VM off: a guest shutdown, or a hard power-off when hard.
func (h Host) Stop(ctx context.Context, vmx string, hard bool) error {
	return h.power(ctx, "stop", vmx, hard)
}

// Suspend suspends the VM to disk, or hard when hard.
func (h Host) Suspend(ctx context.Context, vmx string, hard bool) error {
	return h.power(ctx, "suspend", vmx, hard)
}

// Reset restarts the VM, a guest restart or hard when hard.
func (h Host) Reset(ctx context.Context, vmx string, hard bool) error {
	return h.power(ctx, "reset", vmx, hard)
}

// power is the stop, suspend and reset commands, which differ in their word.
func (h Host) power(ctx context.Context, verb, vmx string, hard bool) error {
	if err := CheckVMX(vmx); err != nil {
		return err
	}
	how := "soft"
	if hard {
		how = "hard"
	}
	if verb == "reset" {
		// A reset starts the VM process again, as start does.
		_, err := h.detached(ctx, verb, verb, vmx, how)
		return err
	}
	_, err := h.vmrun(ctx, verb, verb, vmx, how)
	return err
}

// DeleteVM unregisters the VM and removes its vmx and the files vmrun owns.
// vmrun leaves a disk made by vmcli behind, so a caller that means to remove
// the whole VM removes its directory too (RemoveVMDir).
func (h Host) DeleteVM(ctx context.Context, vmx string) error {
	if err := CheckVMX(vmx); err != nil {
		return err
	}
	_, err := h.vmrun(ctx, "deleteVM", "deleteVM", vmx)
	return err
}

// Snapshots returns the names of the VM's snapshots, oldest first.
func (h Host) Snapshots(ctx context.Context, vmx string) ([]string, error) {
	if err := CheckVMX(vmx); err != nil {
		return nil, err
	}
	out, err := h.vmrun(ctx, "listSnapshots", "listSnapshots", vmx)
	if err != nil {
		return nil, err
	}
	return parseCounted(out.Stdout, "Total snapshots:")
}

// TakeSnapshot takes a snapshot called name. A name already in use is
// ErrExists.
func (h Host) TakeSnapshot(ctx context.Context, vmx, name string) error {
	if err := checkSnapshot(vmx, name); err != nil {
		return err
	}
	_, err := h.vmrun(ctx, "snapshot", "snapshot", vmx, name)
	return err
}

// RevertToSnapshot returns the VM to the snapshot called name. A snapshot that
// does not exist is ErrNotFound.
func (h Host) RevertToSnapshot(ctx context.Context, vmx, name string) error {
	if err := checkSnapshot(vmx, name); err != nil {
		return err
	}
	_, err := h.vmrun(ctx, "revertToSnapshot", "revertToSnapshot", vmx, name)
	return err
}

// DeleteSnapshot deletes the snapshot called name, and its children when
// children.
func (h Host) DeleteSnapshot(ctx context.Context, vmx, name string, children bool) error {
	if err := checkSnapshot(vmx, name); err != nil {
		return err
	}
	args := []string{"deleteSnapshot", vmx, name}
	if children {
		args = append(args, "andDeleteChildren")
	}
	_, err := h.vmrun(ctx, "deleteSnapshot", args...)
	return err
}

func checkSnapshot(vmx, name string) error {
	if err := CheckVMX(vmx); err != nil {
		return err
	}
	return CheckSnapshotName(name)
}

// GuestIP returns the guest's IP address as VMware Tools reports it. A VM
// that is not running is ErrNotRunning; one running without the tools has no
// address to give, which is ErrNoTools.
//
// A plain getGuestIPAddress is not trusted when it says the tools are not
// running: on a real Fusion 26.0.1, a Windows guest resumed from suspend kept
// answering that for minutes while checkToolsState said running and
// getGuestIPAddress -wait returned its address at once. So that answer is
// checked against checkToolsState, and when the tools are running the address
// is asked for again with -wait, bounded by waitIP.
func (h Host) GuestIP(ctx context.Context, vmx string) (string, error) {
	if err := CheckVMX(vmx); err != nil {
		return "", err
	}
	out, err := h.vmrun(ctx, "getGuestIPAddress", "getGuestIPAddress", vmx)
	if err == nil {
		return strings.TrimSpace(out.Stdout), nil
	}
	if !errors.Is(err, ErrNoTools) {
		return "", err
	}
	state, serr := h.ToolsState(ctx, vmx)
	if serr != nil || state != "running" {
		return "", err
	}
	wctx, cancel := context.WithTimeout(ctx, waitIP)
	defer cancel()
	out, werr := h.vmrun(wctx, "getGuestIPAddress", "getGuestIPAddress", vmx, "-wait")
	if werr != nil {
		return "", fmt.Errorf("VMware Tools are running but gave no address within %s: %w", waitIP, werr)
	}
	return strings.TrimSpace(out.Stdout), nil
}

// waitIP bounds getGuestIPAddress -wait, which otherwise waits as long as
// the guest takes to report one.
var waitIP = 60 * time.Second

// ToolsState returns what vmrun says of VMware Tools in the guest: running,
// installed (but not running) or unknown, as vmrun names them.
func (h Host) ToolsState(ctx context.Context, vmx string) (string, error) {
	if err := CheckVMX(vmx); err != nil {
		return "", err
	}
	out, err := h.vmrun(ctx, "checkToolsState", "checkToolsState", vmx)
	if err != nil {
		return "", err
	}
	return lineStarting(out.Stdout, ""), nil
}

// Network is one of the host's virtual networks.
type Network struct {
	Index        int
	Name, Type   string
	DHCP         bool
	Subnet, Mask string
}

// Networks returns the host's virtual networks (vmnet0, vmnet8, ...).
func (h Host) Networks(ctx context.Context) ([]Network, error) {
	out, err := h.vmrun(ctx, "listHostNetworks", "listHostNetworks")
	if err != nil {
		return nil, err
	}
	return parseNetworks(out.Stdout)
}

// Config returns the VM's configuration entries (its vmx keys) as vmcli
// lists them.
func (h Host) Config(ctx context.Context, vmx string) (map[string]string, error) {
	out, err := h.vmcli(ctx, "config", vmx, "ConfigParams", "query")
	if err != nil {
		return nil, err
	}
	return parseKeyValues(out.Stdout), nil
}

// SetConfig sets one configuration entry. The key is checked as a vmx key,
// since vmcli takes it as an argument and a VM's keys are its whole behavior.
func (h Host) SetConfig(ctx context.Context, vmx, key, value string) error {
	if err := CheckConfigKey(key); err != nil {
		return err
	}
	if err := checkText("value", value, 1024); err != nil {
		return err
	}
	_, err := h.vmcli(ctx, "setConfig", vmx, "ConfigParams", "SetEntry", key, value)
	return err
}

// CheckConfigKey refuses a vmx key that is empty, that begins with a hyphen,
// or that holds anything but letters, digits, dot, underscore and hyphen.
func CheckConfigKey(key string) error {
	if key == "" || len(key) > 128 || key[0] == '-' {
		return fmt.Errorf("config key %q must be 1 to 128 characters and not begin with a hyphen", key)
	}
	for _, r := range key {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return fmt.Errorf("config key %q holds %q, which a vmx key does not", key, r)
		}
	}
	return nil
}

// SetMemory sets the VM's memory in megabytes.
func (h Host) SetMemory(ctx context.Context, vmx string, mb int) error {
	if mb < 128 || mb > 1<<20 {
		return fmt.Errorf("memory %d MB is outside 128 MB to 1 TB", mb)
	}
	_, err := h.vmcli(ctx, "setMemory", vmx, "Chipset", "SetMemSize", strconv.Itoa(mb))
	return err
}

// SetCPUs sets the VM's virtual CPU count.
func (h Host) SetCPUs(ctx context.Context, vmx string, n int) error {
	if n < 1 || n > 256 {
		return fmt.Errorf("cpu count %d is outside 1 to 256", n)
	}
	_, err := h.vmcli(ctx, "setCPUs", vmx, "Chipset", "SetVCpuCount", strconv.Itoa(n))
	return err
}

// CreateVM creates an empty VM called name in directory dir and returns its
// vmx path. guest is a vmcli guest type (arm-ubuntu-64, arm-windows11-64).
// vmcli makes the vmx and a default disk beside it.
func (h Host) CreateVM(ctx context.Context, name, dir, guest string) (string, error) {
	if err := CheckName("vm name", name); err != nil {
		return "", err
	}
	if err := CheckTool(dir); err != nil {
		return "", fmt.Errorf("vm directory: %w", err)
	}
	if err := CheckGuestType(guest); err != nil {
		return "", err
	}
	out, err := h.vmcli(ctx, "create", "", "VM", "Create", "-n", name, "-d", dir, "-c", guest)
	if err != nil {
		return "", err
	}
	vmx := parseCreated(out.Stdout)
	if vmx == "" {
		return "", fmt.Errorf("vmcli created a VM and did not say where: %s", farside.Output(out.Stdout))
	}
	return vmx, nil
}

// CheckGuestType refuses a guest type that is not the form vmcli names them
// in: letters, digits and hyphens, such as arm-ubuntu-64.
func CheckGuestType(guest string) error {
	if guest == "" || len(guest) > 64 || guest[0] == '-' {
		return fmt.Errorf("guest type %q must be 1 to 64 characters and not begin with a hyphen", guest)
	}
	for _, r := range guest {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return fmt.Errorf("guest type %q holds %q, which a vmcli guest type does not", guest, r)
		}
	}
	return nil
}

// SendKeys sends a key sequence to the VM's console, which is how the
// "press any key to boot from CD" prompt of a Windows install disc is
// answered.
func (h Host) SendKeys(ctx context.Context, vmx, sequence string) error {
	if err := checkText("key sequence", sequence, 256); err != nil {
		return err
	}
	if sequence == "" || sequence[0] == '-' {
		return fmt.Errorf("key sequence %q is empty or begins with a hyphen", sequence)
	}
	_, err := h.vmcli(ctx, "sendKeys", vmx, "MKS", "sendKeySequence", sequence)
	return err
}

// VMXExists reports whether a file is at vmx on the host. A VM whose file is
// gone is an answer, not a failure, which is why this is asked before any tool
// that would fail on it.
func (h Host) VMXExists(ctx context.Context, vmx string) (bool, error) {
	if err := CheckVMX(vmx); err != nil {
		return false, err
	}
	out, err := h.Runner.Run(ctx, []string{"/bin/test", "-f", vmx})
	if err != nil {
		return false, err
	}
	return out.ExitCode == 0, nil
}

// keepAwakeScript finds the vmware-vmx process running the VM whose vmx is $1,
// matching the path as a fixed string, and starts caffeinate in the background
// to hold the Mac awake until that process ends. It prints the process ID, or
// exits 3 when no such process runs.
//
// It exists because a VM started headless from an SSH session is throttled by
// macOS as background work: on 2026-10-26 a Windows 11 guest ran about five
// times slower than its own clock and its VMware Tools install hung, and
// "caffeinate -dimsu -w <pid>" brought it to full speed at once. -w ends the
// assertion with the VM, so nothing is left holding the Mac awake after it.
const keepAwakeScript = `p=$(ps -axo pid=,command= | grep -F -- vmware-vmx | grep -F -- "$1" | grep -v -F -- grep | awk '{print $1; exit}')
[ -n "$p" ] || exit 3
nohup /usr/bin/caffeinate -dimsu -w "$p" >/dev/null 2>&1 </dev/null &
printf '%s\n' "$p"`

// KeepAwake holds a Mac awake for as long as the VM at vmx runs (see
// keepAwakeScript) and returns the VM process's ID. It is Fusion's alone:
// caffeinate is macOS's.
func (h Host) KeepAwake(ctx context.Context, vmx string) (int, error) {
	if h.Product != Fusion {
		return 0, fmt.Errorf("keeping the host awake is a Mac's, and this host runs %q", h.Product)
	}
	if err := CheckVMX(vmx); err != nil {
		return 0, err
	}
	out, err := h.run(ctx, "keepAwake", "/bin/sh", "-c", keepAwakeScript, "sh", vmx)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(out.Stdout))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("keeping the host awake named %s as the VM's process", farside.Output(out.Stdout))
	}
	return pid, nil
}
