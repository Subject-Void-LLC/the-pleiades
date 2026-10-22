package apt

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "pkg.apt.install",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameApt},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true, Site: collection.SiteTarget, Device: collection.DeviceRequired},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			// It asks dpkg before it acts, so a check can predict through the same code (CheckInstall).
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that installed an absent package emits a pkg.apt.remove naming it and the packages the " +
					"install pulled in with it (dependencies), which the remove takes too while nothing else needs them " +
					"and removing them takes nothing else with them; any it keeps it names. An install that pulled in " +
					"more than 128 packages, or whose installed set could not be read afterwards, names the package " +
					"alone and says so, leaving the rest to apt-get autoremove. A run that found it " +
					"already present, even at a different version than requested, emits nothing: reinstalling over an " +
					"existing package is not something removing it would undo correctly, since the version that was " +
					"already there before this task ran would be lost along with the one this task left. What the " +
					"inverse cannot restore is anything the package's install scripts did: a service they started, a " +
					"user they created, a config file they wrote outside dpkg's own tracking.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "pkg.apt.remove", Record: []string{"name", "dependencies"}},
				},
			},
			Doc: installDoc(),
		},
		Invoke: Install,
		Check:  CheckInstall,
	})
}

func installDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Makes sure a package is installed via APT.",
		Description: "Makes sure a package is present on a Debian-family host, installing it if it is absent. This is ansible.builtin.apt with state=present. Installed state is read from dpkg before anything is sent, so a package that is already present at the requested version (or present at any version, when none is requested) reports no change and no command reaches the device. version pins to an exact version string, the same way apt-get install name=version does; leave it unset to mean whatever apt-get would install unpinned.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The APT package name to install, such as curl or nginx."},
			{Name: paramVersion, Type: "string", Description: "Install exactly this version rather than whatever is current. A package already installed at a different version is upgraded or downgraded to match."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The package this task acted on."},
			{Name: statVersion, Type: "string", Returned: "always", Description: "The version left installed after this task."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What dpkg reported about the package before this task and after it, each holding installed and version. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
		},
		Examples: []collection.Example{
			{
				Name:        "Install a package",
				RunbookYAML: "- name: Make sure curl is installed\n  pkg.apt.install:\n    name: curl\n",
			},
			{
				Name:        "Pin an exact version",
				RunbookYAML: "- name: Install a specific nginx build\n  pkg.apt.install:\n    name: nginx\n    version: 1.24.0-2ubuntu7\n",
			},
		},
		SeeAlso: []string{"pkg.apt.remove", "pkg.apt.upgrade", "pkg.dnf.install", "pkg.install"},
	}
}

// Install implements "pkg.apt.install".
//
// A package already present is left alone and the task reports no
// change, UNLESS version names a version the installed one does not
// match, in which case apt-get is asked to install that exact version,
// which upgrades or downgrades it as needed.
func Install(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return install(ctx, rc, device, params, collection.ModeExecute)
}

// CheckInstall is pkg.apt.install's check: the same dpkg read and change decision as Install, through the one body both share, then a prediction instead of apt-get (predictInstall).
func CheckInstall(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return install(ctx, rc, device, params, collection.ModeCheck)
}

// install is Install's and CheckInstall's one body; mode says which.
func install(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "pkg.apt.install"

	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	version := sdk.StringParam(params, paramVersion)

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	before, err := queryDpkg(ctx, conn, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if mode == collection.ModeCheck {
		if before.installed && (version == "" || before.version == version) {
			return collection.Result{}, recordState(rc, name, before, before)
		}
		predicted, err := predictInstall(ctx, conn, name, version)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		if err := recordState(rc, name, before, predicted); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{Changed: true}, nil
	}

	changed := false
	if !before.installed || (version != "" && before.version != version) {
		pkgArg := name
		if version != "" {
			pkgArg = name + "=" + version
		}
		var preInstall map[string]bool
		if !before.installed {
			if preInstall, err = installedPackages(ctx, conn); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
		if _, err := runAptGet(ctx, conn, "install", "-y", pkgArg); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		changed = true
		// The package is installed now, so its undo is recorded before
		// anything else can fail: a read that fails from here on costs the
		// undo only its list of dependencies, never the undo itself.
		if !before.installed {
			if err := recordInstallUndo(ctx, rc, conn, name, preInstall); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
	}

	after := before
	if changed {
		if after, err = queryDpkg(ctx, conn, name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	if err := recordState(rc, name, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	return collection.Result{Changed: changed}, nil
}

// maxUndoDependencies is the most dependencies an undo names: the most
// values the journal keeps for one list parameter (internal/engine's
// maxRecordedItems), which refuses a longer list outright and would leave
// the run's whole undo incomplete.
const maxUndoDependencies = 128

// recordInstallUndo records the undo of a fresh install of name: a
// pkg.apt.remove naming it and what the install pulled in, read from
// dpkg's installed set against preInstall, the set before apt-get ran.
//
// Only a genuine absent-to-present transition gets one. A version change
// on a package that was already there cannot safely undo to "remove it":
// the version installed before this task ran is gone the moment apt-get
// replaces it, so there is nothing left to name as what removing would
// restore.
//
// When the installed set cannot be read, or the install pulled in more
// than an undo can name, the undo names the package alone and a warning
// says what it leaves: removing the package still works, and
// apt-get autoremove takes what it pulled in.
func recordInstallUndo(ctx context.Context, rc sdk.RunbookContext, conn *remoteexec.Conn, name string, preInstall map[string]bool) error {
	invParams := map[string]any{paramName: name}
	brought := ""
	var warnings []string
	postInstall, err := installedPackages(ctx, conn)
	pulledIn := newlyInstalled(preInstall, postInstall, name)
	switch {
	case err != nil:
		warnings = append(warnings, fmt.Sprintf("could not read what installing %s pulled in (%v): its undo removes %s alone, "+
			"and apt-get autoremove takes the rest", name, err, name))
	case len(pulledIn) > maxUndoDependencies:
		warnings = append(warnings, fmt.Sprintf("installing %s pulled in %d packages, more than the %d an undo names: its "+
			"undo removes %s alone, and apt-get autoremove takes the rest", name, len(pulledIn), maxUndoDependencies, name))
	case len(pulledIn) > 0:
		// As []any, the shape a list has in a runbook and after the
		// journal, so the undo reads the same however it is replayed.
		deps := make([]any, len(pulledIn))
		for i, dep := range pulledIn {
			deps[i] = dep
		}
		invParams[paramDependencies] = deps
		brought = fmt.Sprintf(", and %s, which it pulled in, while nothing else needs them", strings.Join(pulledIn, ", "))
	}
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:   "pkg.apt.remove",
		Params: invParams,
		Description: fmt.Sprintf("Remove %s, which this task installed%s. Anything its install scripts did beyond "+
			"placing its own files (a service they started, a user they created) is not undone.", name, brought),
	}); err != nil {
		return err
	}
	return sdk.RecordWarnings(rc, warnings)
}
