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
		Name: "identity.group.remove",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NamePosixAccount},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that removed a present group emits an identity.group.create pinned to the exact " +
					"gid this run captured before removing it, which is a real, restorable inverse. A run that " +
					"found the group already absent emits nothing. What the inverse cannot restore is any user's " +
					"primary or supplementary membership in the group at the moment it was removed.",
			},
			Doc: removeDoc(),
		},
		Invoke: Remove,
	})
}

func removeDoc() collection.Doc {
	return collection.Doc{
		Summary: "Removes a POSIX group from the target.",
		Description: "Makes sure a group is absent from the target, removing it if present. This is " +
			"ansible.builtin.group with state=absent. Group state is read from getent before anything is " +
			"sent, so a group already absent reports no change and no command reaches the device. groupdel " +
			"itself refuses to remove a group that is still any user's primary group; that refusal surfaces " +
			"here as a plain task failure naming what groupdel said, not something this method works around.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The group name to remove."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The group this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What getent reported about the group before this task and after it. After always reports exists: false on a successful run."},
		},
		Examples: []collection.Example{
			{
				Name:        "Remove a group",
				RunbookYAML: "- name: Make sure the old admins group is gone\n  fqcn: identity.group.remove\n  params:\n    name: admins\n",
			},
		},
		SeeAlso: []string{"identity.group.create", "identity.group.modify"},
	}
}

// Remove implements "identity.group.remove".
//
// A group already absent is left alone and the task reports no change.
// The gid captured before removal is what makes this method's inverse a
// real one rather than a guess.
func Remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "identity.group.remove"

	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
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

	changed := false
	if before.exists {
		if _, err := runGroupdel(ctx, conn, name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		changed = true
	}

	after := before
	if changed {
		after = groupAccount{}
	}

	if err := recordState(rc, name, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if changed {
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:        "identity.group.create",
			Params:      map[string]any{paramName: name, paramGID: before.gid},
			Description: fmt.Sprintf("Recreate %s at its previous gid %d. Any user's membership in it is not restored.", name, before.gid),
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}
