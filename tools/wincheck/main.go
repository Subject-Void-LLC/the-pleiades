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

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := check.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, newLeg)
	stop()
	os.Exit(code)
}
