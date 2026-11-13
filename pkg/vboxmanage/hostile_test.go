// Hostile values at every boundary pkg/vboxmanage owns, where text from a
// runbook or from the host reaches something that reads it as syntax:
// VBoxManage's command line (through winrmexec.CommandLine, then the
// cmd.exe the WinRM service puts in front of it) and the PowerShell scripts
// the file and folder calls send. Each value holds a quote, a backtick,
// "$(", ";", a line break, a NUL, U+FFFE, a ".." segment, a UNC path or a
// drive. Each is refused before anything reaches the host, or reaches it as
// exactly one argument, unchanged, in the position its verb reads it, or
// inside a PowerShell single-quoted literal as winrmexec.QuotePS writes it.
// That the command line round-trips through the Windows argument parser and
// cmd.exe is winrmexec's own proof (TestCommandLine_RoundTripsThroughTheWindowsParser,
// FuzzTransparentLine); this file proves every value reaches that line, or
// a script, only in the form those proofs cover.
package vboxmanage

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// hostileText is every hostile value, bare.
var hostileText = []string{
	`a'b`, "a\u2019b", "a\"b", "a`b", "a$(calc)b", "a;calc", "a|b&c", "a%PATH%b", "a!b!c", "a^b",
	"a\nb", "a\r\nb", "a\x00b", "a\uFFFEb", "a\xffb",
	"..", `a\..\b`, "a/../b", `\\server\share`, `C:\`, `C:\Windows`, "-a", "",
}

// hostilePaths is every hostile value as a host path a runbook could name
// or a host could report.
var hostilePaths = []string{
	`G:\Lab\a'b.iso`, `G:\Lab\a"b.iso`, "G:\\Lab\\a\nb.iso", "G:\\Lab\\a\x00b.iso", "G:\\Lab\\a\uFFFEb.iso", "G:\\Lab\\a\xffb.iso",
	`G:\Lab\..\Windows\x.iso`, `G:\Lab\..`, `G:\Lab/..`, `G:\Lab/../Windows/x.iso`, `G:\Lab\./x.iso`, `G:\Lab\...\x.iso`, `G:\Lab\.. \x.iso`,
	`G:\Lab\\x.iso`, `\\server\share\x.iso`, `\\?\G:\Lab\x.iso`, `x.iso`, `C:`, `C:\`, `G:\Lab\*.iso`,
}

// carriedPaths are paths holding PowerShell and cmd.exe syntax that are
// still paths a host can have, so they are carried rather than refused.
var carriedPaths = []string{
	"G:\\Lab\\a`b.iso", `G:\Lab\a$(calc)b.iso`, `G:\Lab\a;calc.iso`, `G:\Lab\a&b.iso`, `G:\Lab\a%PATH%b.iso`, `G:\Lab\a!b!c.iso`, "G:\\Lab\\a\u2019;calc;\u2019.iso",
}

