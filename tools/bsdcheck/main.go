// Command bsdcheck runs a branch's container-free packages on a real
// FreeBSD machine, before a push, for what neither Linux nor macOS shows:
// FreeBSD's own userland, kernel and file system. No CI job runs FreeBSD
// at all.
//
// The machine is a device of a Pleiades project that already manages it,
// as the VirtualBox lab's bsd-check is (examples/virtualbox_lab):
// PLEIADES_BSD_PROJECT names the project's directory and
// PLEIADES_BSD_DEVICE the device in it, from the environment or from
// tests/bsd/.env (gitignored; tests/bsd/.env.example shows them). A linked
// worktree with no tests/bsd/.env of its own reads the clone's main
// worktree's. The device is driven through this branch's own pleiades,
// built under the worktree's git directory, with the credential that
// project already holds: wait.connection checks it answers, exec.command
// that it runs FreeBSD with go and cc (the race detector builds with it),
// and the committed tree goes by file.copy and archive.extract, as
// tools/maccheck sends a Mac its tree. The packages run there through
// tools/testgate as pr-check runs them, race detector included.
//
// Usage:
//
//	go run ./tools/bsdcheck [-optional] [-phase all|prepare|test] [-commit HEAD] [-p N] package...
//
// -phase, -ready-file and an interrupt behave as maccheck's
// (tools/internal/oscheck). With -optional, a clone with no FreeBSD
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

// check is bsdcheck's words and settings. The settings file is relative to
// the repository's top level and gitignored, so never committed or sent.
var check = oscheck.LabCheck{
	Tool:       "bsdcheck",
	Kind:       "FreeBSD machine",
	OS:         "FreeBSD",
	EnvFile:    "tests/bsd/.env",
	ProjectKey: "PLEIADES_BSD_PROJECT",
	DeviceKey:  "PLEIADES_BSD_DEVICE",
	VMHostKey:  "PLEIADES_BSD_VM_HOST",
	VMKey:      "PLEIADES_BSD_VM",
	DataKey:    "PLEIADES_BSD_DATA",
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := check.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, newLeg)
	stop()
	os.Exit(code)
}
