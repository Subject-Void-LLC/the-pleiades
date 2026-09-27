// Tests for making a machine from nothing (create.go), against the model
// host in vboxmanagetest, and for reading mediumio's hex dumps as the real
// host wrote them (testdata/mediumio-cat-*.stdout).
package vboxmanage_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

const vhdx = `G:\iso\server-2025.vhdx`

// windowsMachine is the machine a Windows base is made as.
func windowsMachine() vboxmanage.NewMachine {
	return vboxmanage.NewMachine{Name: "ws2025-base", OSType: "Windows2025_64", Folder: `G:\PleiadesLab`,
		Firmware: vboxmanage.FirmwareBIOS, MemoryMB: 2048, CPUs: 1, ConsoleLog: `G:\PleiadesLab\ws2025-base\console.log`}
}

// diskStart is the first 1024 bytes of a disk: a boot signature, and a
// GPT header's signature after it when gpt is set.
func diskStart(gpt bool) []byte {
	b := make([]byte, 1024)
	b[510], b[511] = 0x55, 0xAA
	if gpt {
		copy(b[512:], "EFI PART")
	}
	return b
}

func TestCreateVMGivesAWindowsGuestWhatItBootsWith(t *testing.T) {
	ctx := context.Background()
	h := vboxmanagetest.New()
	host := h.VBoxHost()
	if err := host.CreateVM(ctx, windowsMachine()); err != nil {
		t.Fatal(err)
	}
	m, err := host.Machine(ctx, "ws2025-base")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Windows() || m.OSType != "Windows Server 2025 (64-bit)" || m.Firmware != "BIOS" || m.MemoryMB != 2048 || m.CPUs != 1 {
		t.Errorf("made %+v", m)
	}
	if m.ConsoleLog != `G:\PleiadesLab\ws2025-base\console.log` {
		t.Errorf("console log %q", m.ConsoleLog)
	}
	if slot, ok := m.FreeIDESlot(); !ok || slot.Controller != vboxmanage.ControllerIDE {
		t.Errorf("no free IDE slot for a seed: %+v", m.Slots)
	}
	if err := host.SetFirmware(ctx, "ws2025-base", vboxmanage.FirmwareEFI); err != nil {
		t.Fatal(err)
	}
	if m, _ = host.Machine(ctx, "ws2025-base"); m.Firmware != "EFI" {
		t.Errorf("firmware %q after setting efi", m.Firmware)
	}
	calls := strings.Join(h.Calls(), "\n")
	for _, want := range []string{"--platform-architecture x86 --ostype Windows2025_64 --register --basefolder G:\\PleiadesLab",
		"--nic-type1 82540EM", "--boot1 disk --boot2 dvd", "--x86-long-mode on", "--add sata --controller IntelAhci", "--add ide --controller PIIX4"} {
		if !strings.Contains(calls, want) {
			t.Errorf("no call carried %q:\n%s", want, calls)
		}
	}
	if err := host.CreateVM(ctx, windowsMachine()); err == nil {
		t.Error("made a machine over one of the same name")
	}
}

func TestCreateVMWithoutAConsoleOrAFolder(t *testing.T) {
	ctx := context.Background()
	h := vboxmanagetest.New()
	m := windowsMachine()
	m.ConsoleLog, m.Folder = "", ""
	if err := h.VBoxHost().CreateVM(ctx, m); err != nil {
		t.Fatal(err)
	}
	for _, call := range h.Calls() {
		if strings.Contains(call, "--uart") || strings.Contains(call, "--basefolder") {
			t.Errorf("sent %q", call)
		}
	}
}

func TestCreateVMRefusesWhatItWouldNotSend(t *testing.T) {
	for name, change := range map[string]func(*vboxmanage.NewMachine){
		"a bad name":          func(m *vboxmanage.NewMachine) { m.Name = "a b" },
		"a bad OS type":       func(m *vboxmanage.NewMachine) { m.OSType = "Windows 2025" },
		"a relative folder":   func(m *vboxmanage.NewMachine) { m.Folder = `PleiadesLab` },
		"an unknown firmware": func(m *vboxmanage.NewMachine) { m.Firmware = "coreboot" },
		"no memory":           func(m *vboxmanage.NewMachine) { m.MemoryMB = 0 },
		"no CPUs":             func(m *vboxmanage.NewMachine) { m.CPUs = 0 },
		"a quoted console":    func(m *vboxmanage.NewMachine) { m.ConsoleLog = `G:\a"b.log` },
	} {
		h := vboxmanagetest.New()
		m := windowsMachine()
		change(&m)
		if err := h.VBoxHost().CreateVM(context.Background(), m); err == nil {
			t.Errorf("%s: made it", name)
		}
		if len(h.Calls()) != 0 {
			t.Errorf("%s: sent %v", name, h.Calls())
		}
	}
}

