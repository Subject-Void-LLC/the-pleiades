// The FreeBSD machine as a lab device: checked, sent its tree and run
// through oscheck's POSIX steps, from the project that manages it.
package main

import (
	"context"
	"io"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
)

// bsdPath is added to the login's PATH: go unpacked in its home, which
// needs no root (the lab's bsd-check has it there), then where go.dev's
// archive and FreeBSD's go package put it.
const bsdPath = "$HOME/sdk/go/bin:/usr/local/go/bin:/usr/local/bin"

// freebsd is the real machine.
type freebsd struct {
	oscheck.Lab
	posix oscheck.POSIX
}

// newLeg is device in project.
func newLeg(project, device string) (oscheck.Leg, error) {
	lab, err := oscheck.NewLab("bsdcheck", project, device)
	if err != nil {
		return nil, err
	}
	return freebsd{Lab: lab, posix: oscheck.POSIX{
		Pleiades: lab.Pleiades,
		Tool:     "bsdcheck",
		Machine:  "the FreeBSD machine",
		Dir:      ".pleiades-bsdcheck",
		Path:     bsdPath,
		// Written in the project, which file.copy reads from, and deleted
		// once sent.
		Files: ".bsdcheck",
	}}, nil
}

// Ready builds pleiades from this tree and asks the machine whether it
// runs FreeBSD with go and cc.
func (f freebsd) Ready() error {
	if err := f.Pleiades.Build(); err != nil {
		return err
	}
	return f.posix.Ready(f.Target, "FreeBSD", "FreeBSD machine",
		oscheck.Need{Command: "go", Why: "install it with `pkg install go`, or unpack go.dev's FreeBSD release into ~/sdk (examples/virtualbox_lab does, with no root)"},
		oscheck.Need{Command: "git", Why: "the tests that make a throwaway repository need it: install it with `pkg install git`, or have the clone's first boot install it (virt.vbox.vm.clone's packages)"},
		oscheck.Need{Command: "cc", Why: "the race detector builds with it, and FreeBSD's base system has it unless it was left out"})
}

// Dedicated is the machine as a lab VM kept for checks, whose login's Go
// caches its readiness step clears before every run (oscheck.Dedicating).
func (f freebsd) Dedicated() oscheck.Leg {
	f.posix.Dedicated = true
	return f
}

// Ship unpacks commit sha's tree on the machine (oscheck.POSIX.Ship).
func (f freebsd) Ship(sha string) error { return f.posix.Ship(sha) }

// Test runs the packages on sha's tree (oscheck.POSIX.Test).
func (f freebsd) Test(ctx context.Context, sha string, parallel int, packages []string, stdout io.Writer) (bool, error) {
	return f.posix.Test(ctx, f.Target, sha, parallel, packages, stdout)
}

// Records reads back the tests' records (oscheck.POSIX.Records).
func (f freebsd) Records(ctx context.Context, sha string) ([]flakegate.Run, error) {
	return f.posix.Records(ctx, sha)
}
