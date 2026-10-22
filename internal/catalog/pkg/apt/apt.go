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
// # The capability, and the devices that satisfy it
//
// capability.AptCapable (pkg/capability/capabilities_package.go) is what
// RequiredCapabilities below names. linux_server implements its accessors
// (internal/inventory/devices/linux/packages.go) but declares it only when
// classified linux_server,debian_family: which package manager a server
// has is per-distribution data, so no Linux server claims one by default.
// cmd/pleiades's TestCLI_PackageAndAccountMethodsReachARealDevice runs
// pkg.install, which dispatches here, through the real binary against a
// real Debian device.
package apt

import (
	"context"
	"fmt"
	"sort"
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
	// paramDependencies names, on pkg.apt.remove, the packages installing
	// this one pulled in, which pkg.apt.install's undo passes so that
	// removing the package removes what came with it.
	paramDependencies = "dependencies"
)

// Stat names this namespace emits.
const (
	statName    = "name"
	statVersion = "version"
	// statDependenciesRemoved is the dependencies a pkg.apt.remove removed
	// with its package, or would remove, for a check.
	statDependenciesRemoved = "dependencies_removed"
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

// predictInstall is what installing name would leave, for a check: the
// version named, or, with none named, the version APT would pick, read
// from apt-cache without installing anything. A package APT has no
// candidate for is refused, since installing it would fail.
func predictInstall(ctx context.Context, conn *remoteexec.Conn, name, version string) (state, error) {
	if version != "" {
		return state{installed: true, version: version}, nil
	}
	candidate, err := queryCandidate(ctx, conn, name)
	if err != nil {
		return state{}, err
	}
	if candidate == "" {
		return state{}, fmt.Errorf("APT has no candidate to install for %s, so a real run of this task would fail", name)
	}
	return state{installed: true, version: candidate}, nil
}

// installedPackages is every package dpkg reports installed ("ii"), so
// an install can tell which packages it pulled in by comparing the set
// before and after.
func installedPackages(ctx context.Context, conn *remoteexec.Conn) (map[string]bool, error) {
	result, err := conn.Run(ctx, "dpkg-query -W -f='${db:Status-Abbrev}${Package}\\n'")
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("dpkg-query -W exited %d: %s", result.ExitCode, failureDetail(result))
	}
	installed := map[string]bool{}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if name, ok := strings.CutPrefix(line, "ii "); ok && strings.TrimSpace(name) != "" {
			installed[strings.TrimSpace(name)] = true
		}
	}
	return installed, nil
}

// newlyInstalled is what after holds that before did not, other than
// name itself, in name order: the dependencies an install pulled in.
func newlyInstalled(before, after map[string]bool, name string) []string {
	var added []string
	for pkg := range after {
		if !before[pkg] && pkg != name {
			added = append(added, pkg)
		}
	}
	sort.Strings(added)
	return added
}

// simulateRemove asks apt-get, changing nothing (-s), which packages
// removing pkgs would remove; with autoRemove, also those nothing would
// need any more. Its "Remv <name> [<version>]" lines are read by name.
func simulateRemove(ctx context.Context, conn *remoteexec.Conn, autoRemove bool, pkgs ...string) (map[string]bool, error) {
	args := []string{"-s", "remove"}
	if autoRemove {
		args = append(args, "--auto-remove")
	}
	result, err := runAptGet(ctx, conn, append(args, pkgs...)...)
	if err != nil {
		return nil, err
	}
	removed := map[string]bool{}
	for _, line := range strings.Split(result.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "Remv" {
			pkg, _, _ := strings.Cut(fields[1], ":")
			removed[pkg] = true
		}
	}
	return removed, nil
}

// removableDependencies decides which of deps removing name may take with
// it, and which stay. A dependency is removed only while it is installed,
// apt would remove it as no longer needed once name is gone (so nothing
// installed since has come to depend on it, and nobody has installed it
// on purpose), and removing all of those together removes nothing more:
// apt removes whatever depends on a package it removes, and a dependency
// is never worth a package nobody asked to lose. One that fails any of
// those stays, and is reported as kept. A dependency no longer installed
// is neither.
func removableDependencies(ctx context.Context, conn *remoteexec.Conn, name string, deps []string) (remove, keep []string, err error) {
	if len(deps) == 0 {
		return nil, nil, nil
	}
	installed, err := installedPackages(ctx, conn)
	if err != nil {
		return nil, nil, err
	}
	unneeded, err := simulateRemove(ctx, conn, true, name)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	for _, dep := range deps {
		if seen[dep] || !installed[dep] {
			continue
		}
		seen[dep] = true
		if unneeded[dep] {
			remove = append(remove, dep)
		} else {
			keep = append(keep, dep)
		}
	}
	if len(remove) > 0 {
		would, err := simulateRemove(ctx, conn, false, append([]string{name}, remove...)...)
		if err != nil {
			return nil, nil, err
		}
		allowed := map[string]bool{name: true}
		for _, dep := range remove {
			allowed[dep] = true
		}
		for pkg := range would {
			if !allowed[pkg] {
				keep, remove = append(keep, remove...), nil
				break
			}
		}
	}
	sort.Strings(remove)
	sort.Strings(keep)
	return remove, keep, nil
}

// recordDependencies records the dependencies a remove takes with its
// package, and warns about the ones it keeps.
func recordDependencies(rc sdk.RunbookContext, removed, kept []string) error {
	if len(removed) > 0 {
		if err := rc.SetStat(statDependenciesRemoved, removed); err != nil {
			return err
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return sdk.RecordWarnings(rc, []string{fmt.Sprintf("kept %s: still needed by another installed package, installed "+
		"on purpose since, or removing it would remove more than this package brought", strings.Join(kept, ", "))})
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
