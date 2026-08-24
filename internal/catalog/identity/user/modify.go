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
		Name: "identity.user.modify",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NamePosixAccount},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that converged one or more attributes emits an identity.user.modify pinned to " +
					"exactly the old values of the attributes it changed, which is a real, restorable inverse. A " +
					"run that found every requested attribute already matching emits nothing.",
			},
			Doc: modifyDoc(),
		},
		Invoke: Modify,
	})
}

func modifyDoc() collection.Doc {
	return collection.Doc{
		Summary: "Changes attributes of an existing POSIX user account on the target.",
		Description: "Converges an existing account's uid, primary group, shell, home or comment to whichever " +
			"of those the runbook names, refusing outright if the account does not exist rather than creating " +
			"one (use identity.user.create for that). Account state is read from getent before anything is " +
			"sent, so an attribute already matching what was requested is left alone, and a run that requests " +
			"nothing different reports no change and no command reaches the device.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The account to modify. Must already exist."},
			{Name: paramUID, Type: "int", Description: "Converge the account to this numeric user ID."},
			{Name: paramGroup, Type: "string", Description: "Converge the account's primary group, by name or numeric gid."},
			{Name: paramShell, Type: "string", Description: "Converge the account's login shell."},
			{Name: paramHome, Type: "string", Description: "Converge the account's home directory path. Its contents are not moved."},
			{Name: paramComment, Type: "string", Description: "Converge the account's GECOS comment field."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The account this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What getent reported about the account before this task and after it, each holding exists, uid, gid, comment, home and shell."},
		},
		Examples: []collection.Example{
			{
				Name:        "Change a login shell",
				RunbookYAML: "- name: Switch deploy to a restricted shell\n  identity.user.modify:\n    name: deploy\n    shell: /usr/sbin/nologin\n",
			},
		},
		SeeAlso: []string{"identity.user.create", "identity.user.remove"},
	}
}

// Modify implements "identity.user.modify".
//
// Unlike Create, an absent account is a refusal rather than an implicit
// creation: this method is for changing a known-existing account's
// attributes, not for deciding whether one should exist at all.
func Modify(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "identity.user.modify"

	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	d, err := parseDesired(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
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
	if !before.exists {
		return collection.Result{}, fmt.Errorf("%s: %s: no such user", fqcn, name)
	}

	usermodArgs, oldValues, err := converge(ctx, conn, before, d)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	changed := len(usermodArgs) > 0
	if changed {
		if _, err := runUsermod(ctx, conn, name, usermodArgs); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
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
		invParams := oldValues
		invParams[paramName] = name
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:   "identity.user.modify",
			Params: invParams,
			Description: fmt.Sprintf("Restore %s's previous uid/group/shell/home/comment for whichever of those "+
				"this task changed.", name),
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}
