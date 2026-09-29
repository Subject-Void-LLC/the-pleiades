package group

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "identity.group.create",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NamePosixAccount},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true, Site: collection.SiteTarget, Device: collection.DeviceRequired},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			// It asks getent before it acts, so a check can predict through the same code (CheckCreate).
			SupportsCheck: true,
			// It changes accounts or groups, which a login made before it does not
			// see, so a connection kept open to the device is closed after it.
			EndsLoginSession: true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that created an absent group emits an identity.group.remove naming it. A run that " +
					"found the group already present but converged its gid emits an identity.group.modify pinned " +
					"to the old gid, which is a real, restorable inverse. A run that found the group already at " +
					"the requested gid emits nothing.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "identity.group.remove", Record: []string{"name"}},
					{FQCN: "identity.group.modify", Record: []string{"name", "gid"}},
				},
			},
			Doc: createDoc(),
		},
		Invoke: Create,
		Check:  CheckCreate,
	})
}

func createDoc() collection.Doc {
	return collection.Doc{
		Summary: "Makes sure a POSIX group exists on the target.",
		Description: "Makes sure a group is present on the target, creating it if absent. This is " +
			"ansible.builtin.group with state=present. Group state is read from getent before anything is " +
			"sent, so a group already present at the requested gid (or present with none requested) reports " +
			"no change and no command reaches the device; a group present at a different gid than requested " +
			"is converged with groupmod rather than recreated.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The group name to create or converge."},
			{Name: paramGID, Type: "int", Description: "The numeric group ID to assign. An existing group with a different gid is converged to this one."},
			{Name: paramSystem, Type: "bool", Default: "false", Description: "Create the group as a system group (groupadd -r). Ignored when the group already exists."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The group this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What getent reported about the group before this task and after it, each holding exists and gid. Recorded even on a run that changed nothing."},
		},
		Examples: []collection.Example{
			{
				Name:        "Create a plain group",
				RunbookYAML: "- name: Make sure admins exists\n  identity.group.create:\n    name: admins\n",
			},
			{
				Name:        "Pin a gid",
				RunbookYAML: "- name: Create a service group\n  identity.group.create:\n    name: appsvc\n    gid: 5000\n    system: true\n",
			},
		},
		SeeAlso: []string{"identity.group.modify", "identity.group.remove", "identity.user.create"},
	}
}

// Create implements "identity.group.create".
//
// An absent group is created fresh with groupadd. A present group is
// left alone unless a requested gid differs from what getent reports,
// in which case groupmod converges it.
func Create(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return create(ctx, rc, device, params, collection.ModeExecute)
}

// CheckCreate is identity.group.create's check: the same getent read and change decision as Create, through the one body both share, then a prediction instead of groupadd or groupmod. A gid the task does not name is the system's to choose, so a new group's is left out of the prediction.
func CheckCreate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return create(ctx, rc, device, params, collection.ModeCheck)
}

// create is Create's and CheckCreate's one body; mode says which.
func create(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "identity.group.create"

	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	gid, gidSet, err := sdk.IntParam(params, paramGID)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, paramGID, err)
	}
	system, err := sdk.BoolParamOr(params, paramSystem, false)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, paramSystem, err)
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

	if mode == collection.ModeCheck {
		changed := !before.exists || (gidSet && gid != before.gid)
		after := before.Map()
		if changed {
			after = map[string]any{"exists": true}
			if gidSet {
				after["gid"] = gid
			}
		}
		if err := recordPrediction(rc, name, before, after); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{Changed: changed}, nil
	}

	changed := false
	if !before.exists {
		args := []string{"groupadd"}
		if gidSet {
			args = append(args, "-g", strconv.Itoa(gid))
		}
		if system {
			args = append(args, "-r")
		}
		args = append(args, name)
		if _, err := runGroupadd(ctx, conn, args...); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		changed = true
	} else if gidSet && gid != before.gid {
		if _, err := runGroupmod(ctx, conn, name, gid); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		changed = true
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
		if !before.exists {
			if err := sdk.RecordInverse(rc, sdk.Inverse{
				FQCN:   "identity.group.remove",
				Params: map[string]any{paramName: name},
				Description: fmt.Sprintf("Remove %s, which this task created. Any user whose primary or "+
					"supplementary membership was set to it in the meantime is not undone.", name),
			}); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		} else {
			if err := sdk.RecordInverse(rc, sdk.Inverse{
				FQCN:        "identity.group.modify",
				Params:      map[string]any{paramName: name, paramGID: before.gid},
				Description: fmt.Sprintf("Restore %s's previous gid %d.", name, before.gid),
			}); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
	}

	return collection.Result{Changed: changed}, nil
}
