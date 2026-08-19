package wait

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// This file holds the one test that cannot be written from outside the
// package, and it is deliberately the only one.
//
// Everything else about this method is proven against a real SSH server
// running a real /bin/sh (port_test.go), because that is the path the
// platform runs. The branch below is the exception: the tool check ends
// in exit 0, so a non-zero status can only come from a shell that could
// not run the command at all, and a harness built on a real POSIX shell
// can never be one. The answer such a device returns is a Result, so the
// decision that reads it is tested with the Result itself rather than
// left unexercised.

// TestPortSelectProber_RefusesAShellThatDidNotRunTheCheck covers the
// device whose shell is not a POSIX shell at all: a network appliance's
// own CLI, which answers a command line it does not understand with an
// error and a non-zero status.
//
// Falling through to the "none of these tools" refusal would be the easy
// mistake, and it would send somebody to install python3 on a switch that
// has no shell to install it into. The message has to say the check never
// ran, and carry what the device said back.
func TestPortSelectProber_RefusesAShellThatDidNotRunTheCheck(t *testing.T) {
	_, err := portSelectProber(remoteexec.Result{
		ExitCode: 1,
		Stderr:   "% Invalid input detected at '^' marker.\n",
	})
	if err == nil {
		t.Fatal("a shell that never ran the check was accepted, so a later probe would have been built for nothing")
	}
	if !strings.Contains(err.Error(), "did not run the check") {
		t.Errorf("error = %q, want it to say the check never ran", err)
	}
	if !strings.Contains(err.Error(), "Invalid input detected") {
		t.Errorf("error = %q, want it to carry what the device said", err)
	}
	if strings.Contains(err.Error(), "has none of") {
		t.Errorf("error = %q, want it not to blame a missing tool for a shell that cannot run commands", err)
	}
}

// TestPortSelectProber_IgnoresAToolTheDeviceInvented proves the choice is
// made from this package's own preference list rather than from whatever
// the device printed.
//
// A device echoing an unexpected word is the shape of a shell profile
// that greets every session with a banner, and treating that word as a
// tool would build a probe command for something that does not exist.
func TestPortSelectProber_IgnoresAToolTheDeviceInvented(t *testing.T) {
	if _, err := portSelectProber(remoteexec.Result{Stdout: "Welcome to the device\n"}); err == nil {
		t.Fatal("a banner line was accepted as a probe tool")
	}

	prober, err := portSelectProber(remoteexec.Result{Stdout: "banner\nbash\n"})
	if err != nil {
		t.Fatalf("portSelectProber: %v", err)
	}
	if prober.tool != "bash" {
		t.Errorf("chose %q, want bash: the only real tool in the answer", prober.tool)
	}
}
