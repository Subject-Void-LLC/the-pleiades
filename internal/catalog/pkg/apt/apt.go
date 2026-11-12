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

	"github.com/Subject-Void-LLC/the-pleiades/pkg/operand"
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

// packageName reads the package a pkg.apt method acts on, held to
// operand.AptPackage, the rule its parameter declares
// (collection.ParamFormatAptPackage) and validation, the engine and the
// rollback planner hold it to as well: a Debian package name, with an
// optional ":architecture", and none of the forms apt-get reads as something
// other than that one package.
//
// The trailing "-" is the one measured first. apt-get install reads a word
// ending in "-" that names no package as "remove the package before the
// dash", and dpkg-query reports that word absent first, so pkg.apt.install
// with "diffutils-" would plan the removal of diffutils (measured on Debian
// bookworm: "1 to remove", FAILURE_PATTERNS 1174). No Debian package name
// ends in "-". A trailing "+" is not refused here, since real names such as
// g++ end in one; apt-get reads it as "install the package before the +"
// only when no package has the exact name, and pkg.apt.install and
// pkg.apt.upgrade refuse such a name before installing anything
// (requireCandidate).
func packageName(params map[string]any) (string, error) {
	return sdk.RequiredOperandParamOf(params, paramName, operand.AptPackage)
}

// packageVersion reads the optional version an install joins onto the name
// as name=version, held to operand.PackageVersion: apt-get reads
// name=version- as "remove the package" (measured on Debian 12: "Remv
// diffutils").
func packageVersion(params map[string]any) (string, error) {
	return sdk.OptionalOperandParamOf(params, paramVersion, operand.PackageVersion)
}

// dpkgQueryNoMatch is dpkg-query's exit status for a name it has no record
// of: a package never installed. Any other non-zero status is a failure to
// answer at all (2 is a usage error, such as an option it does not know).
const dpkgQueryNoMatch = 1

