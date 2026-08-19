// Package dnf implements the three "pkg.dnf.*" Collection methods:
// install, remove and upgrade.
//
// See internal/catalog/pkg/apt's own doc comment for the shape this
// mirrors (no new pkg/ primitive, read-first converge, and the
// capability-wiring gap this namespace shares with it) and for
// capability.DnfCapable's own state: declared in
// pkg/capability/capabilities_package.go, structurally implemented by
// no device type in this repository yet.
package dnf

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// paramName is the package to act on, ansible.builtin.dnf's own
// spelling. paramVersion pins an install or upgrade to an exact version;
// left unset, "current" means whatever dnf would install unpinned.
const (
	paramName    = "name"
	paramVersion = "version"
)

// Stat names this namespace emits.
const (
	statName    = "name"
	statVersion = "version"
)

// state is what rpm reports about one package.
type state struct {
	installed bool
	version   string // rpm's "version-release" for the installed build; empty when not installed
}

// Map renders state the way sdk.Diff records it.
func (s state) Map() map[string]any {
	return map[string]any{"installed": s.installed, "version": s.version}
}

// queryRPM asks rpm directly for a package's installed state, rather
// than dnf, because rpm IS the package database on a Red Hat-family
// host and answers instantly with no network access; dnf's own idea of
// "installed" reads the same database anyway.
func queryRPM(ctx context.Context, conn *remoteexec.Conn, name string) (state, error) {
	result, err := conn.Run(ctx, "rpm -q --qf '%{VERSION}-%{RELEASE}' "+remoteexec.QuoteArg(name))
	if err != nil {
		return state{}, err
	}
	if result.ExitCode != 0 {
		// rpm -q exits 1 and writes "package <name> is not installed"
		// when absent. That is state, not a failure worth surfacing:
		// installing a brand-new package always starts here.
		return state{}, nil
	}
	version := strings.TrimSpace(result.Stdout)
	if version == "" {
		return state{}, nil
	}
	return state{installed: true, version: version}, nil
}

// hasUpdate asks dnf whether a newer build of an already-installed
// package is available, using dnf check-update's own exit code
// convention rather than parsing its table output: 0 means nothing to
// update, 100 means updates are available, and anything else is a real
// error dnf hit trying to answer at all.
func hasUpdate(ctx context.Context, conn *remoteexec.Conn, name string) (bool, error) {
	result, err := conn.Run(ctx, "dnf check-update "+remoteexec.QuoteArg(name))
	if err != nil {
		return false, err
	}
	switch result.ExitCode {
	case 0:
		return false, nil
	case 100:
		return true, nil
	default:
		return false, fmt.Errorf("dnf check-update %s exited %d: %s", name, result.ExitCode, failureDetail(result))
	}
}

// runDnf runs one dnf invocation non-interactively (-y) and treats a
// non-zero exit as a real error.
func runDnf(ctx context.Context, conn *remoteexec.Conn, args ...string) (remoteexec.Result, error) {
	argv := append([]string{"dnf"}, args...)
	command := remoteexec.QuoteCommand(argv)
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
// non-zero exit: stderr when dnf wrote one, stdout otherwise, and a
// plain statement when it said nothing at all.
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
