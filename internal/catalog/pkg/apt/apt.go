// Package apt implements the three "pkg.apt.*" Collection methods:
// install, remove and upgrade.
//
// # No new pkg/ primitive
//
// This talks to APT entirely through pkg/remoteexec (via sdk.Connect),
// the same primitive exec.command uses. It queries package state with
// dpkg-query and apt-cache, and changes it with apt-get, all as plain
// SSH commands. Nothing here needed a shared primitive of its own, the
// same way file.* did not: the query-then-act shape lives in this
// package, not in pkg/.
//
// # Read first, always
//
// Every method here reads dpkg's own record of the package before
// deciding whether to act, and acts only on the difference, for the
// reason exec.command's own doc comment gives for Changed: a method that
// sent apt-get unconditionally would work, and would report changed on
// every run forever, which is indistinguishable from a method that is
// genuinely fixing something every time.
//
// # The capability this cannot reach yet
//
// capability.AptCapable exists (pkg/capability/capabilities_package.go)
// and is what RequiredCapabilities below names, but no device type in
// this repository structurally implements it today: PackageManagerName,
// AptSourcesList and DnfRepoDir have no real accessor anywhere.
// internal/inventory/devices/linux/server_test.go's
// TestNewServer_UnionsClassificationCapabilities is a deliberate
// regression proof of exactly this, for linux.Server specifically:
// declaring AptCapable in a record's classification data is not enough,
// because HasCapability also requires a structural Implements, and
// nothing implements it. That is settled, intentional architecture
// (package-manager family is genuinely per-distro data, unlike a
// service manager which has a safe universal default), not an oversight
// this package's own tests can or should paper over.
//
// The practical consequence: pkg.apt.install, pkg.apt.remove and
// pkg.apt.upgrade are implemented and tested here, against a real SSH
// server with a fake apt-get on PATH, exactly the tier svc.systemd.*
// was accepted at (that namespace also ships with no container release
// gate). They are not yet reachable against any real inventory device
// through the platform end to end. Wiring a device type to
// AptCapable/DnfCapable is separate, deliberate follow-up work.
package apt

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// paramName is the package to act on, ansible.builtin.apt's own
// spelling. paramVersion pins an install or upgrade to an exact version;
// left unset, "current" means whatever apt-get would install unpinned.
const (
	paramName    = "name"
	paramVersion = "version"
)

// Stat names this namespace emits.
const (
	statName    = "name"
	statVersion = "version"
)

// state is what dpkg-query reports about one package.
type state struct {
	installed bool
	version   string // dpkg's installed version; empty when not installed
}

// Map renders state the way sdk.Diff records it: as a plain map, since
// what is worth recording about a package (installed, version) is fixed
// for this namespace but not shared with dnf's own, different record.
func (s state) Map() map[string]any {
	return map[string]any{"installed": s.installed, "version": s.version}
}

// queryDpkg asks dpkg directly for a package's installed state, rather
// than apt or apt-get, because dpkg IS the package database and answers
// instantly with no network access; apt-get's own idea of "installed"
// reads the same database anyway.
func queryDpkg(ctx context.Context, conn *remoteexec.Conn, name string) (state, error) {
	result, err := conn.Run(ctx, "dpkg-query -W -f='${Status}\\t${Version}' "+remoteexec.QuoteArg(name))
	if err != nil {
		return state{}, err
	}
	if result.ExitCode != 0 {
		// dpkg-query exits 1 for a package it has never heard of. That is
		// "not installed," not a failure worth surfacing: installing a
		// brand-new package always starts here.
		return state{}, nil
	}
	fields := strings.SplitN(strings.TrimSpace(result.Stdout), "\t", 2)
	if len(fields) != 2 || !strings.HasPrefix(fields[0], "install ok") {
		// "deinstall ok config-files" and similar are dpkg's record of a
		// package apt removed but did not purge: its config is still on
		// disk, but the package itself is absent, exactly like a package
		// dpkg has never heard of.
		return state{}, nil
	}
	return state{installed: true, version: fields[1]}, nil
}

// queryCandidate asks apt-cache what the newest version available to
// install is, APT's own name for it and the version pkg.apt.upgrade
// compares the installed one against. An empty result with a nil error
// means apt-cache reported no candidate at all, which happens for a
// package APT has never heard of.
func queryCandidate(ctx context.Context, conn *remoteexec.Conn, name string) (string, error) {
	result, err := conn.Run(ctx, "apt-cache policy "+remoteexec.QuoteArg(name))
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("apt-cache policy %s exited %d: %s", name, result.ExitCode, failureDetail(result))
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		v, ok := strings.CutPrefix(strings.TrimSpace(line), "Candidate:")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "(none)" {
			return "", nil
		}
		return v, nil
	}
	return "", fmt.Errorf("apt-cache policy %s: no Candidate line in its output", name)
}

// runAptGet runs one apt-get invocation non-interactively and treats a
// non-zero exit as a real error.
//
// DEBIAN_FRONTEND=noninteractive is set on every call rather than only
// where a prompt seems likely, because whether a given package's
// maintainer scripts prompt is not something this platform knows ahead
// of time, and a task hanging on a terminal that will never answer is a
// worse failure than a task that never needed to ask.
func runAptGet(ctx context.Context, conn *remoteexec.Conn, args ...string) (remoteexec.Result, error) {
	argv := append([]string{"apt-get"}, args...)
	command := "DEBIAN_FRONTEND=noninteractive " + remoteexec.QuoteCommand(argv)
	result, err := conn.Run(ctx, command)
	if err != nil {
		return remoteexec.Result{}, err
	}
	if result.ExitCode != 0 {
		return remoteexec.Result{}, fmt.Errorf("%s exited %d: %s", command, result.ExitCode, failureDetail(result))
	}
	return result, nil
}

// failureDetail picks the stream an operator should read after a
// non-zero exit: stderr when apt-get wrote one, stdout otherwise, and a
// plain statement when it said nothing at all. Mirrors
// internal/catalog/exec.failureDetail; apt-get almost always explains
// itself on stderr, which is why that stream is preferred.
func failureDetail(result remoteexec.Result) string {
	if detail := strings.TrimSpace(result.Stderr); detail != "" {
		return detail
	}
	if detail := strings.TrimSpace(result.Stdout); detail != "" {
		return detail
	}
	return "no output"
}

// recordState writes name, the version left installed, and the
// before/after diff, the three things every method in this namespace
// reports regardless of which one ran.
func recordState(rc sdk.RunbookContext, name string, before, after state) error {
	if err := rc.SetStat(statName, name); err != nil {
		return err
	}
	if err := rc.SetStat(statVersion, after.version); err != nil {
		return err
	}
	return sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after.Map()})
}
