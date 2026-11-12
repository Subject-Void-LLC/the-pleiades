// Package need: a test that needs something the machine may not offer,
// skipped where it is missing and failed where the run requires it.
//
// This is testsupport.Require's whole implementation, kept in a package
// that imports nothing but the standard library. internal/testsupport
// imports testcontainers-go, and an external Collection author's `go mod
// tidy` loads the tests of every package pkg/external reaches, so a test
// there that imported testsupport put testcontainers-go's whole module
// graph into every such author's go.sum. A test of such a package uses
// this package instead (internal/archtest holds that).
package need

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// Env lists, comma separated, what a run insists on: a test that needs
// one of them fails when it is missing instead of skipping. "all" requires
// everything. CI sets it for what its runners provide, so a capability a
// runner loses turns the job red rather than quietly shrinking what it
// tests; a developer's machine leaves it unset.
const Env = "PLEIADES_TEST_REQUIRE"

// Every name a Require call can be given, and so every name Env may list.
// Env is checked against Names, so a typo or a renamed need in a workflow's
// list fails the run at its first Require instead of quietly requiring
// nothing. A caller that passes a name not here is caught by
// TestEveryRequireCallNamesAKnownNeed.
const (
	CMLController   = "a CML controller"
	Docker          = "docker"
	DockerDesktop   = "docker-desktop"
	DockerSocket    = "docker socket"
	IPv6Loopback    = "ipv6-loopback"
	Landlock        = "landlock"
	LandlockScoping = "landlock-scoping"
	Linux           = "linux"
	LocalStack      = "localstack"
	Memory          = "memory"
	NonRoot         = "non-root"
	OpenFiles       = "open-files"
	POSIXModes      = "posix-modes"
	PosixShell      = "posix-shell"
	PostgresClient  = "postgres-client"
	PTY             = "pty"
	RefusingPort    = "refusing-port"
	Symlinks        = "symlinks"
	Timeout         = "timeout"
	Toybox          = "toybox"
	Userns          = "userns"
)

// All is the word Env uses to require every need.
const All = "all"

// Names lists every known need, the registry Env is checked against.
var Names = []string{
	CMLController, Docker, DockerDesktop, DockerSocket, IPv6Loopback, Landlock,
	LandlockScoping, Linux, LocalStack, Memory, NonRoot, OpenFiles, POSIXModes,
	PosixShell, PostgresClient, PTY, RefusingPort, Symlinks, Timeout, Toybox, Userns,
}

// UnknownRequired returns the entries of Env that are neither All nor a
// known need, in the order written.
func UnknownRequired() []string {
	var unknown []string
	for _, name := range envList() {
		if name == "" || name == All || slices.Contains(Names, name) {
			continue
		}
		unknown = append(unknown, name)
	}
	return unknown
}

// Require stops t when the machine lacks what: available is the answer and
// why says what is missing and how to provide it. When this run requires
// what (Env), that is a failure; otherwise it is a skip, which every
// tools/testgate run lists with its reason ("needs <what>: <why>"), so it
// is never read as a pass. An Env entry that names no known need fails t
// first, whatever what is, since it would otherwise require nothing.
func Require(t testing.TB, what string, available bool, why string) {
	t.Helper()
	if unknown := UnknownRequired(); len(unknown) > 0 {
		t.Fatalf("%s=%s names %q, which no test needs; the known needs are %s, and %q for all",
			Env, os.Getenv(Env), unknown, strings.Join(Names, ", "), All)
		return // unreachable with a real testing.T, whose Fatalf never returns
	}
	if available {
		return
	}
	if required(what) {
		t.Fatalf("this run requires %s (%s=%s), and it is missing: %s", what, Env, os.Getenv(Env), why)
		return // unreachable with a real testing.T, whose Fatalf never returns
	}
	t.Skipf("needs %s: %s", what, why)
}

// ShellPath is the POSIX shell a test runs commands through, as
// pkg/remoteexec/remoteexectest's SSH server does for every command it is
// sent.
const ShellPath = "/bin/sh"

// POSIXShell stops t through Require("posix-shell") on a machine without
// ShellPath (Windows), for a test that runs commands through it: such a
// test stands in for a POSIX device, which a Windows machine is not.
func POSIXShell(t testing.TB) {
	t.Helper()
	_, err := os.Stat(ShellPath)
	Require(t, PosixShell, err == nil,
		"the test runs its commands through "+ShellPath+", as a POSIX device would, and this machine has none")
}

// envList returns Env's comma separated entries, trimmed.
func envList() []string {
	list := strings.Split(os.Getenv(Env), ",")
	for i := range list {
		list[i] = strings.TrimSpace(list[i])
	}
	return list
}

// required reports whether Env names what, or says all.
func required(what string) bool {
	list := envList()
	return slices.Contains(list, what) || slices.Contains(list, All)
}
