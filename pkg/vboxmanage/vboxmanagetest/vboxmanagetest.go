// Package vboxmanagetest is a model of a Windows VirtualBox host, for
// testing code built on pkg/vboxmanage without one. Host answers the
// programs pkg/vboxmanage runs (VBoxManage, whoami, tasklist, sc) from a
// small model of the host's machines and snapshots, in the formats
// VirtualBox 7.2.20 on Windows 11 wrote them when they were captured into
// pkg/vboxmanage/testdata, and its own tests hold it to those captures.
//
// It keeps the rule the real host was measured to keep: a VM does not
// start from the runner's own logon unless one of the account's VMs
// already runs, and the account's autostart service starts the VMs marked
// for it. A model is not the host, so a Release Gate still runs the real
// one; this is what lets a method's own logic be tested on every run.
package vboxmanagetest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// Path is where the model says VBoxManage is, as a host's record would.
const Path = `C:\Program Files\Oracle\VirtualBox\VBoxManage.exe`

// Snapshot is one snapshot in the model.
type Snapshot struct {
	Name        string
	UUID        string
	Description string
	// Parent is the parent snapshot's UUID, or "" for the root.
	Parent string
}

// VM is one machine in the model.
type VM struct {
	Name      string
	UUID      string
	State     string
	MemoryMB  int
	CPUs      int
	Autostart bool
	// Snapshots are in the order they were taken.
	Snapshots []Snapshot
	// Current is the current snapshot's UUID, or "" when there is none.
	Current string
	// PowerButtonReads is how many reads after a power button press still
	// find the VM running, as a guest takes a while to shut down; below
	// zero, the guest never answers the button.
	PowerButtonReads int
	// Folder is the VM's folder on the host.
	Folder string
	// Slots are its storage slots and what is in them.
	Slots []vboxmanage.Slot
	// NICs are its network adapters by number, from 1; one absent is off.
	NICs map[int]vboxmanage.NIC
	// ConsoleLog is the file its first serial port writes to, or "".
	ConsoleLog string
	// LinkedFrom is the VM a linked clone's disk descends from, or "".
	LinkedFrom string
	// Extra is its extradata.
	Extra map[string]string

	pressed     bool
	currentNode string
}

// Host is the model host. Its zero value has no machines, the account
// vengeance\pleiades-gate, and the account's autostart service installed.
type Host struct {
	// Account is the runner's logon, as whoami writes it.
	Account string
	// NoAutostartService says the account's autostart service is not
	// installed.
	NoAutostartService bool
	// Fail makes a call fail instead of being answered: a key is the start
	// of a call as Calls records it, and its value is the output given.
	Fail map[string]vboxmanage.Output
	// Skip lets the first Skip[key] calls matching a Fail key through
	// before the failure applies, so a test can fail a later read of a
	// machine and not the first.
	Skip map[string]int
	// Appliances are the OVA files on the host, by path, each as the VM
	// importing it makes.
	Appliances map[string]*VM
	// Files are the other files on the host, by path.
	Files map[string][]byte
	// CPUs, MemoryMB and AvailableMB are what list hostinfo reports; each
	// left zero reports what the captured host did.
	CPUs, MemoryMB, AvailableMB int

	mu    sync.Mutex
	vms   []*VM
	calls []string
	uuids int
	macs  int
}

// New returns a host with vms registered.
func New(vms ...*VM) *Host {
	return &Host{vms: vms, Appliances: map[string]*VM{}, Files: map[string][]byte{}}
}

// VBoxHost returns a vboxmanage.Host reaching h.
func (h *Host) VBoxHost() vboxmanage.Host {
	return vboxmanage.Host{Runner: h, Path: Path}
}

// VM returns the machine called name, or nil.
func (h *Host) VM(name string) *VM {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.find(name)
}

// SetFile puts data at path on the host, as something on it would, such
// as a running VM writing its console log.
func (h *Host) SetFile(path string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Files[path] = data
}

// Calls returns each call so far: the program's file name, then its
// arguments, space separated.
func (h *Host) Calls() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

