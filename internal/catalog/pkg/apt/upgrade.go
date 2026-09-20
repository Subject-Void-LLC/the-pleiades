package apt

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
		Name: "pkg.apt.upgrade",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameApt},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			// It asks dpkg before it acts, so a check can predict through the same code (CheckUpgrade).
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "Downgrading a package is not something apt-get reliably supports once the previous version " +
					"has left the local cache and the repository's own metadata (a mirror generally keeps only the " +
					"latest build of a package), so no safe inverse can be promised here. This mirrors " +
					"exec.command's own reasoning for why it cannot be undone: the platform does not control whether " +
					"the information an undo would need still exists anywhere reachable.",
			},
			Doc: upgradeDoc(),
		},
		Invoke: Upgrade,
		Check:  CheckUpgrade,
	})
}

func upgradeDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Makes sure a package is at its newest available version via APT.",
		Description: "Makes sure one named package is at the newest version APT knows about, upgrading it if a newer one is available. This is ansible.builtin.apt with state=latest for a single package, not apt-get upgrade or apt-get dist-upgrade: it never touches any package other than the one named. A package that is absent is installed fresh, since there is no \"current\" version to upgrade from. A package that is already at the newest candidate version reports no change and no command reaches the device.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The APT package to keep current."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The package this task acted on."},
			{Name: statVersion, Type: "string", Returned: "always", Description: "The version left installed after this task."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What dpkg reported about the package before this task and after it, each holding installed and version."},
		},
		Examples: []collection.Example{
			{
				Name:        "Keep a package at its newest available version",
				RunbookYAML: "- name: Keep openssl current\n  pkg.apt.upgrade:\n    name: openssl\n",
			},
		},
		SeeAlso: []string{"pkg.apt.install", "pkg.apt.remove", "pkg.dnf.upgrade", "pkg.upgrade"},
	}
}

// Upgrade implements "pkg.apt.upgrade".
//
// Absent means install fresh, matching ansible.builtin.apt's own
// state=latest semantics: there being no "current" version, "upgrade"
// and "install" mean the same thing. Present means comparing the
// installed version against apt-cache's own candidate and upgrading
// only when they differ, so a package already at the newest version
// sends nothing.
func Upgrade(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return upgrade(ctx, rc, device, params, collection.ModeExecute)
}

// CheckUpgrade is pkg.apt.upgrade's check: the same dpkg and apt-cache reads and change decision as Upgrade, through the one body both share, then a prediction (the candidate version) instead of apt-get.
func CheckUpgrade(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return upgrade(ctx, rc, device, params, collection.ModeCheck)
}

// upgrade is Upgrade's and CheckUpgrade's one body; mode says which.
func upgrade(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "pkg.apt.upgrade"

	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

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
		predicted := before
		if !before.installed {
			if predicted, err = predictInstall(ctx, conn, name, ""); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		} else {
			candidate, err := queryCandidate(ctx, conn, name)
			if err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
			if candidate != "" && candidate != before.version {
				predicted = state{installed: true, version: candidate}
			}
		}
		if err := recordState(rc, name, before, predicted); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{Changed: predicted != before}, nil
	}

	if !before.installed {
		if _, err := runAptGet(ctx, conn, "install", "-y", name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		after, err := queryDpkg(ctx, conn, name)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		if err := recordState(rc, name, before, after); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{Changed: true}, nil
	}

	candidate, err := queryCandidate(ctx, conn, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	changed := false
	after := before
	if candidate != "" && candidate != before.version {
		if _, err := runAptGet(ctx, conn, "install", "-y", "--only-upgrade", name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		changed = true
		if after, err = queryDpkg(ctx, conn, name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	if err := recordState(rc, name, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	// No inverse: Reversible is false on this method's own manifest, and
	// that holds for every path through here, including the fresh-install
	// path above, since a package that arrived via "upgrade" is in the
	// same undo-less position as one that arrived via "install" at no
	// pinned version once a newer build has superseded it in the repo.
	return collection.Result{Changed: changed}, nil
}
