// Package dnf implements the three "pkg.dnf.*" Collection methods:
// install, remove and upgrade.
//
// See internal/catalog/pkg/apt's own doc comment for the shape this
// mirrors (no new pkg/ primitive, read-first converge, and the
// capability it names) and for capability.DnfCapable's: implemented by
// linux_server and declared when it is classified
// linux_server,rhel_family.
package dnf

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/operand"
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

// packageName reads the package a pkg.dnf method acts on, held to
// operand.RPMPackage, the rule its parameter declares
// (collection.ParamFormatRPMPackage): an RPM package name, and none of the
// forms dnf reads as something other than one package (a group, a file, a
// capability, a version constraint). It is also what keeps a name from dnf's
// options: dnf5 refuses the "--" that would otherwise end them (runDnf).
func packageName(params map[string]any) (string, error) {
	return sdk.RequiredOperandParamOf(params, paramName, operand.RPMPackage)
}

// packageVersion reads the optional version an install joins onto the name
// as name-version, held to operand.PackageVersion: dnf install
// "nginx-*" installs every nginx- package.
func packageVersion(params map[string]any) (string, error) {
	return sdk.OptionalOperandParamOf(params, paramVersion, operand.PackageVersion)
}

// queryRPM asks rpm directly for a package's installed state, rather
// than dnf, because rpm IS the package database on a Red Hat-family
// host and answers instantly with no network access; dnf's own idea of
// "installed" reads the same database anyway.
//
// The "--" ends rpm's options, so the name is read as a name even if one
// beginning with "-" ever reached here past packageName. LC_ALL=C keeps
// rpm's sentence for an absent package in the words read below: under a
// French locale rpm writes "le paquet nosuchpkg n'est pas installé" and
// under a German one "Das Paket nosuchpkg ist nicht installiert" (measured
// on Fedora 42, FAILURE_PATTERNS 1175), and either would read as a failure
// to answer, failing every install of an absent package.
func queryRPM(ctx context.Context, conn *remoteexec.Conn, name string) (state, error) {
	result, err := conn.Run(ctx, "LC_ALL=C rpm -q --qf '%{VERSION}-%{RELEASE}' -- "+remoteexec.QuoteArg(name))
	if err != nil {
		return state{}, err
	}
	if result.ExitCode != 0 {
		// rpm -q exits 1 and writes "package <name> is not installed"
		// when absent. That is state, not a failure worth surfacing:
		// installing a brand-new package always starts here. It also exits
		// 1 for an option it does not know (measured on Rocky Linux 9:
		// "rpm: -oX: unknown option"), so the exit status alone cannot say
		// which happened, and only the sentence reads as "absent": reading
		// every failure that way is what let a refused option pass for an
		// absent package and reach the package manager (FAILURE_PATTERNS
		// 1174).
		if strings.Contains(result.Stdout+result.Stderr, "is not installed") {
			return state{}, nil
		}
		return state{}, fmt.Errorf("rpm -q %s exited %d: %s", name, result.ExitCode, failureDetail(result))
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
// error dnf hit trying to answer at all. No "--" precedes the name, for
// runDnf's reason.
func hasUpdate(ctx context.Context, conn *remoteexec.Conn, name string) (bool, error) {
	if err := checkOperands([]string{name}); err != nil {
		return false, err
	}
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
//
// options is the command and its flags, and operands the package names.
// No "--" goes between them: dnf 4 takes one, but dnf5 (Fedora 41 and
// later) refuses it, writing 'Unknown argument "--" for command "install"'
// and exiting 2, for install, remove, upgrade and check-update alike
// (measured on Fedora 42 with dnf5 5.2.18). What keeps an operand from
// dnf's options is the rule every operand passes before it reaches here
// (packageName, packageVersion), none of which admits a word beginning with
// "-", and a name joined to its version as name-version begins with the
// name. checkOperands holds that again here, so a caller that skipped the
// readers still cannot send an option.
func runDnf(ctx context.Context, conn *remoteexec.Conn, options []string, operands ...string) (remoteexec.Result, error) {
	if err := checkOperands(operands); err != nil {
		return remoteexec.Result{}, err
	}
	argv := append(append([]string{"dnf"}, options...), operands...)
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

// checkOperands refuses an operand dnf would read as an option or a
// pattern (pkg/operand's general rule), since dnf gets no "--" to end its
// options (runDnf).
func checkOperands(operands []string) error {
	for _, op := range operands {
		if err := operand.Check("package", op); err != nil {
			return fmt.Errorf("dnf: %w", err)
		}
	}
	return nil
}

// requirePackage refuses, before dnf is asked to install an absent
// package, a name no package in the device's repositories has. dnf install
// also reads a word as a capability another package provides: on Rocky
// Linux 9, dnf install vim installs vim-enhanced, and rpm -q vim still
// reports vim not installed afterward (measured), so the task would report
// a change on every run and record an undo, pkg.dnf.remove of vim, that
// removes nothing. dnf repoquery matches a name, a name.arch or a
// name-version, never a capability, so a word it lists no package for is
// one dnf install would read some other way (measured on Rocky Linux 9 and
// Fedora 42: vim, @core and perl(strict) list nothing, jq and jq.x86_64
// list jq). Only a real run asks, since a check promises not to ask dnf
// anything that can refresh its metadata.
func requirePackage(ctx context.Context, conn *remoteexec.Conn, name string) error {
	if err := checkOperands([]string{name}); err != nil {
		return err
	}
	result, err := conn.Run(ctx, "LC_ALL=C dnf repoquery -q --qf '%{name}\\n' "+remoteexec.QuoteArg(name))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("dnf repoquery %s exited %d: %s", name, result.ExitCode, failureDetail(result))
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return fmt.Errorf("no package in this device's repositories is named %s (it may be a capability, such as vim, that dnf would read as another package), so nothing is installed: name the package itself, which dnf provides tells", name)
	}
	return nil
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

// recordPrediction is recordState for a check: name, and the diff from
// before to what the change would leave. version is what would be
// installed when it is known (a version the task names, or nothing when
// the package would be removed); an empty version on an install means
// dnf would choose it, and it is left out of both the stat and the diff
// rather than guessed, since asking dnf which build it would pick can
// refresh its metadata cache on the device.
func recordPrediction(rc sdk.RunbookContext, name string, before state, installed bool, version string) error {
	if err := rc.SetStat(statName, name); err != nil {
		return err
	}
	after := map[string]any{"installed": installed}
	if !installed || version != "" {
		after["version"] = version
		if err := rc.SetStat(statVersion, version); err != nil {
			return err
		}
	}
	return sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after})
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
