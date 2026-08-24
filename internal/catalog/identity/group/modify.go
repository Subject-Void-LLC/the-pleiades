package group

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
		Name: "identity.group.modify",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NamePosixAccount},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that converged the gid emits an identity.group.modify pinned to the old gid, which " +
					"is a real, restorable inverse. A run that found the gid already matching emits nothing.",
			},
			Doc: modifyDoc(),
		},
		Invoke: Modify,
	})
}

func modifyDoc() collection.Doc {
	return collection.Doc{
		Summary: "Changes the gid of an existing POSIX group on the target.",
		Description: "Converges an existing group's gid, refusing outright if the group does not exist rather " +
			"than creating one (use identity.group.create for that). A POSIX group has no other mutable " +
			"attribute this platform manages: renaming is not something groupmod supports and " +
			"ansible.builtin.group does not offer it either, and membership is identity.user.*'s own concern. " +
			"Group state is read from getent before anything is sent, so a gid already matching what was " +
			"requested reports no change and no command reaches the device.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The group to modify. Must already exist."},
			{Name: paramGID, Type: "int", Required: true, Description: "Converge the group to this numeric gid."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The group this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What getent reported about the group before this task and after it, each holding exists and gid."},
		},
		Examples: []collection.Example{
			{
				Name:        "Change a group's gid",
				RunbookYAML: "- name: Renumber admins\n  identity.group.modify:\n    name: admins\n    gid: 6000\n",
			},
		},
		SeeAlso: []string{"identity.group.create", "identity.group.remove"},
	}
}

// Modify implements "identity.group.modify".
//
// Unlike Create, an absent group is a refusal rather than an implicit
// creation.
func Modify(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "identity.group.modify"

	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	gid, gidSet, err := sdk.IntParam(params, paramGID)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, paramGID, err)
	}
	if !gidSet {
		return collection.Result{}, fmt.Errorf("%s: %s is required", fqcn, paramGID)
	}

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	before, err := queryGroup(ctx, conn, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !before.exists {
		return collection.Result{}, fmt.Errorf("%s: %s: no such group", fqcn, name)
	}

	changed := gid != before.gid
	if changed {
		if _, err := runGroupmod(ctx, conn, name, gid); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	after := before
	if changed {
		if after, err = queryGroup(ctx, conn, name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	if err := recordState(rc, name, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if changed {
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:        "identity.group.modify",
			Params:      map[string]any{paramName: name, paramGID: before.gid},
			Description: fmt.Sprintf("Restore %s's previous gid %d.", name, before.gid),
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}