// TestHostile_NamesNeverReachTheHost: every VM and snapshot name, UUID,
// adapter and OS type a call takes is refused before the runner hears of
// it, for every hostile value.
func TestHostile_NamesNeverReachTheHost(t *testing.T) {
	ctx := context.Background()
	h := Host{Runner: never{t}, Path: vbox}
	uuid := "5732a952-0b85-4e3a-820a-1a568f31166b"
	slot := Slot{Controller: ControllerIDE}
	hw := Hardware{MemoryMB: 1024, CPUs: 1, HostOnlyAdapter: "VirtualBox Host-Only Ethernet Adapter", ConsoleLog: `G:\Lab\vm\console.log`}
	for _, v := range hostileText {
		_, machineErr := h.Machine(ctx, v)
		_, takeErr := h.TakeSnapshot(ctx, "lab", v, "")
		_, takeVMErr := h.TakeSnapshot(ctx, v, "snap", "")
		_, leasesErr := h.DHCPLeases(ctx, v)
		badHW := hw
		badHW.HostOnlyAdapter = v
		for call, err := range map[string]error{
			"Machine":                 machineErr,
			"Start":                   h.Start(ctx, v),
			"PowerOff":                h.PowerOff(ctx, v),
			"ACPIShutdown":            h.ACPIShutdown(ctx, v),
			"SaveState":               h.SaveState(ctx, v),
			"DiscardState":            h.DiscardState(ctx, v),
			"SetAutostart":            h.SetAutostart(ctx, v, false),
			"Configure":               h.Configure(ctx, v, hw),
			"Configure adapter":       h.Configure(ctx, "lab", badHW),
			"Resize":                  h.Resize(ctx, v, 1024, 1),
			"CloneLinked from":        h.CloneLinked(ctx, v, uuid, "lab", ""),
			"CloneLinked name":        h.CloneLinked(ctx, "base", uuid, v, ""),
			"CloneLinked snapshot":    h.CloneLinked(ctx, "base", v, "lab", ""),
			"TakeSnapshot name":       takeErr,
			"TakeSnapshot vm":         takeVMErr,
			"RestoreSnapshot vm":      h.RestoreSnapshot(ctx, v, uuid),
			"RestoreSnapshot uuid":    h.RestoreSnapshot(ctx, "lab", v),
			"DeleteSnapshot uuid":     h.DeleteSnapshot(ctx, "lab", v),
			"AttachDVD vm":            h.AttachDVD(ctx, v, slot, "emptydrive"),
			"AttachDVD controller":    h.AttachDVD(ctx, "lab", Slot{Controller: v}, "emptydrive"),
			"EjectDVD":                h.EjectDVD(ctx, v, slot),
			"Detach":                  h.Detach(ctx, v, slot),
			"Unregister":              h.Unregister(ctx, v),
			"Forget":                  h.Forget(ctx, v),
			"SetExtraData vm":         h.SetExtraData(ctx, v, ExtraAddress, "192.168.56.10"),
			"Type":                    h.Type(ctx, v, []Keystroke{{Text: "x"}}),
			"Import":                  h.Import(ctx, `G:\x.ova`, v, ""),
			"CreateVM name":           h.CreateVM(ctx, NewMachine{Name: v, OSType: "Ubuntu_64", Firmware: FirmwareBIOS, MemoryMB: 1024, CPUs: 1}),
			"CreateVM ostype":         h.CreateVM(ctx, NewMachine{Name: "lab", OSType: v, Firmware: FirmwareBIOS, MemoryMB: 1024, CPUs: 1}),
			"SetFirmware":             h.SetFirmware(ctx, v, FirmwareBIOS),
			"SetConsoleLog":           h.SetConsoleLog(ctx, v, `G:\Lab\vm\console.log`),
			"AttachDiskAt vm":         h.AttachDiskAt(ctx, v, Slot{Controller: ControllerSATA}, `G:\Lab\vm\disk.vdi`),
			"AttachDiskAt controller": h.AttachDiskAt(ctx, "lab", Slot{Controller: v}, `G:\Lab\vm\disk.vdi`),
			"Screenshot":              h.Screenshot(ctx, v, `G:\Lab\vm\screen.png`),
			"CloseDisk":               h.CloseDisk(ctx, v, true),
			"DHCPLeases":              leasesErr,
		} {
			if err == nil {
				t.Errorf("%s accepted %q", call, v)
			}
		}
		// An extradata key is an opaque string to VirtualBox, never a path, so
		// "a/../b" is a key like any other; every key holding syntax is refused.
		if v != "a/../b" {
			if err := h.SetExtraData(ctx, "lab", v, "x"); err == nil {
				t.Errorf("SetExtraData accepted the key %q", v)
			}
		}
	}
}