func TestCreateVMStopsAtTheFirstRefusal(t *testing.T) {
	for _, key := range []string{"VBoxManage.exe createvm", "VBoxManage.exe modifyvm", "VBoxManage.exe storagectl ws2025-base --name SATA", "VBoxManage.exe storagectl ws2025-base --name IDE"} {
		h := vboxmanagetest.New()
		h.Fail = map[string]vboxmanage.Output{key: {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}}
		if err := h.VBoxHost().CreateVM(context.Background(), windowsMachine()); err == nil || !strings.Contains(err.Error(), "refused") {
			t.Errorf("%s refused: %v", key, err)
		}
	}
}

func TestDisksMadeCopiedAndAttached(t *testing.T) {
	ctx := context.Background()
	h := vboxmanagetest.New()
	h.SetFile(vhdx, diskStart(true))
	host := h.VBoxHost()
	if err := host.CreateVM(ctx, windowsMachine()); err != nil {
		t.Fatal(err)
	}
	copied := `G:\PleiadesLab\ws2025-base\ws2025-base.vdi`
	if err := host.CopyDisk(ctx, vhdx, copied); err != nil {
		t.Fatal(err)
	}
	disks, err := host.Disks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The source was unknown before, so it is forgotten again; the copy
	// stays registered.
	if slices.Contains(disks, vhdx) || !slices.Contains(disks, copied) {
		t.Errorf("disks after the copy: %v", disks)
	}
	if err := host.AttachDisk(ctx, "ws2025-base", copied); err != nil {
		t.Fatal(err)
	}
	m, _ := host.Machine(ctx, "ws2025-base")
	if m.Slots[0].Controller != vboxmanage.ControllerSATA && !slices.ContainsFunc(m.Slots, func(s vboxmanage.Slot) bool { return s.Medium == copied }) {
		t.Errorf("slots %+v", m.Slots)
	}
	made := `G:\PleiadesLab\ws2025-base\new.vdi`
	if err := host.CreateDisk(ctx, made, 65536); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(h.Calls(), "\n"), "createmedium disk --filename "+made+" --size 65536 --format VDI --variant Standard") {
		t.Errorf("calls %v", h.Calls())
	}
}

func TestCopyDiskLeavesAKnownSourceRegistered(t *testing.T) {
	ctx := context.Background()
	h := vboxmanagetest.New()
	h.SetFile(vhdx, diskStart(true))
	host := h.VBoxHost()
	// A read registers it, as VirtualBox does, before the copy is asked.
	if _, err := host.PartitionStyle(ctx, vhdx); err != nil {
		t.Fatal(err)
	}
	h.Fail = nil
	if err := host.CopyDisk(ctx, vhdx, `G:\PleiadesLab\a.vdi`); err != nil {
		t.Fatal(err)
	}
	// PartitionStyle forgot it again, so the copy found it unknown too.
	if disks, _ := host.Disks(ctx); slices.Contains(disks, vhdx) {
		t.Errorf("the source stayed registered: %v", disks)
	}
	if err := host.CopyDisk(ctx, `G:\PleiadesLab\a.vdi`, `G:\PleiadesLab\b.vdi`); err != nil {
		t.Fatal(err)
	}
	if disks, _ := host.Disks(ctx); !slices.Contains(disks, `G:\PleiadesLab\a.vdi`) {
		t.Errorf("a disk registered before the copy was forgotten: %v", disks)
	}
}

