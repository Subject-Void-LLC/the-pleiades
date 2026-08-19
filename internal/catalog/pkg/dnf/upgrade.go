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
		Name: "pkg.dnf.upgrade",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameDnf},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "Downgrading a package is not something dnf reliably supports once the previous build has " +
					"left the local cache and the repository's own metadata (a mirror generally keeps only the " +
					"latest build of a package), so no safe inverse can be promised here, for the same reason " +
					"pkg.apt.upgrade cannot promise one.",
			},
			Doc: upgradeDoc(),
		},
		Invoke: Upgrade,
	})
}

func upgradeDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Makes sure a package is at its newest available version via DNF.",
		Description: "Makes sure one named package is at the newest version DNF knows about, upgrading it if a newer build is available. This is ansible.builtin.dnf with state=latest for a single package, not a full-system dnf upgrade: it never touches any package other than the one named. A package that is absent is installed fresh, since there is no \"current\" version to upgrade from. A package that is already at the newest available build reports no change and no command reaches the device.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The RPM package to keep current."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The package this task acted on."},
			{Name: statVersion, Type: "string", Returned: "always", Description: "The version-release left installed after this task."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What rpm reported about the package before this task and after it, each holding installed and version."},
		},
		Examples: []collection.Example{
			{
				Name:        "Keep a package at its newest available version",
				RunbookYAML: "- name: Keep openssl current\n  fqcn: pkg.dnf.upgrade\n  params:\n    name: openssl\n",
			},
		},
		SeeAlso: []string{"pkg.dnf.install", "pkg.dnf.remove", "pkg.apt.upgrade", "pkg.upgrade"},
	}
}

// Upgrade implements "pkg.dnf.upgrade".
//
// Absent means install fresh, matching ansible.builtin.dnf's own
// state=latest semantics. Present means asking dnf check-update whether
// a newer build exists and upgrading only when it says so, so a package
// already current sends nothing beyond the check itself.
func Upgrade(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "pkg.dnf.upgrade"

	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	before, err := queryRPM(ctx, conn, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if !before.installed {
		if _, err := runDnf(ctx, conn, "install", "-y", name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		after, err := queryRPM(ctx, conn, name)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		if err := recordState(rc, name, before, after); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{Changed: true}, nil
	}

	available, err := hasUpdate(ctx, conn, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	changed := false
	after := before
	if available {
		if _, err := runDnf(ctx, conn, "upgrade", "-y", name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		changed = true
		if after, err = queryRPM(ctx, conn, name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	if err := recordState(rc, name, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	return collection.Result{Changed: changed}, nil
}
