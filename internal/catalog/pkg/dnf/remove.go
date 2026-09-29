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
		Name: "pkg.dnf.remove",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameDnf},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			// It asks rpm before it acts, so a check can predict through the same code (CheckRemove).
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that removed a present package emits a pkg.dnf.install pinned to the exact " +
					"version-release this run captured before removing it, which is a real, restorable inverse: " +
					"reinstalling that build undoes exactly what this run did to the rpm database. A run that found " +
					"the package already absent emits nothing. What the inverse cannot restore is anything the " +
					"removal's own scriptlets did beyond deleting the package's files.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "pkg.dnf.install", Record: []string{"name", "version"}},
				},
			},
			Doc: removeDoc(),
		},
		Invoke: Remove,
		Check:  CheckRemove,
	})
}

func removeDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Removes a package via DNF.",
		Description: "Makes sure a package is absent from a Red Hat-family host, removing it if it is present. This is ansible.builtin.dnf with state=absent. Installed state is read from rpm before anything is sent, so a package that is already absent reports no change and no command reaches the device.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The RPM package name to remove."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The package this task acted on."},
			{Name: statVersion, Type: "string", Returned: "always", Description: "Always empty after a successful run: a removed package has no installed version, and a package that was already absent had none either."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What rpm reported about the package before this task and after it, each holding installed and version."},
		},
		Examples: []collection.Example{
			{
				Name:        "Remove a package",
				RunbookYAML: "- name: Make sure telnet is not installed\n  pkg.dnf.remove:\n    name: telnet\n",
			},
		},
		SeeAlso: []string{"pkg.dnf.install", "pkg.dnf.upgrade", "pkg.apt.remove", "pkg.remove"},
	}
}

// Remove implements "pkg.dnf.remove".
//
// A package already absent is left alone and the task reports no
// change. The version captured before removal is what makes this
// method's inverse a real one rather than a guess.
func Remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return remove(ctx, rc, device, params, collection.ModeExecute)
}

// CheckRemove is pkg.dnf.remove's check: the same rpm read and change decision as Remove, through the one body both share, then a prediction (not installed) instead of dnf.
func CheckRemove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return remove(ctx, rc, device, params, collection.ModeCheck)
}

// remove is Remove's and CheckRemove's one body; mode says which.
func remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "pkg.dnf.remove"

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

	if mode == collection.ModeCheck {
		if !before.installed {
			return collection.Result{}, recordState(rc, name, before, before)
		}
		if err := recordPrediction(rc, name, before, false, ""); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{Changed: true}, nil
	}

	changed := false
	if before.installed {
		if _, err := runDnf(ctx, conn, "remove", "-y", name); err != nil {
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

	if changed {
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:   "pkg.dnf.install",
			Params: map[string]any{paramName: name, paramVersion: before.version},
			Description: fmt.Sprintf("Reinstall %s at version %s, which this task removed. Anything the removal's "+
				"own scriptlets did beyond deleting the package's files is not undone.", name, before.version),
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}
