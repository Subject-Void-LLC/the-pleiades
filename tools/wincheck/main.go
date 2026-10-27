// Command wincheck runs a branch's container-free packages on a real
// Windows machine, before a push, for what Linux and macOS cannot show:
// Windows paths, files that cannot be removed while open, a missing POSIX
// tool. CI's ci job only builds and vets for Windows, and runs no tests
// there.
//
// The machine is a device of a Pleiades project that already manages it,
// as the VirtualBox lab's win-check is (examples/virtualbox_lab):
// PLEIADES_WINRM_PROJECT names the project's directory and
// PLEIADES_WINRM_DEVICE the device in it, the same two settings the WinRM
// release gates in cmd/pleiades read, from the environment or from
// tests/win/.env (gitignored; tests/win/.env.example shows them). A linked
// worktree with no tests/win/.env of its own reads the clone's main
// worktree's. The device is driven over WinRM through this branch's own
// pleiades, built under the worktree's git directory, with the credential
// that project already holds: wait.connection checks it answers,
// exec.winrm.shell that it runs Windows with go and tar.exe, the committed
// tree goes by win.file.copy and is unpacked by tar.exe, and the packages
// run there through tools/testgate as pr-check runs them, but without the
// race detector, which needs cgo and a C toolchain on Windows.
//
// Usage:
//
//	go run ./tools/wincheck [-optional] [-phase all|prepare|test] [-commit HEAD] [-p N] package...
//
// -phase, -ready-file and an interrupt behave as maccheck's
// (tools/internal/oscheck). With -optional, a clone with no Windows
// machine configured is a skip, said so, which is how pr-check calls it.
//
// -arm64, first on the command line, checks the Windows ARM machine instead:
// the same steps against a Windows 11 ARM VM on a Mac running VMware Fusion,
// named by PLEIADES_WINARM_PROJECT and PLEIADES_WINARM_DEVICE in
// tests/winarm/.env, its VM by PLEIADES_WINARM_VM_HOST (the Mac, a device in
// the same project granted VMwareFusionCapable) and PLEIADES_WINARM_VM (its
// vmx path). It names itself winarmcheck, so its build and its records never
// meet the x64 check's when both run at once.
// Exit status: 0 passed or skipped, 1 a test failed, 2 the machine could
// not be used.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
)

// check is wincheck's words and settings. The settings file is relative to
// the repository's top level and gitignored, so never committed or sent;
// the names are the WinRM release gates', so one project serves both.
var check = oscheck.LabCheck{
	Tool:       "wincheck",
	Kind:       "Windows machine",
	OS:         "Windows",
	EnvFile:    "tests/win/.env",
	ProjectKey: "PLEIADES_WINRM_PROJECT",
	DeviceKey:  "PLEIADES_WINRM_DEVICE",
	VMHostKey:  "PLEIADES_WIN_VM_HOST",
	VMKey:      "PLEIADES_WIN_VM",
	BoxWait:    oscheck.WindowsWait,
}

// checkARM is the Windows ARM machine's check: a VM on a Mac running VMware
// Fusion, started and suspended through virt.fusion.* around a run.
var checkARM = oscheck.LabCheck{
	Tool:       "winarmcheck",
	Kind:       "Windows ARM machine",
	OS:         "Windows (arm64)",
	EnvFile:    "tests/winarm/.env",
	ProjectKey: "PLEIADES_WINARM_PROJECT",
	DeviceKey:  "PLEIADES_WINARM_DEVICE",
	VMHostKey:  "PLEIADES_WINARM_VM_HOST",
	VMKey:      "PLEIADES_WINARM_VM",
	BoxWait:    oscheck.WindowsWait,
	Box:        oscheck.FusionBox,
}

// pick is the check args select, and the arguments left for it: -arm64,
// first, selects checkARM.
func pick(args []string) (oscheck.LabCheck, []string) {
	if len(args) > 0 && args[0] == "-arm64" {
		return checkARM, args[1:]
	}
	return check, args
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	c, args := pick(os.Args[1:])
	code := c.Run(ctx, args, os.Stdout, os.Stderr, newLeg)
	stop()
	os.Exit(code)
}
