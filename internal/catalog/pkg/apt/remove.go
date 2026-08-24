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
		Name: "pkg.apt.remove",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameApt},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that removed a present package emits a pkg.apt.install pinned to the exact version this " +
					"run captured before removing it, which is a real, restorable inverse: reinstalling that version " +
					"undoes exactly what this run did to the package database. A run that found the package already " +
					"absent emits nothing. What the inverse cannot restore is anything the removal's own maintainer " +
					"scripts did beyond deleting the package's files.",
			},
			Doc: removeDoc(),
		},
		Invoke: Remove,
	})
}

func removeDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Removes a package via APT.",
		Description: "Makes sure a package is absent from a Debian-family host, removing it if it is present. This is ansible.builtin.apt with state=absent. Installed state is read from dpkg before anything is sent, so a package that is already absent reports no change and no command reaches the device. This removes the package but does not purge it (apt-get remove, not apt-get purge), so its configuration files are left on disk; there is no separate purge parameter today.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The APT package name to remove."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The package this task acted on."},
			{Name: statVersion, Type: "string", Returned: "always", Description: "Always empty after a successful run: a removed package has no installed version, and a package that was already absent had none either."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What dpkg reported about the package before this task and after it, each holding installed and version."},
		},
		Examples: []collection.Example{
			{
				Name:        "Remove a package",
				RunbookYAML: "- name: Make sure telnet is not installed\n  pkg.apt.remove:\n    name: telnet\n",
			},
		},
		SeeAlso: []string{"pkg.apt.install", "pkg.apt.upgrade", "pkg.dnf.remove", "pkg.remove"},
	}
}

// Remove implements "pkg.apt.remove".
//
// A package already absent is left alone and the task reports no
// change. The version captured before removal is what makes this
// method's inverse a real one rather than a guess.
func Remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "pkg.apt.remove"

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

	changed := false
	if before.installed {
		if _, err := runAptGet(ctx, conn, "remove", "-y", name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		changed = true
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

	if changed {
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:   "pkg.apt.install",
			Params: map[string]any{paramName: name, paramVersion: before.version},
			Description: fmt.Sprintf("Reinstall %s at version %s, which this task removed. Anything the removal's "+
				"own maintainer scripts did beyond deleting the package's files is not undone.", name, before.version),
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}
