// The model's machines made from nothing: createvm, storage controllers,
// disks made new or copied from an image, the registered-disk list, the
// hex dump mediumio writes of a disk's start, and screenshots. A
// refusal's wording is the model's own unless it says it was captured.
package vboxmanagetest

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// osTypes are the OS type IDs the model knows, each with the description
// showvminfo writes for it.
var osTypes = map[string]string{
	"Windows2025_64": "Windows Server 2025 (64-bit)",
	"Windows2022_64": "Windows Server 2022 (64-bit)",
	"Ubuntu_64":      "Ubuntu (64-bit)",
	"FreeBSD_64":     "FreeBSD (64-bit)",
	"Other_64":       "Other/Unknown (64-bit)",
}

// defaultFolder is where the model puts a machine given no base folder.
const defaultFolder = `C:\Users\pleiades-gate\VirtualBox VMs`

// createvm answers registering an empty machine.
func (h *Host) createvm(args []string) vboxmanage.Output {
	f, err := flags(args)
	if err != nil {
		return errorOutput(err.Error())
	}
	name := f["--name"]
	if _, ok := f["--register"]; !ok || name == "" {
		return errorOutput("the model makes registered machines with a name only")
	}
	description, ok := osTypes[f["--ostype"]]
	if !ok {
		return errorOutput(fmt.Sprintf("Guest OS type '%s' is invalid", f["--ostype"]))
	}
	if h.find(name) != nil {
		return errorOutput(fmt.Sprintf("Machine settings file '%s' already exists", name))
	}
	folder := f["--basefolder"]
	if folder == "" {
		folder = defaultFolder
	}
	vm := &VM{Name: name, UUID: h.newUUID(), State: vboxmanage.StatePoweroff, MemoryMB: 128, CPUs: 1,
		Folder: folder + `\` + name, OSType: description, Firmware: "BIOS", NICs: map[int]vboxmanage.NIC{}}
	h.vms = append(h.vms, vm)
	return vboxmanage.Output{Stdout: fmt.Sprintf("Virtual machine '%s' is created and registered.\r\nUUID: %s\r\nSettings file: '%s'\r\n",
		name, vm.UUID, vm.Folder+`\`+name+".vbox")}
}

// storagectl answers adding a SATA or IDE controller, with the slots
// showvminfo then lists for it.
func (h *Host) storagectl(name string, args []string) vboxmanage.Output {
	vm := h.find(name)
	if vm == nil {
		return notFound(name)
	}
	f, err := flags(args)
	if err != nil {
		return errorOutput(err.Error())
	}
	controller := f["--name"]
	for _, s := range vm.Slots {
		if s.Controller == controller {
			return errorOutput(fmt.Sprintf("Storage controller named '%s' already exists", controller))
		}
	}
	switch f["--add"] {
	case "sata":
		ports, _ := strconv.Atoi(f["--portcount"])
		for port := range max(ports, 1) {
			vm.Slots = append(vm.Slots, vboxmanage.Slot{Controller: controller, ControllerType: f["--controller"], Port: port, Medium: "none"})
		}
	case "ide":
		for _, pd := range [][2]int{{0, 0}, {0, 1}, {1, 0}, {1, 1}} {
			vm.Slots = append(vm.Slots, vboxmanage.Slot{Controller: controller, ControllerType: f["--controller"], Port: pd[0], Device: pd[1], Medium: "none"})
		}
	default:
		return errorOutput("the model adds sata and ide controllers only")
	}
	return vboxmanage.Output{}
}

// createmedium answers making a new, empty disk.
func (h *Host) createmedium(args []string) vboxmanage.Output {
	if len(args) == 0 || args[0] != "disk" {
		return errorOutput("the model makes disks only")
	}
	f, err := flags(args[1:])
	if err != nil {
		return errorOutput(err.Error())
	}
	path := f["--filename"]
	if _, exists := h.Files[path]; exists {
		return errorOutput(fmt.Sprintf("Could not create the medium storage unit '%s'. VD: cannot create image because file exists (VERR_ALREADY_EXISTS)", path))
	}
	h.Files[path] = make([]byte, 1024)
	h.register(path)
	return vboxmanage.Output{Stdout: "Medium created. UUID: " + h.newUUID() + "\r\n", Stderr: progress}
}

// clonemedium answers copying a disk image into a new VDI, registering
// both, as VirtualBox does.
func (h *Host) clonemedium(args []string) vboxmanage.Output {
	if len(args) < 3 || args[0] != "disk" {
		return errorOutput("the model copies disks only")
	}
	from, to := args[1], args[2]
	data, ok := h.Files[from]
	if !ok {
		return errorOutput(fmt.Sprintf("Could not find file for the medium '%s' (VERR_FILE_NOT_FOUND)", from))
	}
	if _, exists := h.Files[to]; exists {
		return errorOutput(fmt.Sprintf("Cannot register the hard disk '%s' because a hard disk with that location already exists", to))
	}
	h.Files[to] = append([]byte(nil), data...)
	h.register(from)
	h.register(to)
	return vboxmanage.Output{Stdout: "Clone medium created in format 'VDI'. UUID: " + h.newUUID() + "\r\n", Stderr: progress}
}

// register adds path to the disks VirtualBox knows, once.
func (h *Host) register(path string) {
	for _, p := range h.disks {
		if strings.EqualFold(p, path) {
			return
		}
	}
	h.disks = append(h.disks, path)
}

// listHDDs answers list hdds, one block per registered disk.
func (h *Host) listHDDs() vboxmanage.Output {
	var b strings.Builder
	for i, path := range h.disks {
		fmt.Fprintf(&b, "UUID:           00000000-0000-4000-9000-%012d\r\nParent UUID:    base\r\nState:          created\r\nType:           normal (base)\r\nLocation:       %s\r\nStorage format: VDI\r\nCapacity:       65536 MBytes\r\nEncryption:     disabled\r\n\r\n", i+1, path)
	}
	return vboxmanage.Output{Stdout: b.String()}
}

// closeDisk answers forgetting a disk, and deleting its file too when
// asked, which VirtualBox refuses while a machine holds it.
func (h *Host) closeDisk(path string, args []string) vboxmanage.Output {
	for _, vm := range h.vms {
		for _, slot := range vm.Slots {
			if strings.EqualFold(slot.Medium, path) {
				return errorOutput(fmt.Sprintf("Cannot close medium '%s' because it is attached to the machine '%s'", path, vm.Name))
			}
		}
	}
	kept := h.disks[:0]
	for _, p := range h.disks {
		if !strings.EqualFold(p, path) {
			kept = append(kept, p)
		}
	}
	h.disks = kept
	if len(args) == 1 && args[0] == "--delete" {
		delete(h.Files, path)
	}
	return vboxmanage.Output{}
}

// dittoRun is the shortest run of lines the same as the one before that
// the model writes as one ditto line. The host was captured writing runs
// of up to 20 out in full and folding runs of 24 and 27, so the model
// folds from 24; no capture pins where between 21 and 24 the host starts.
const dittoRun = 24

// mediumio answers mediumio --disk=PATH cat --hex --offset=N --size=N in
// the form captured from the host: twelve hex digits of offset, sixteen
// bytes a line with a dash in the middle, and the bytes as text, with a
// long run of lines the same as the one before written as one ditto line
// and the last line always written out. It registers the disk, as
// VirtualBox does.
func (h *Host) mediumio(args []string) vboxmanage.Output {
	values := map[string]string{}
	for _, a := range args {
		key, value, _ := strings.Cut(a, "=")
		values[key] = value
	}
	path := values["--disk"]
	data, ok := h.Files[path]
	_, isCat := values["cat"]
	_, isHex := values["--hex"]
	if !ok || !isCat || !isHex {
		return errorOutput("the model answers mediumio --disk=PATH cat --hex for a disk it holds only")
	}
	h.register(path)
	offset, _ := strconv.Atoi(values["--offset"])
	size, _ := strconv.Atoi(values["--size"])
	end := min(offset+size, len(data))
	var b strings.Builder
	for at := offset; at < end; at += 16 {
		if run := repeats(data, offset, at, end); run >= dittoRun {
			fmt.Fprintf(&b, "**********  <ditto x %d>\r\n", run)
			at += (run - 1) * 16
			continue
		}
		row := data[at:min(at+16, end)]
		fmt.Fprintf(&b, "%012x:", at)
		text := make([]byte, len(row))
		for i, c := range row {
			sep := " "
			if i == 8 {
				sep = "-"
			}
			fmt.Fprintf(&b, "%s%02x", sep, c)
			text[i] = '.'
			if c >= 0x20 && c < 0x7f {
				text[i] = c
			}
		}
		b.WriteString(strings.Repeat("   ", 16-len(row)) + " " + string(text) + "\r\n")
	}
	return vboxmanage.Output{Stdout: b.String()}
}

// screenshot answers controlvm screenshotpng: a running machine's screen
// written to a file.
func (h *Host) screenshot(vm *VM, path string) vboxmanage.Output {
	if vm.State != vboxmanage.StateRunning {
		return errorOutput(fmt.Sprintf("Machine '%s' is not currently running.", vm.Name))
	}
	if screen, ok := h.Screens[vm.Name]; ok {
		h.Files[path] = screen
		return vboxmanage.Output{}
	}
	// A PNG's signature and header chunk: 1024 by 768, as the lab's
	// screens were captured.
	h.Files[path] = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR\x00\x00\x04\x00\x00\x00\x03\x00\x08\x02\x00\x00\x00")
	return vboxmanage.Output{}
}

// repeats counts the whole lines from at on that are the same as the line
// before at, in a dump from start to end, stopping short of its last
// line, which is always written out. The first line has none before it.
func repeats(data []byte, start, at, end int) int {
	if at-16 < start {
		return 0
	}
	run := 0
	for next := at; next+16 < end && bytes.Equal(data[next:next+16], data[at-16:at]); next += 16 {
		run++
	}
	return run
}