func TestDiskVerbsRefuse(t *testing.T) {
	ctx := context.Background()
	h := vboxmanagetest.New()
	host := h.VBoxHost()
	for name, err := range map[string]error{
		"a copy from a relative path": host.CopyDisk(ctx, `a.vhdx`, `G:\b.vdi`),
		"a copy to a relative path":   host.CopyDisk(ctx, vhdx, `b.vdi`),
		"a copy from nothing":         host.CopyDisk(ctx, vhdx, `G:\b.vdi`),
		"a disk of no size":           host.CreateDisk(ctx, `G:\b.vdi`, 0),
		"a disk at a quoted path":     host.CreateDisk(ctx, `G:\"b.vdi`, 1),
		"an attach to a bad name":     host.AttachDisk(ctx, "a b", `G:\b.vdi`),
		"an attach of a bad path":     host.AttachDisk(ctx, "vm", `b.vdi`),
		"a firmware for a bad name":   host.SetFirmware(ctx, "a b", vboxmanage.FirmwareEFI),
		"an unknown firmware":         host.SetFirmware(ctx, "vm", "coreboot"),
		"a screenshot of a bad name":  host.Screenshot(ctx, "a b", `G:\s.png`),
		"a screenshot to a bad path":  host.Screenshot(ctx, "vm", `s.png`),
		"a screenshot of no machine":  host.Screenshot(ctx, "vm", `G:\s.png`),
	} {
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	h.Fail = map[string]vboxmanage.Output{"VBoxManage.exe list hdds": {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}}
	if err := host.CopyDisk(ctx, vhdx, `G:\b.vdi`); err == nil {
		t.Error("copied with no list of disks")
	}
	if _, err := host.PartitionStyle(ctx, vhdx); err == nil {
		t.Error("read a disk with no list of disks")
	}
}

func TestPartitionStyle(t *testing.T) {
	ctx := context.Background()
	for name, tt := range map[string]struct {
		data []byte
		want string
	}{
		"a GUID partition table": {diskStart(true), vboxmanage.PartitionsGPT},
		"a master boot record":   {diskStart(false), vboxmanage.PartitionsMBR},
		"nothing":                {make([]byte, 1024), ""},
		"a short disk":           {diskStart(true)[:515], ""},
	} {
		h := vboxmanagetest.New()
		h.SetFile(vhdx, tt.data)
		got, err := h.VBoxHost().PartitionStyle(ctx, vhdx)
		if got != tt.want || (err != nil) != (tt.want == "") {
			t.Errorf("%s: %q, %v", name, got, err)
		}
	}
	if _, err := vboxmanagetest.New().VBoxHost().PartitionStyle(ctx, `a.vhdx`); err == nil {
		t.Error("read a relative path")
	}
	h := vboxmanagetest.New()
	h.SetFile(vhdx, diskStart(true))
	h.Fail = map[string]vboxmanage.Output{"VBoxManage.exe mediumio": {Stdout: "not a dump\r\n"}}
	if _, err := h.VBoxHost().PartitionStyle(ctx, vhdx); err == nil {
		t.Error("read a dump that is not one")
	}
}

// captured reads a file in testdata.
func captured(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseHexDumpReadsTheHostsDumps(t *testing.T) {
	mbr, err := vboxmanage.ParseHexDump(captured(t, "mediumio-cat-mbr.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	// The protective MBR's one entry is type 0xEE, and the sector ends in
	// its boot signature.
	if len(mbr) != 66 || mbr[4] != 0xEE || !bytes.Equal(mbr[64:], []byte{0x55, 0xAA}) {
		t.Errorf("read % x", mbr)
	}
	// The whole of the VHDX's first 1024 bytes, with two runs written as
	// ditto lines.
	whole, err := vboxmanage.ParseHexDump(captured(t, "mediumio-cat-gpt.stdout"))
	if err != nil || len(whole) != 1024 || string(whole[512:520]) != "EFI PART" || whole[0x1c2] != 0xEE || whole[0x3ff] != 0 {
		t.Errorf("read %d bytes, %v", len(whole), err)
	}
	header, err := vboxmanage.ParseHexDump(captured(t, "mediumio-cat-gpt-header.stdout"))
	if err != nil || string(header[:8]) != "EFI PART" {
		t.Errorf("read %q, %v", header, err)
	}
	for name, text := range map[string]string{
		"a line that is not one": "000000000000 00 11\r\n",
		"a gap":                  "000000000000: 00 11\r\n000000000010: 22\r\n",
		"a ditto first":          "**********  <ditto x 2>\r\n",
		"a ditto of a short row": "000000000000: 00 11\r\n**********  <ditto x 2>\r\n",
	} {
		if _, err := vboxmanage.ParseHexDump(text); err == nil {
			t.Errorf("%s: read", name)
		}
	}
}

func TestTheModelDumpsAsTheHostDoes(t *testing.T) {
	h := vboxmanagetest.New()
	disk := make([]byte, 1024)
	copy(disk[446:], []byte{0x00, 0x00, 0x02, 0x00, 0xee, 0xfe, 0x3f, 0xa1, 0x01, 0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0xff})
	disk[510], disk[511] = 0x55, 0xAA
	h.SetFile(vhdx, disk)
	out, err := h.Run(context.Background(), vboxmanagetest.Path, []string{"mediumio", "--disk=" + vhdx, "cat", "--hex", "--offset=446", "--size=66"})
	if err != nil {
		t.Fatal(err)
	}
	if want := captured(t, "mediumio-cat-mbr.stdout"); out.Stdout != want {
		t.Errorf("the model wrote\n%q\nthe host wrote\n%q", out.Stdout, want)
	}
}

func TestTheModelFoldsARunAsTheHostDoes(t *testing.T) {
	disk, err := vboxmanage.ParseHexDump(captured(t, "mediumio-cat-gpt.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	h := vboxmanagetest.New()
	h.SetFile(vhdx, disk)
	for _, tt := range []struct{ offset, size int }{{0, 1024}, {608, 352}, {768, 112}} {
		out, err := h.Run(context.Background(), vboxmanagetest.Path, []string{"mediumio", "--disk=" + vhdx, "cat", "--hex", fmt.Sprintf("--offset=%d", tt.offset), fmt.Sprintf("--size=%d", tt.size)})
		if err != nil {
			t.Fatal(err)
		}
		folded := strings.Contains(out.Stdout, "ditto")
		// The host folded the 1024-byte dump and wrote the others out.
		if folded != (tt.size == 1024) {
			t.Errorf("offset %d size %d: folded %v:\n%s", tt.offset, tt.size, folded, out.Stdout)
		}
		if tt.size == 1024 && out.Stdout != captured(t, "mediumio-cat-gpt.stdout") {
			t.Errorf("the model wrote\n%s\nthe host wrote\n%s", out.Stdout, captured(t, "mediumio-cat-gpt.stdout"))
		}
		if back, err := vboxmanage.ParseHexDump(out.Stdout); err != nil || !bytes.Equal(back, disk[tt.offset:tt.offset+tt.size]) {
			t.Errorf("offset %d size %d did not read back: %v", tt.offset, tt.size, err)
		}
	}
}

func TestScreenshot(t *testing.T) {
	ctx := context.Background()
	h := vboxmanagetest.New(&vboxmanagetest.VM{Name: "vm", UUID: "00000000-0000-4000-8000-000000000099", State: vboxmanage.StateRunning})
	if err := h.VBoxHost().Screenshot(ctx, "vm", `G:\s.png`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(h.Calls(), "\n"), `controlvm vm screenshotpng G:\s.png`) {
		t.Errorf("calls %v", h.Calls())
	}
}

func TestWithTimeout(t *testing.T) {
	winrm := vboxmanage.Host{Runner: vboxmanage.WinRMRunner{Options: winrmexec.Options{Timeout: time.Minute}}, Path: vboxmanagetest.Path}
	if got := winrm.WithTimeout(time.Hour).Runner.(vboxmanage.WinRMRunner).Options.Timeout; got != time.Hour {
		t.Errorf("timeout %s", got)
	}
	// The original keeps its own.
	if got := winrm.Runner.(vboxmanage.WinRMRunner).Options.Timeout; got != time.Minute {
		t.Errorf("the original's timeout became %s", got)
	}
	model := vboxmanagetest.New().VBoxHost()
	if model.WithTimeout(time.Hour).Runner != model.Runner {
		t.Error("a runner with no timeout of its own was replaced")
	}
}

func TestDeleteDiskAndEjectDVD(t *testing.T) {
	ctx := context.Background()
	h := vboxmanagetest.New(&vboxmanagetest.VM{Name: "win", UUID: "00000000-0000-4000-8000-000000000010", State: vboxmanage.StateRunning,
		Slots: []vboxmanage.Slot{{Controller: "SATA", ControllerType: "IntelAhci", Port: 1, Medium: `G:\PleiadesLab\win\seed.iso`}}})
	h.SetFile(`G:\PleiadesLab\win\seed.iso`, []byte("seed"))
	h.SetFile(`G:\PleiadesLab\loose.vdi`, []byte("disk"))
	host := h.VBoxHost()
	m, _ := host.Machine(ctx, "win")
	if slot, ok := m.FreeSATASlot(); ok {
		t.Errorf("a SATA slot holding a DVD was free: %+v", slot)
	}
	if err := host.EjectDVD(ctx, "win", m.Slots[0]); err != nil {
		t.Fatal(err)
	}
	if m, _ = host.Machine(ctx, "win"); m.Slots[0].Medium != "emptydrive" {
		t.Errorf("after the eject: %+v", m.Slots[0])
	}
	if !strings.Contains(strings.Join(h.Calls(), "\n"), "--type dvddrive --medium emptydrive --forceunmount") {
		t.Errorf("calls %v", h.Calls())
	}
	if err := host.DeleteDisk(ctx, `G:\PleiadesLab\loose.vdi`); err != nil {
		t.Fatal(err)
	}
	if disks, _ := host.Disks(ctx); slices.Contains(disks, `G:\PleiadesLab\loose.vdi`) {
		t.Errorf("a deleted disk is still registered: %v", disks)
	}
	for name, err := range map[string]error{
		"an eject from a bad name":       host.EjectDVD(ctx, "a b", m.Slots[0]),
		"an eject from a bad controller": host.EjectDVD(ctx, "win", vboxmanage.Slot{Controller: "a\"b"}),
		"a delete of a bad path":         host.DeleteDisk(ctx, "loose.vdi"),
	} {
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := vboxmanage.ParseHexDump("000000000000: 00 11 22 33 44 55 66 77-88 99 aa bb cc dd ee ff\r\n**********  <ditto x 99999999>\r\n"); err == nil {
		t.Error("a ditto past any dump mediumio writes was read")
	}
}
