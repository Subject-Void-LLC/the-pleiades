package user

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
		Name: "identity.user.remove",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NamePosixAccount},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true, Site: collection.SiteTarget, Device: collection.DeviceRequired},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			// It asks getent before it acts, so a check can predict through the same code (CheckRemove).
			SupportsCheck: true,
			// It changes accounts or groups, which a login made before it does not
			// see, so a connection kept open to the device is closed after it.
			EndsLoginSession: true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that removed a present account emits an identity.user.create pinned to the exact " +
					"uid, group, shell, home and comment this run captured before removing it, which is a real, " +
					"restorable inverse for the account's identity attributes. A run that found the account " +
					"already absent emits nothing. What the inverse cannot restore is the account's password " +
					"(never captured by this platform), or the home directory's contents once remove=true has " +
					"asked userdel to delete them.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "identity.user.create", Record: []string{"name", "uid", "group", "shell", "home"}, Withhold: []string{"comment"}, MayBePartial: true},
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
		Summary: "Removes a POSIX user account from the target.",
		Description: "Makes sure a user account is absent from the target, removing it if present. This is " +
			"ansible.builtin.user with state=absent. Account state is read from getent before anything is " +
			"sent, so an account already absent reports no change and no command reaches the device.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The account name to remove."},
			{Name: paramRemove, Type: "bool", Default: "false", Description: "Also delete the home directory and mail spool (userdel -r). Left false, they are left on disk."},
			{Name: paramRemoveHomeIfUnchanged, Type: "bool", Default: "false", Description: "Delete the home directory and mail spool only while the home holds exactly what useradd copied into it from the skeleton directory, every file unchanged; otherwise keep it and say so in a warning. identity.user.create's undo sets it for a home that run made. Not with remove."},
			{Name: paramCreatedHome, Type: "string", Description: "With remove_home_if_unchanged, the home the account was made with: the home is deleted only while the account's home is still this path, so a home moved since (identity.user.modify) is never judged in its place. identity.user.create's undo sets it."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The account this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What getent reported about the account before this task and after it. After always reports exists: false on a successful run."},
		},
		Examples: []collection.Example{
			{
				Name:        "Remove an account",
				RunbookYAML: "- name: Make sure the old deploy account is gone\n  identity.user.remove:\n    name: deploy\n",
			},
			{
				Name:        "Remove an account and its home directory",
				RunbookYAML: "- name: Remove deploy entirely\n  identity.user.remove:\n    name: deploy\n    remove: true\n",
			},
		},
		SeeAlso: []string{"identity.user.create", "identity.user.modify"},
	}
}

// Remove implements "identity.user.remove".
//
// An account already absent is left alone and the task reports no
// change. The attributes captured before removal are what make this
// method's inverse a real one rather than a guess.
func Remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return remove(ctx, rc, device, params, collection.ModeExecute)
}

// CheckRemove is identity.user.remove's check: the same getent read and change decision as Remove, through the one body both share, then a prediction (no account) instead of userdel.
func CheckRemove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return remove(ctx, rc, device, params, collection.ModeCheck)
}

// remove is Remove's and CheckRemove's one body; mode says which.
func remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "identity.user.remove"

	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	removeHome, err := sdk.BoolParamOr(params, paramRemove, false)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, paramRemove, err)
	}
	ifUnchanged, err := sdk.BoolParamOr(params, paramRemoveHomeIfUnchanged, false)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, paramRemoveHomeIfUnchanged, err)
	}
	if removeHome && ifUnchanged {
		return collection.Result{}, fmt.Errorf("%s: %s and %s cannot both be set: one always deletes the home, the other only an untouched one", fqcn, paramRemove, paramRemoveHomeIfUnchanged)
	}
	createdHome := sdk.StringParam(params, paramCreatedHome)
	if createdHome != "" && !ifUnchanged {
		return collection.Result{}, fmt.Errorf("%s: %s is read only with %s", fqcn, paramCreatedHome, paramRemoveHomeIfUnchanged)
	}

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	before, err := queryUser(ctx, conn, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	// An untouched home goes with the account; one in use since stays,
	// as file.remove keeps a directory something was put in.
	var warnings []string
	if before.exists && ifUnchanged && createdHome != "" && before.home != createdHome {
		// The account's home was moved after the run that made it. The home
		// that run made is not the one passwd names now, and the one it
		// names is not this undo's to judge. In a rollback the undo of the
		// move runs first and puts it back, which a check cannot see.
		if mode == collection.ModeCheck {
			warnings = append(warnings, fmt.Sprintf("would keep the home: the account's home is %s, not the %s it was made with, unless a step before this one moves it back", before.home, createdHome))
		} else {
			warnings = append(warnings, fmt.Sprintf("kept the home: the account's home is %s, not the %s it was made with", before.home, createdHome))
		}
		ifUnchanged = false
	}
	if before.exists && ifUnchanged {
		_, skel, err := useraddDefaults(ctx, conn)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		state, err := homeState(ctx, conn, before.home, skel)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		switch state {
		case homeUnchanged:
			removeHome = true
		case homeChanged:
			// A check sees the home as it is now. In a rollback the undos
			// before this one have not run yet, and they may remove the very
			// files that make it differ, so the check says it would keep the
			// home unless they do, not that it will.
			if mode == collection.ModeCheck {
				warnings = append(warnings, fmt.Sprintf("would keep %s unless a step before this one removes what differs: it holds files useradd did not put there, or ones changed since", before.home))
			} else {
				warnings = append(warnings, fmt.Sprintf("kept %s: it holds files useradd did not put there, or ones changed since", before.home))
			}
		}
	}

	if mode == collection.ModeCheck {
		if err := recordState(rc, name, before, account{}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		if err := sdk.RecordWarnings(rc, warnings); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{Changed: before.exists}, nil
	}

	changed := false
	if before.exists {
		if _, err := runUserdel(ctx, conn, name, removeHome); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		changed = true
		// The account is gone now, so its undo is recorded before anything
		// else can fail and leave the removal with no way back.
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN: "identity.user.create",
			// The password and any removed home directory's contents are
			// not restored.
			Partial: true,
			Params: map[string]any{
				paramName:    name,
				paramUID:     before.uid,
				paramGroup:   strconv.Itoa(before.gid),
				paramShell:   before.shell,
				paramHome:    before.home,
				paramComment: before.comment,
			},
			Description: fmt.Sprintf("Recreate %s with its previous uid, group, shell, home and comment. Its "+
				"password (never captured by this platform) and, if remove=true asked userdel to delete its "+
				"home directory, that directory's contents are not restored.", name),
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	after := before
	if changed {
		after = account{}
	}

	if err := recordState(rc, name, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := sdk.RecordWarnings(rc, warnings); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	return collection.Result{Changed: changed}, nil
}