// TestHostile_PathsNeverReachTheHost: every host path a call takes is
// refused before the runner hears of it, for every hostile path: one
// climbing out through ".." between either separator, one no WS-Man
// message can carry, one from no drive's root.
func TestHostile_PathsNeverReachTheHost(t *testing.T) {
	ctx := context.Background()
	h := Host{Runner: never{t}, Path: vbox}
	for _, p := range hostilePaths {
		_, readErr := h.ReadTail(ctx, p, 8)
		_, foldersErr := h.MachineFolders(ctx, p)
		_, linksErr := h.Links(ctx, p)
		_, styleErr := h.PartitionStyle(ctx, p)
		for call, err := range map[string]error{
			"CheckPath":        CheckPath("path", p),
			"Upload":           h.Upload(ctx, p, []byte("x")),
			"ReadTail":         readErr,
			"Remove":           h.Remove(ctx, p, true),
			"RemoveFolder":     h.RemoveFolder(ctx, p),
			"MachineFolders":   foldersErr,
			"Links":            linksErr,
			"Import ova":       h.Import(ctx, p, "lab", ""),
			"Import folder":    h.Import(ctx, `G:\x.ova`, "lab", p),
			"CloneLinked":      h.CloneLinked(ctx, "base", "5732a952-0b85-4e3a-820a-1a568f31166b", "lab", p),
			"AttachDVD":        h.AttachDVD(ctx, "lab", Slot{Controller: ControllerIDE}, p),
			"CloseDVD":         h.CloseDVD(ctx, p, true),
			"CreateDisk":       h.CreateDisk(ctx, p, 1024),
			"CopyDisk from":    h.CopyDisk(ctx, p, `G:\Lab\vm\disk.vdi`),
			"CopyDisk to":      h.CopyDisk(ctx, `G:\iso\disk.vhdx`, p),
			"AttachDiskAt":     h.AttachDiskAt(ctx, "lab", Slot{Controller: ControllerSATA}, p),
			"DeleteDisk":       h.DeleteDisk(ctx, p),
			"PartitionStyle":   styleErr,
			"Screenshot":       h.Screenshot(ctx, "lab", p),
			"SetConsoleLog":    h.SetConsoleLog(ctx, "lab", p),
			"Configure":        h.Configure(ctx, "lab", Hardware{MemoryMB: 1024, CPUs: 1, HostOnlyAdapter: "VirtualBox Host-Only Ethernet Adapter", ConsoleLog: p}),
			"CreateVM folder":  h.CreateVM(ctx, NewMachine{Name: "lab", OSType: "Ubuntu_64", Folder: p, Firmware: FirmwareBIOS, MemoryMB: 1024, CPUs: 1}),
			"CreateVM console": h.CreateVM(ctx, NewMachine{Name: "lab", OSType: "Ubuntu_64", Firmware: FirmwareBIOS, MemoryMB: 1024, CPUs: 1, ConsoleLog: p}),
		} {
			if err == nil {
				t.Errorf("%s accepted %q", call, p)
			}
		}
	}
}

// TestHostile_CarriedPathsStayOneLiteral: a path holding PowerShell or
// cmd.exe syntax a host path may hold (a backtick, "$(", ";", "&", "%", "!",
// a typographic quote) reaches each PowerShell script only as the single
// assignment QuotePS writes, where none of it is syntax, and reaches
// VBoxManage as one argument, unchanged, on a line winrmexec builds.
func TestHostile_CarriedPathsStayOneLiteral(t *testing.T) {
	ctx := context.Background()
	for _, p := range carriedPaths {
		if err := CheckPath("path", p); err != nil {
			t.Fatalf("%q is refused, so this test proves nothing about it: %v", p, err)
		}
		r := &scripts{}
		h := Host{Runner: r, Path: vbox}
		_ = h.Upload(ctx, p, []byte("x"))
		_, _ = h.ReadTail(ctx, p, 8)
		_ = h.Remove(ctx, p, true)
		_ = h.RemoveFolder(ctx, p)
		_, _ = h.MachineFolders(ctx, p)
		_, _ = h.Links(ctx, p)
		if len(r.got) != 6 {
			t.Fatalf("%q: ran %d scripts, want 6", p, len(r.got))
		}
		want := "\n$path = " + winrmexec.QuotePS(p) + "\n"
		for _, script := range r.got {
			if !strings.Contains(script, want) || strings.Count(script, winrmexec.QuotePS(p)) != 1 || strings.Count(script, p) > 1 {
				t.Errorf("%q: script %q does not carry the path once, as QuotePS writes it:\n%s", p, strings.SplitN(script, "\n", 2)[0], script)
			}
		}
		rec := &recorded{t: t}
		if err := (Host{Runner: rec, Path: vbox}).CloseDVD(ctx, p, false); err != nil {
			t.Fatal(err)
		}
		if got := rec.calls[0]; len(got) != 4 || got[3] != p {
			t.Errorf("%q: VBoxManage was sent %q, want the path as its one argument", p, got)
		}
		if _, err := winrmexec.CommandLine(vbox, rec.calls[0][1:]...); err != nil {
			t.Errorf("%q: no command line carries it: %v", p, err)
		}
	}
}

