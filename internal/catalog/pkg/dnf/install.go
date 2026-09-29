package dnf

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "pkg.dnf.install",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameDnf},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			// It asks rpm before it acts, so a check can predict through the same code (CheckInstall).
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that installed an absent package emits a pkg.dnf.remove naming it. A run that found it " +
					"already present, even at a different version than requested, emits nothing, for the same reason " +
					"pkg.apt.install's inverse does not fire on a version change: the version already there before " +
					"this task ran is gone the moment dnf replaces it, so removing afterward would not restore it. " +
					"What the inverse cannot restore is anything the package's scriptlets did beyond placing its own " +
					"files.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "pkg.dnf.remove", Record: []string{"name"}},
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
		Summary:     "Makes sure a package is installed via DNF.",
		Description: "Makes sure a package is present on a Red Hat-family host, installing it if it is absent. This is ansible.builtin.dnf with state=present. Installed state is read from rpm before anything is sent, so a package that is already present at the requested version (or present at any version, when none is requested) reports no change and no command reaches the device. version pins to an exact version-release string, appended to the package name the way dnf's own name-version syntax expects (name-version, e.g. nginx-1.24.0); leave it unset to mean whatever dnf would install unpinned.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The RPM package name to install, such as curl or nginx."},
			{Name: paramVersion, Type: "string", Description: "Install exactly this version-release rather than whatever is current. A package already installed at a different version is upgraded or downgraded to match."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The package this task acted on."},
			{Name: statVersion, Type: "string", Returned: "always", Description: "The version-release left installed after this task."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What rpm reported about the package before this task and after it, each holding installed and version. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
		},
		Examples: []collection.Example{
			{
				Name:        "Install a package",
				RunbookYAML: "- name: Make sure curl is installed\n  pkg.dnf.install:\n    name: curl\n",
			},
			{
				Name:        "Pin an exact version",
				RunbookYAML: "- name: Install a specific nginx build\n  pkg.dnf.install:\n    name: nginx\n    version: 1.24.0-1.el9\n",
			},
		},
		SeeAlso: []string{"pkg.dnf.remove", "pkg.dnf.upgrade", "pkg.apt.install", "pkg.install"},
	}
}

// Install implements "pkg.dnf.install".
//
// A package already present is left alone and the task reports no
// change, UNLESS version names a version the installed one does not
// match, in which case dnf is asked to install that exact
// name-version, which upgrades or downgrades it as needed.
func Install(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return install(ctx, rc, device, params, collection.ModeExecute)
}

// CheckInstall is pkg.dnf.install's check: the same rpm read and change decision as Install, through the one body both share, then a prediction instead of dnf (recordPrediction).
func CheckInstall(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return install(ctx, rc, device, params, collection.ModeCheck)
}

// install is Install's and CheckInstall's one body; mode says which.
func install(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "pkg.dnf.install"

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

	before, err := queryRPM(ctx, conn, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if mode == collection.ModeCheck {
		if before.installed && (version == "" || before.version == version) {
			return collection.Result{}, recordState(rc, name, before, before)
		}
		if err := recordPrediction(rc, name, before, true, version); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{Changed: true}, nil
	}

	changed := false
	if !before.installed || (version != "" && before.version != version) {
		pkgArg := name
		if version != "" {
			pkgArg = name + "-" + version
		}
		if _, err := runDnf(ctx, conn, "install", "-y", pkgArg); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		changed = true
	}

	after := before
	if changed {
		if after, err = queryRPM(ctx, conn, name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	if err := recordState(rc, name, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if changed && !before.installed {
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:   "pkg.dnf.remove",
			Params: map[string]any{paramName: name},
			Description: fmt.Sprintf("Remove %s, which this task installed. Anything its scriptlets did beyond "+
				"placing its own files (a service they started, a user they created) is not undone.", name),
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}
