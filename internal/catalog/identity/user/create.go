package user

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
		Name: "identity.user.create",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NamePosixAccount},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
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
				Notes: "A run that created an absent account emits an identity.user.remove naming it. A run that " +
					"found the account already present but converged one or more attributes (uid, group, shell, " +
					"home or comment) emits an identity.user.modify pinned to exactly the old values of the " +
					"attributes it changed, which is a real, restorable inverse. A run that found the account " +
					"already exactly as requested emits nothing. What no inverse here can restore is the account's " +
					"password, or anything a login shell or profile script did while the account existed.",
			},
			Doc: createDoc(),
		},
		Invoke: Create,
		Check:  CheckCreate,
	})
}

func createDoc() collection.Doc {
	return collection.Doc{
		Summary: "Makes sure a POSIX user account exists on the target.",
		Description: "Makes sure a user account is present on the target, creating it if absent. This is " +
			"ansible.builtin.user with state=present. Account state is read from getent before anything is " +
			"sent, so an account already present with every requested attribute already matching reports no " +
			"change and no command reaches the device; an account present with a different uid, group, shell, " +
			"home or comment than requested is converged with usermod rather than recreated. Supplementary " +
			"group membership and the account password are not managed by this method.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The account name to create or converge."},
			{Name: paramUID, Type: "int", Description: "The numeric user ID to assign. An existing account with a different uid is converged to this one."},
			{Name: paramGroup, Type: "string", Description: "The primary group, by name or numeric gid. An existing account with a different primary group is converged to this one."},
			{Name: paramShell, Type: "string", Description: "The login shell, such as /bin/bash. An existing account with a different shell is converged to this one."},
			{Name: paramHome, Type: "string", Description: "The home directory path. An existing account with a different home is converged to this one; its contents are not moved."},
			{Name: paramComment, Type: "string", Description: "The GECOS comment field, typically the account's full name."},
			{Name: paramCreateHome, Type: "bool", Default: "true", Description: "Create the home directory when the account is created. Ignored when the account already exists."},
			{Name: paramSystem, Type: "bool", Default: "false", Description: "Create the account as a system account (useradd -r). Ignored when the account already exists."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The account this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What getent reported about the account before this task and after it, each holding exists, uid, gid, comment, home and shell. Recorded even on a run that changed nothing."},
		},
		Examples: []collection.Example{
			{
				Name:        "Create a plain account",
				RunbookYAML: "- name: Make sure deploy exists\n  identity.user.create:\n    name: deploy\n",
			},
			{
				Name:        "Pin uid and shell",
				RunbookYAML: "- name: Create a service account\n  identity.user.create:\n    name: appsvc\n    uid: 5000\n    shell: /usr/sbin/nologin\n    system: true\n",
			},
		},
		SeeAlso: []string{"identity.user.modify", "identity.user.remove", "identity.group.create"},
	}
}

// Create implements "identity.user.create".
//
// An absent account is created fresh with useradd from whichever
// attributes the runbook named. A present account is left alone unless
// one of those same attributes differs from what getent reports, in
// which case usermod converges exactly the attributes that differ and
// no others.
func Create(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return create(ctx, rc, device, params, collection.ModeExecute)
}

// CheckCreate is identity.user.create's check: the same getent reads and change decision as Create, through the one body both share, then a prediction (predictAccount) instead of useradd or usermod.
func CheckCreate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return create(ctx, rc, device, params, collection.ModeCheck)
}

// create is Create's and CheckCreate's one body; mode says which.
func create(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "identity.user.create"

	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	d, err := parseDesired(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	createHome, err := sdk.BoolParamOr(params, paramCreateHome, true)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, paramCreateHome, err)
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

	before, err := queryUser(ctx, conn, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if mode == collection.ModeCheck {
		changed := !before.exists
		if before.exists {
			args, _, err := converge(ctx, conn, before, d)
			if err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
			changed = len(args) > 0
		}
		after := before.Map()
		if changed {
			if after, err = predictAccount(ctx, conn, before, d); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
		if err := recordPrediction(rc, name, before, after); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{Changed: changed}, nil
	}

	var changed bool
	var usermodArgs []string
	var oldValues map[string]any
	if !before.exists {
		if _, err := runUseradd(ctx, conn, useraddArgs(name, d, createHome, system)...); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		changed = true
	} else {
		if usermodArgs, oldValues, err = converge(ctx, conn, before, d); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		if len(usermodArgs) > 0 {
			if _, err := runUsermod(ctx, conn, name, usermodArgs); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
			changed = true
		}
	}

	after := before
	if changed {
		if after, err = queryUser(ctx, conn, name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	if err := recordState(rc, name, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if changed {
		if !before.exists {
			if err := sdk.RecordInverse(rc, sdk.Inverse{
				FQCN:   "identity.user.remove",
				Params: map[string]any{paramName: name},
				Description: fmt.Sprintf("Remove %s, which this task created. Its password (never set by this "+
					"task) and anything a login shell or profile script did while it existed are not undone.", name),
			}); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		} else {
			invParams := oldValues
			invParams[paramName] = name
			if err := sdk.RecordInverse(rc, sdk.Inverse{
				FQCN:   "identity.user.modify",
				Params: invParams,
				Description: fmt.Sprintf("Restore %s's previous uid/group/shell/home/comment for whichever of "+
					"those this task changed.", name),
			}); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
	}

	return collection.Result{Changed: changed}, nil
}