// Run answers one call, as vboxmanage.Runner.
func (h *Host) Run(_ context.Context, program string, args []string) (vboxmanage.Output, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	base := program[strings.LastIndex(program, `\`)+1:]
	call := strings.TrimSpace(base + " " + strings.Join(args, " "))
	h.calls = append(h.calls, call)
	if out, ok := h.failure(call); ok {
		return out, nil
	}
	switch base {
	case "VBoxManage.exe":
		return h.vboxmanage(args), nil
	case "whoami.exe":
		return vboxmanage.Output{Stdout: h.account() + "\r\n"}, nil
	case "tasklist.exe":
		return vboxmanage.Output{Stdout: "INFO: No tasks are running which match the specified criteria.\r\n"}, nil
	case "sc.exe":
		return h.sc(args), nil
	}
	return vboxmanage.Output{}, fmt.Errorf("vboxmanagetest: the model does not run %s", program)
}

// failure returns the output Fail names for call, taking the longest key
// that starts it, so a narrower key wins over a broader one.
func (h *Host) failure(call string) (vboxmanage.Output, bool) {
	keys := make([]string, 0, len(h.Fail))
	for key := range h.Fail {
		if strings.HasPrefix(call, key) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return vboxmanage.Output{}, false
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	if h.Skip[keys[0]] > 0 {
		h.Skip[keys[0]]--
		return vboxmanage.Output{}, false
	}
	return h.Fail[keys[0]], true
}

// account is whoami's answer.
func (h *Host) account() string {
	if h.Account == "" {
		return `vengeance\pleiades-gate`
	}
	return h.Account
}

// find returns the machine name names, by name or UUID.
func (h *Host) find(name string) *VM {
	for _, vm := range h.vms {
		if vm.Name == name || strings.EqualFold(vm.UUID, name) {
			return vm
		}
	}
	return nil
}

// anyRunning reports whether one of the account's machines runs.
func (h *Host) anyRunning() bool {
	for _, vm := range h.vms {
		if vm.State == vboxmanage.StateRunning {
			return true
		}
	}
	return false
}

// newUUID returns the next UUID the model hands out.
func (h *Host) newUUID() string {
	h.uuids++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", h.uuids)
}

// errorOutput is a failed VBoxManage run, its lines on standard error.
func errorOutput(lines ...string) vboxmanage.Output {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString("VBoxManage.exe: error: " + line + "\r\n")
	}
	return vboxmanage.Output{Stderr: b.String(), ExitCode: 1}
}

// progress is what VBoxManage writes to standard error while it waits.
const progress = "0%...10%...20%...30%...40%...50%...60%...70%...80%...90%...100%\r\n"

// notFound is the captured answer for a machine that is not registered.
func notFound(name string) vboxmanage.Output {
	return errorOutput(
		fmt.Sprintf("Could not find a registered machine named '%s'", name),
		"Details: code VBOX_E_OBJECT_NOT_FOUND (0x80bb0001), component VirtualBoxWrap, interface IVirtualBox, callee IUnknown",
		`Context: "FindMachine(Bstr(VMNameOrUuid).raw(), machine.asOutParam())" at line 3249 of file VBoxManageInfo.cpp`)
}

// locked is the answer for a change VirtualBox refuses while a session
// holds the machine. It is the model's wording, not a captured one.
func locked(name string) vboxmanage.Output {
	return errorOutput(
		fmt.Sprintf("The machine '%s' is already locked for a session (or being unlocked)", name),
		"Details: code VBOX_E_INVALID_OBJECT_STATE (0x80bb0007), component MachineWrap, interface IMachine")
}

// vboxmanage answers one VBoxManage call.
func (h *Host) vboxmanage(args []string) vboxmanage.Output {
	if len(args) == 0 {
		return errorOutput("no command")
	}
	switch {
	case len(args) == 2 && args[0] == "list":
		return h.list(args[1])
	case len(args) == 3 && args[0] == "showvminfo" && args[2] == "--machinereadable":
		vm := h.find(args[1])
		if vm == nil {
			return notFound(args[1])
		}
		return vboxmanage.Output{Stdout: vm.showvminfo()}
	case len(args) == 4 && args[0] == "startvm" && args[2] == "--type" && args[3] == "headless":
		return h.startvm(args[1])
	case len(args) == 3 && args[0] == "controlvm":
		return h.controlvm(args[1], args[2])
	case len(args) >= 2 && args[0] == "modifyvm":
		return h.modifyvm(args[1], args[2:])
	case len(args) >= 2 && args[0] == "import":
		return h.importOVA(args[1], args[2:])
	case len(args) >= 2 && args[0] == "clonevm":
		return h.clonevm(args[1], args[2:])
	case len(args) >= 2 && args[0] == "storageattach":
		return h.storageattach(args[1], args[2:])
	case len(args) >= 3 && args[0] == "closemedium" && args[1] == "dvd":
		return h.closemedium(args[2], args[3:])
	case len(args) == 3 && args[0] == "unregistervm" && args[2] == "--delete":
		return h.unregistervm(args[1])
	case len(args) == 3 && args[0] == "getextradata" && args[2] == "enumerate":
		return h.getextradata(args[1])
	case (len(args) == 3 || len(args) == 4) && args[0] == "setextradata":
		return h.setextradata(args[1], args[2], args[3:])
	case len(args) >= 4 && args[0] == "snapshot":
		return h.snapshot(args[1], args[2], args[3], args[4:])
	}
	return errorOutput("the model does not answer " + strings.Join(args, " "))
}

// list answers list vms and list runningvms.
func (h *Host) list(what string) vboxmanage.Output {
	if what == "hostinfo" {
		return h.hostinfo()
	}
	var b strings.Builder
	for _, vm := range h.vms {
		if what == "vms" || (what == "runningvms" && vm.State == vboxmanage.StateRunning) {
			fmt.Fprintf(&b, "%q {%s}\r\n", vm.Name, vm.UUID)
		}
	}
	return vboxmanage.Output{Stdout: b.String()}
}

// hostinfo answers list hostinfo in the captured form (list-hostinfo.stdout),
// with the model's processors and memory in it.
func (h *Host) hostinfo() vboxmanage.Output {
	value := func(set, captured int) int {
		if set != 0 {
			return set
		}
		return captured
	}
	cpus := value(h.CPUs, 20)
	var b strings.Builder
	b.WriteString("Host Information:\r\n\r\nHost time: 2026-09-27T16:20:57.030000000Z\r\n")
	fmt.Fprintf(&b, "Processor online count: %d\r\nProcessor count: %d\r\nProcessor online core count: %d\r\nProcessor core count: %d\r\n", cpus, cpus, cpus, cpus)
	b.WriteString("Processor supports HW virtualization: yes\r\nProcessor supports PAE: yes\r\nProcessor supports long mode: yes\r\nProcessor supports nested paging: yes\r\nProcessor supports unrestricted guest: no\r\nProcessor supports nested HW virtualization: no\r\nProcessor supports virt. vmsave/vmload: no\r\n")
	for n := range cpus {
		fmt.Fprintf(&b, "Processor#%d speed: unknown\r\nProcessor#%d description: Intel(R) Core(TM) Ultra 7 265KF\r\n", n, n)
	}
	fmt.Fprintf(&b, "Memory size: %d MByte\r\nMemory available: %d MByte\r\n", value(h.MemoryMB, 32388), value(h.AvailableMB, 15121))
	b.WriteString("Operating system: Windows 11\r\nOperating system version: 10.0.26200.9457\r\n")
	return vboxmanage.Output{Stdout: b.String()}
}

// startvm starts a machine from the runner's own logon, which works only
// while another of the account's machines runs: the captured failure is
// what VirtualBox gave on the host when none did.
func (h *Host) startvm(name string) vboxmanage.Output {
	vm := h.find(name)
	if vm == nil {
		return notFound(name)
	}
	if !vm.off() {
		return locked(vm.Name)
	}
	waiting := fmt.Sprintf("Waiting for VM %q to power on...\r\n", vm.Name)
	if !h.anyRunning() {
		out := errorOutput(
			"Failed to load API DLL: WinHvPlatform.dll: VERR_UNRESOLVED_ERROR (VERR_NEM_INIT_FAILED).",
			"VT-x is not available (VERR_VMX_NO_VMX)",
			"Details: code E_FAIL (0x80004005), component ConsoleWrap, interface IConsole")
		out.Stdout = waiting
		return out
	}
	vm.State = vboxmanage.StateRunning
	vm.pressed = false
	return vboxmanage.Output{Stdout: waiting + fmt.Sprintf("VM %q has been successfully started.\r\n", vm.Name)}
}

// controlvm answers poweroff and acpipowerbutton.
func (h *Host) controlvm(name, action string) vboxmanage.Output {
	vm := h.find(name)
	if vm == nil {
		return notFound(name)
	}
	if vm.State != vboxmanage.StateRunning && vm.State != vboxmanage.StatePaused {
		return errorOutput(fmt.Sprintf("Machine '%s' is not currently running.", vm.Name))
	}
	switch action {
	case "poweroff":
		vm.State = vboxmanage.StatePoweroff
		return vboxmanage.Output{Stderr: progress}
	case "acpipowerbutton":
		vm.pressed = true
		return vboxmanage.Output{}
	}
	return errorOutput("the model does not answer controlvm " + action)
}

// sc answers sc start and sc query for the account's autostart service,
// whose run starts every machine marked for autostart.
func (h *Host) sc(args []string) vboxmanage.Output {
	if len(args) != 2 {
		return vboxmanage.Output{ExitCode: 1, Stdout: "[SC] the model does not answer this\r\n"}
	}
	domain, user, _ := strings.Cut(h.account(), `\`)
	if h.NoAutostartService || args[1] != "VBoxAutostartSvc"+strings.ToLower(domain)+user {
		return vboxmanage.Output{ExitCode: 1060, Stdout: "[SC] OpenService FAILED 1060:\r\n\r\nThe specified service does not exist as an installed service.\r\n"}
	}
	switch args[0] {
	case "start":
		for _, vm := range h.vms {
			if vm.Autostart && vm.off() {
				vm.State = vboxmanage.StateRunning
				vm.pressed = false
			}
		}
		return vboxmanage.Output{Stdout: "        STATE              : 4  RUNNING\r\n"}
	case "query":
		return vboxmanage.Output{Stdout: "        STATE              : 1  STOPPED\r\n"}
	}
	return vboxmanage.Output{ExitCode: 1, Stdout: "[SC] the model does not answer sc " + args[0] + "\r\n"}
}

// off reports whether no guest code runs in vm.
func (vm *VM) off() bool {
	return vboxmanage.Machine{State: vm.State}.Off()
}

// showvminfo writes vm as showvminfo --machinereadable does, keeping the
// captured order of the lines it has, and advances a pressed power button.
func (vm *VM) showvminfo() string {
	if vm.pressed && vm.State == vboxmanage.StateRunning {
		switch {
		case vm.PowerButtonReads == 0:
			vm.State = vboxmanage.StatePoweroff
			vm.pressed = false
		case vm.PowerButtonReads > 0:
			vm.PowerButtonReads--
		}
	}
	var b strings.Builder
	line := func(key, value string) { fmt.Fprintf(&b, "%s=%s\r\n", key, value) }
	line("name", quote(vm.Name))
	line("UUID", quote(vm.UUID))
	if vm.Folder != "" {
		line("CfgFile", quote(vm.Folder+`\`+vm.Name+".vbox"))
	}
	line("memory", fmt.Sprint(vm.MemoryMB))
	line("cpus", fmt.Sprint(vm.CPUs))
	line("VMState", quote(vm.State))
	autostart := "off"
	if vm.Autostart {
		autostart = "on"
	}
	line("autostart-enabled", quote(autostart))
	line("autostart-delay", "0")
	vm.writeHardware(line)
	vm.writeSnapshots(&b, "", "")
	return b.String()
}

// quote writes a value as --machinereadable does: in quotes, with a
// backslash before each backslash and quote.
func quote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}