// TestHostile_FreeTextTravelsAsOneArgument: the free text a verb takes (a
// snapshot's description, an extradata value, typed keystrokes) is refused
// when no command line can carry it (a line break, a NUL, U+FFFE, invalid
// UTF-8), and otherwise reaches VBoxManage as exactly one argument,
// unchanged, in the position its handler reads, quotes and shell syntax
// included where the verb allows them.
func TestHostile_FreeTextTravelsAsOneArgument(t *testing.T) {
	ctx := context.Background()
	refuser := Host{Runner: never{t}, Path: vbox}
	for _, v := range hostileText {
		carriable := winrmexec.CheckText("v", v) == nil && !strings.ContainsAny(v, "\r\n")
		descErr := CheckDescription(v)
		if !carriable && descErr == nil {
			t.Errorf("a description %q no command line can carry is accepted", v)
		}
		if !carriable {
			if _, err := refuser.TakeSnapshot(ctx, "lab", "snap", v); err == nil {
				t.Errorf("TakeSnapshot accepted a description %q no command line can carry", v)
			}
		}
		if err := checkExtra(ExtraAddress, v); !carriable && err == nil {
			t.Errorf("an extradata value %q no command line can carry is accepted", v)
		}
		for verb, args := range map[string]func(*Host) error{
			"description": func(h *Host) error {
				if v == "" || descErr != nil {
					return descErr
				}
				_, err := h.TakeSnapshot(ctx, "lab", "snap", v)
				return err
			},
			"extradata": func(h *Host) error { return h.SetExtraData(ctx, "lab", ExtraAddress, v) },
			"keys":      func(h *Host) error { return h.Type(ctx, "lab", []Keystroke{{Text: v}}) },
		} {
			rec := &recorded{t: t}
			h := Host{Runner: carrier{rec}, Path: vbox}
			err := args(&h)
			if err != nil || v == "" {
				continue
			}
			if len(rec.calls) != 1 {
				t.Fatalf("%s %q: %d calls", verb, v, len(rec.calls))
			}
			call := rec.calls[0]
			if !containsExactly(call, v) {
				t.Errorf("%s %q reached VBoxManage as %q, not as one argument", verb, v, call)
			}
			if !carriable {
				t.Errorf("%s %q reached the runner, though no command line carries it", verb, v)
			}
		}
	}
}

// carrier is a runner that, as WinRMRunner does, builds the command line
// before running anything, so a value no line can carry fails there; what
// it does run, it records.
type carrier struct{ rec *recorded }

// Run builds the line, then records the call.
func (c carrier) Run(ctx context.Context, program string, args []string) (Output, error) {
	if _, err := winrmexec.CommandLine(program, args...); err != nil {
		return Output{}, err
	}
	out, err := c.rec.Run(ctx, program, args)
	if len(args) > 1 && args[0] == "snapshot" {
		out.Stdout = "Snapshot taken. UUID: 5732a952-0b85-4e3a-820a-1a568f31166b\n"
	}
	return out, err
}

// PowerShell records the script.
func (c carrier) PowerShell(ctx context.Context, script, stdin string) (Output, error) {
	return c.rec.PowerShell(ctx, script, stdin)
}

// containsExactly reports whether one element of call is v.
func containsExactly(call []string, v string) bool {
	for _, a := range call {
		if a == v {
			return true
		}
	}
	return false
}

// TestHostile_AListSplitByANameIsRefused: VBoxManage writes a VM's name as
// it is, so a name holding a line break splits list vms across lines; the
// list is refused as a whole rather than a fragment being taken for a VM
// and sent back to the host as a name.
func TestHostile_AListSplitByANameIsRefused(t *testing.T) {
	h := Host{Runner: answering{Output{Stdout: "\"lab\" {5732a952-0b85-4e3a-820a-1a568f31166b}\r\n\"a\r\n\" {6732a952-0b85-4e3a-820a-1a568f31166b}\r\n"}}, Path: vbox}
	if refs, err := h.List(context.Background()); err == nil {
		t.Errorf("a list split by a name was read: %+v", refs)
	}
}
