// Making a machine from nothing: an empty VM registered with its firmware
// and storage controllers, a disk made new or copied in from an image,
// what VirtualBox can read of a disk's first sectors, and a picture of a
// machine's screen.
package vboxmanage

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The firmware a new machine boots with, as modifyvm --firmware takes it.
const (
	FirmwareBIOS = "bios"
	FirmwareEFI  = "efi"
)

// The storage controllers CreateVM gives a machine: SATA for its disk and a
// Windows clone's seed, and IDE for other DVDs, where virt.vbox.vm.clone
// puts a Linux clone's seed.
const (
	ControllerSATA = "SATA"
	ControllerIDE  = "IDE"
)

// nicType is the network card every adapter of a machine CreateVM makes
// emulates: Intel's PRO/1000 MT Desktop, which Windows and Linux both
// have a driver for. VirtualBox's own default card has none in Windows.
const nicType = "82540EM"

// osTypePattern is a guest OS type ID, as VBoxManage list ostypes names
// one: Windows2025_64, Ubuntu_64.
var osTypePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,39}$`)

// CheckOSType refuses an OS type ID outside osTypePattern.
func CheckOSType(id string) error {
	if !osTypePattern.MatchString(id) {
		return fmt.Errorf("vboxmanage: OS type %q is not an ID such as Windows2025_64, as VBoxManage list ostypes names them", id)
	}
	return nil
}

// NewMachine is the machine CreateVM makes.
type NewMachine struct {
	Name string
	// OSType is its guest OS type ID, such as Windows2025_64.
	OSType string
	// Folder is where its folder goes, or "" for VirtualBox's default.
	Folder string
	// Firmware is FirmwareBIOS or FirmwareEFI.
	Firmware       string
	MemoryMB, CPUs int
	// ConsoleLog is the host file its first serial port writes to, or ""
	// for no serial port.
	ConsoleLog string
}

// check refuses a NewMachine this package would not send.
func (m NewMachine) check() error {
	if err := CheckName("VM", m.Name); err != nil {
		return err
	}
	if err := CheckOSType(m.OSType); err != nil {
		return err
	}
	if m.Folder != "" {
		if err := CheckPath("VM folder", m.Folder); err != nil {
			return err
		}
	}
	if m.Firmware != FirmwareBIOS && m.Firmware != FirmwareEFI {
		return fmt.Errorf("vboxmanage: firmware %q is not %s or %s", m.Firmware, FirmwareBIOS, FirmwareEFI)
	}
	if m.MemoryMB < 4 || m.CPUs < 1 {
		return fmt.Errorf("vboxmanage: %d MB and %d CPUs is not a machine", m.MemoryMB, m.CPUs)
	}
	if m.ConsoleLog != "" {
		return CheckPath("console log", m.ConsoleLog)
	}
	return nil
}

// CreateVM registers an empty machine: its firmware, memory and CPUs, the
// 64-bit, I/O APIC and UTC clock settings a modern guest expects, a
// graphics card Windows has a driver for, network adapters of a card
// Windows and Linux both drive with nothing attached to them, the boot
// order disk then DVD, a SATA controller for its disk and an IDE one for
// DVDs. A machine whose settings fail after it is registered is left
// registered, for the caller to delete.
func (h Host) CreateVM(ctx context.Context, m NewMachine) error {
	if err := m.check(); err != nil {
		return err
	}
	create := []string{"createvm", "--name", m.Name, "--platform-architecture", "x86", "--ostype", m.OSType, "--register"}
	if m.Folder != "" {
		create = append(create, "--basefolder", m.Folder)
	}
	if _, err := h.run(ctx, create...); err != nil {
		return err
	}
	settings := []string{"modifyvm", m.Name,
		"--firmware", m.Firmware, "--memory", strconv.Itoa(m.MemoryMB), "--cpus", strconv.Itoa(m.CPUs),
		"--ioapic", "on", "--x86-long-mode", "on", "--rtc-use-utc", "on",
		"--graphicscontroller", "vboxsvga", "--vram", "32",
		"--nic1", "none", "--nic-type1", nicType, "--nic-type2", nicType,
		"--boot1", "disk", "--boot2", "dvd", "--boot3", "none", "--boot4", "none"}
	if m.ConsoleLog != "" {
		settings = append(settings, "--uart1", "0x3F8", "4", "--uart-mode1", "file", m.ConsoleLog)
	}
	if _, err := h.run(ctx, settings...); err != nil {
		return err
	}
	// Two SATA ports: the disk, and a DVD a Windows clone's seed goes in,
	// since a Windows image made on Hyper-V has no IDE driver running when
	// it first looks for its answer file.
	if _, err := h.run(ctx, "storagectl", m.Name, "--name", ControllerSATA, "--add", "sata", "--controller", "IntelAhci", "--portcount", "2", "--bootable", "on"); err != nil {
		return err
	}
	_, err := h.run(ctx, "storagectl", m.Name, "--name", ControllerIDE, "--add", "ide", "--controller", "PIIX4")
	return err
}

// SetFirmware sets what vm boots with, FirmwareBIOS or FirmwareEFI.
func (h Host) SetFirmware(ctx context.Context, vm, firmware string) error {
	if err := CheckName("VM", vm); err != nil {
		return err
	}
	if firmware != FirmwareBIOS && firmware != FirmwareEFI {
		return fmt.Errorf("vboxmanage: firmware %q is not %s or %s", firmware, FirmwareBIOS, FirmwareEFI)
	}
	_, err := h.run(ctx, "modifyvm", vm, "--firmware", firmware)
	return err
}

// CreateDisk makes a new, empty VDI disk of sizeMB megabytes at path,
// growing on the host as the guest writes to it.
func (h Host) CreateDisk(ctx context.Context, path string, sizeMB int) error {
	if err := CheckPath("disk", path); err != nil {
		return err
	}
	if sizeMB < 1 {
		return fmt.Errorf("vboxmanage: a disk of %d MB is not a disk", sizeMB)
	}
	_, err := h.run(ctx, "createmedium", "disk", "--filename", path, "--size", strconv.Itoa(sizeMB), "--format", "VDI", "--variant", "Standard")
	return err
}

// CopyDisk copies the disk image at from (VHDX, VHD, VMDK or VDI) to a new
// VDI at to, which then grows as the guest writes to it. The source is
// left as registered as it was found: VirtualBox registers a disk it
// reads, so one it did not know before is forgotten again.
func (h Host) CopyDisk(ctx context.Context, from, to string) error {
	if err := CheckPath("disk image", from); err != nil {
		return err
	}
	if err := CheckPath("disk", to); err != nil {
		return err
	}
	return h.withDisk(ctx, from, func() error {
		_, err := h.run(ctx, "clonemedium", "disk", from, to, "--format", "VDI", "--variant", "Standard")
		return err
	})
}

// AttachDisk puts the disk at path in vm's first SATA port.
func (h Host) AttachDisk(ctx context.Context, vm, path string) error {
	if err := CheckName("VM", vm); err != nil {
		return err
	}
	if err := CheckPath("disk", path); err != nil {
		return err
	}
	_, err := h.run(ctx, "storageattach", vm, "--storagectl", ControllerSATA, "--port", "0", "--device", "0", "--type", "hdd", "--medium", path)
	return err
}

// DeleteDisk forgets the disk at path and deletes its file. A disk a
// machine holds is refused.
func (h Host) DeleteDisk(ctx context.Context, path string) error {
	if err := CheckPath("disk", path); err != nil {
		return err
	}
	_, err := h.run(ctx, "closemedium", "disk", path, "--delete")
	return err
}

// Disks returns the path of every disk VirtualBox has registered.
func (h Host) Disks(ctx context.Context) ([]string, error) {
	out, err := h.run(ctx, "list", "hdds")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out.Stdout, "\n") {
		if path, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "Location:"); ok {
			paths = append(paths, strings.TrimSpace(path))
		}
	}
	return paths, nil
}

// withDisk runs use, which makes VirtualBox read the disk at path, and
// then forgets the disk again if VirtualBox did not know it before.
func (h Host) withDisk(ctx context.Context, path string, use func() error) error {
	known, err := h.Disks(ctx)
	if err != nil {
		return err
	}
	for _, p := range known {
		if strings.EqualFold(p, path) {
			return use()
		}
	}
	if err := use(); err != nil {
		return err
	}
	_, err = h.run(ctx, "closemedium", "disk", path)
	return err
}

// Partition table styles a disk can start with.
const (
	PartitionsGPT = "gpt"
	PartitionsMBR = "mbr"
)

// PartitionStyle reads the first two sectors of the disk image at path and
// returns PartitionsGPT when they hold a GUID partition table (an EFI
// machine's disk), PartitionsMBR when they hold only a master boot record
// (a BIOS machine's), and an error when they hold neither.
func (h Host) PartitionStyle(ctx context.Context, path string) (string, error) {
	if err := CheckPath("disk image", path); err != nil {
		return "", err
	}
	var dump string
	err := h.withDisk(ctx, path, func() error {
		out, err := h.run(ctx, "mediumio", "--disk="+path, "cat", "--hex", "--offset=0", "--size=1024")
		dump = out.Stdout
		return err
	})
	if err != nil {
		return "", err
	}
	start, err := ParseHexDump(dump)
	if err != nil {
		return "", err
	}
	return partitionStyle(path, start)
}

// partitionStyle decides a disk's partition table style from its first
// 1024 bytes: a boot signature at 510, and "EFI PART" at 512 on GPT.
func partitionStyle(path string, start []byte) (string, error) {
	if len(start) < 520 || start[510] != 0x55 || start[511] != 0xAA {
		return "", fmt.Errorf("vboxmanage: %s starts with no partition table; say which firmware its machine boots with", path)
	}
	if string(start[512:520]) == "EFI PART" {
		return PartitionsGPT, nil
	}
	return PartitionsMBR, nil
}

// hexDumpLine is one line of mediumio cat --hex: an offset, a colon, and up
// to sixteen bytes, the eighth and ninth joined by a dash, then the bytes
// as text.
var hexDumpLine = regexp.MustCompile(`^([0-9a-f]{12}): ((?:[0-9a-f]{2}[ -]){0,15}[0-9a-f]{2})`)

// dittoLine is how mediumio cat --hex writes a run of lines the same as
// the one before: "**********  <ditto x 27>" stands for 27 more of it.
var dittoLine = regexp.MustCompile(`^\*+\s+<ditto x ([0-9]+)>$`)

// ParseHexDump reads the bytes mediumio cat --hex writes, from the first
// line's offset on, refusing a line that is not one or bytes that do not
// follow on from the last line's.
func ParseHexDump(text string) ([]byte, error) {
	var data, last []byte
	base := int64(-1)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if d := dittoLine.FindStringSubmatch(line); d != nil {
			n, err := strconv.Atoi(d[1])
			if err != nil || len(last) != 16 || n > 1<<16 {
				return nil, fmt.Errorf("vboxmanage: mediumio repeats a line it cannot: %q", line)
			}
			data = append(data, bytes.Repeat(last, n)...)
			continue
		}
		m := hexDumpLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("vboxmanage: mediumio gave a line that is not a hex dump: %q", line)
		}
		offset, _ := strconv.ParseInt(m[1], 16, 64)
		if base < 0 {
			base = offset
		}
		if offset != base+int64(len(data)) {
			return nil, fmt.Errorf("vboxmanage: mediumio's dump jumps to offset %d after %d bytes from %d", offset, len(data), base)
		}
		row, err := hex.DecodeString(strings.NewReplacer(" ", "", "-", "").Replace(m[2]))
		if err != nil {
			return nil, fmt.Errorf("vboxmanage: mediumio gave bytes that are not hex: %q", line)
		}
		data, last = append(data, row...), row
	}
	return data, nil
}

// Screenshot writes a PNG of vm's screen to path on the host. The machine
// must be running.
func (h Host) Screenshot(ctx context.Context, vm, path string) error {
	if err := CheckName("VM", vm); err != nil {
		return err
	}
	if err := CheckPath("screenshot", path); err != nil {
		return err
	}
	_, err := h.run(ctx, "controlvm", vm, "screenshotpng", path)
	return err
}

// TimeoutRunner is a Runner whose calls can be given a different bound,
// for the one call that takes longer than every other, such as copying a
// disk of many gigabytes.
type TimeoutRunner interface {
	Runner
	// WithTimeout returns the runner with each call bounded by timeout.
	WithTimeout(timeout time.Duration) Runner
}

// WithTimeout returns h with each call bounded by timeout, when its
// runner can be; otherwise h as it is.
func (h Host) WithTimeout(timeout time.Duration) Host {
	if t, ok := h.Runner.(TimeoutRunner); ok {
		h.Runner = t.WithTimeout(timeout)
	}
	return h
}