// queryDpkg asks dpkg directly for a package's installed state, rather
// than apt or apt-get, because dpkg IS the package database and answers
// instantly with no network access; apt-get's own idea of "installed"
// reads the same database anyway.
//
// The "--" ends dpkg-query's options, so the name is read as a name even
// if one beginning with "-" ever reached here past packageName.
func queryDpkg(ctx context.Context, conn *remoteexec.Conn, name string) (state, error) {
	result, err := conn.Run(ctx, "dpkg-query -W -f='${Status}\\t${Version}' -- "+remoteexec.QuoteArg(name))
	if err != nil {
		return state{}, err
	}
	switch result.ExitCode {
	case 0:
	case dpkgQueryNoMatch:
		// A package dpkg has never heard of is "not installed," not a
		// failure worth surfacing: installing a brand-new package always
		// starts here.
		return state{}, nil
	default:
		// Anything else is dpkg-query failing to answer. Reading it as "not
		// installed" is what let a refused option pass for an absent
		// package and reach apt-get install (FAILURE_PATTERNS 1174).
		return state{}, fmt.Errorf("dpkg-query -W %s exited %d: %s", name, result.ExitCode, failureDetail(result))
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
// install is for the package named exactly name, APT's own name for it and
// the version pkg.apt.upgrade compares the installed one against. An empty
// result with a nil error means APT has no candidate for that exact name:
// it has never heard of it, or it is a virtual package another provides.
//
// Only the block apt-cache heads with name itself is read. apt-cache
// policy, like apt-get, reads a word naming no package as a regular
// expression, so "jq+" printed the block of libjs-jquery.sparkline, whose
// candidate an older reading of the first Candidate line took for jq+'s
// (measured on Debian 12). A qualified name (jq:amd64) is headed by its
// package alone ("jq:").
//
// LC_ALL=C keeps the words read here English: under a French locale
// apt-cache writes "Candidat :" (measured on Debian 12, FAILURE_PATTERNS
// 1175), which no reading of "Candidate:" would find.
func queryCandidate(ctx context.Context, conn *remoteexec.Conn, name string) (string, error) {
	result, err := conn.Run(ctx, "LC_ALL=C apt-cache policy -- "+remoteexec.QuoteArg(name))
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("apt-cache policy %s exited %d: %s", name, result.ExitCode, failureDetail(result))
	}
	base, _, _ := strings.Cut(name, ":")
	found, inBlock := false, false
	for _, line := range strings.Split(result.Stdout, "\n") {
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			// A line at the margin heads one package's block: "<name>:".
			header := strings.TrimSuffix(line, ":")
			inBlock = header == base || header == name
			found = found || inBlock
			continue
		}
		if !inBlock {
			continue
		}
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
	if !found {
		// No block for this exact name: apt-cache knows no such package, or
		// matched the word as a regular expression against others.
		return "", nil
	}
	return "", fmt.Errorf("apt-cache policy %s: no Candidate line in its output", name)
}

// requireCandidate refuses, before apt-get is asked to install an absent
// package, a name APT has no candidate for exactly: one it has never heard
// of, a virtual package (apt-get installs a provider under another name),
// or one apt-get would read some other way when no package has the name,
// such as "jq+" ("install jq") or "jq++" (a regular expression that planned
// upgrades of dpkg and libc6), both measured on Debian 12. Each of those would install packages dpkg-query never
// reports under the word, so the undo recorded by the word would remove
// nothing (FAILURE_PATTERNS 1174).
func requireCandidate(ctx context.Context, conn *remoteexec.Conn, name string) error {
	candidate, err := queryCandidate(ctx, conn, name)
	if err != nil {
		return err
	}
	if candidate == "" {
		return fmt.Errorf("APT has no package named exactly %s to install (one it does not know, a virtual package another provides, or a word apt-get would read as another package or a regular expression), so nothing is installed: name the package itself", name)
	}
	return nil
}

// predictInstall is what installing name would leave, for a check: the
// version named, or, with none named, the version APT would pick, read
// from apt-cache without installing anything. A package APT has no
// candidate for is refused, since installing it would fail.
//
// The candidate is read whether or not a version is named, since a real
// run refuses a name APT has no package for exactly before it installs
// anything (requireCandidate), and the check must say so too.
func predictInstall(ctx context.Context, conn *remoteexec.Conn, name, version string) (state, error) {
	candidate, err := queryCandidate(ctx, conn, name)
	if err != nil {
		return state{}, err
	}
	if candidate == "" {
		return state{}, fmt.Errorf("APT has no candidate to install for %s, so a real run of this task would fail", name)
	}
	if version != "" {
		return state{installed: true, version: version}, nil
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
	options := []string{"-s", "remove"}
	if autoRemove {
		options = append(options, "--auto-remove")
	}
	result, err := runAptGet(ctx, conn, options, pkgs...)
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
// package, and returns the warning about the ones it keeps, for the
// caller to record with any others.
func recordDependencies(rc sdk.RunbookContext, removed, kept []string) ([]string, error) {
	if len(removed) > 0 {
		if err := rc.SetStat(statDependenciesRemoved, removed); err != nil {
			return nil, err
		}
	}
	if len(kept) == 0 {
		return nil, nil
	}
	return []string{fmt.Sprintf("kept %s: still needed by another installed package, installed "+
		"on purpose since, or removing it would remove more than this package brought", strings.Join(kept, ", "))}, nil
}

// runAptGet runs one apt-get invocation non-interactively and treats a
// non-zero exit as a real error.
//
// options is the command and its flags, and operands the package names,
// which always follow a "--": apt-get then reads each as a name even if one
// beginning with "-" reached here past packageName, where without it
// apt-get install -y took such a word as a configuration option and exited
// 0 having installed nothing (measured on Debian bookworm, FAILURE_PATTERNS
// 1174).
//
// DEBIAN_FRONTEND=noninteractive is set on every call rather than only
// where a prompt seems likely, because whether a given package's
// maintainer scripts prompt is not something this platform knows ahead
// of time, and a task hanging on a terminal that will never answer is a
// worse failure than a task that never needed to ask. LC_ALL=C keeps the
// "Remv" lines simulateRemove reads in the words it reads them by, whatever
// the device's locale (FAILURE_PATTERNS 1175).
func runAptGet(ctx context.Context, conn *remoteexec.Conn, options []string, operands ...string) (remoteexec.Result, error) {
	argv := append(append(append([]string{"apt-get"}, options...), "--"), operands...)
	command := "DEBIAN_FRONTEND=noninteractive LC_ALL=C " + remoteexec.QuoteCommand(argv)
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
