// This file holds the Identity section of docs/hephaestus.md's catalog:
// ansible.builtin.user and ansible.builtin.group.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var identityCollections = []collectionscaffold.Config{
	{
		Name:              "identity.user.create",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes sure a POSIX user account exists on the target.",
			Description: "Makes sure a user account is present on the target, creating it if absent. This is ansible.builtin.user with state=present. Account state is read from getent before anything is sent, so an account already present with every requested attribute already matching reports no change and no command reaches the device; an account present with a different uid, group, shell, home or comment than requested is converged with usermod rather than recreated. Supplementary group membership and the account password are not managed by this method.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The account name to create or converge."},
				{Name: "uid", Type: "int", Description: "The numeric user ID to assign. An existing account with a different uid is converged to this one."},
				{Name: "group", Type: "string", Description: "The primary group, by name or numeric gid. An existing account with a different primary group is converged to this one."},
				{Name: "shell", Type: "string", Description: "The login shell, such as /bin/bash. An existing account with a different shell is converged to this one."},
				{Name: "home", Type: "string", Description: "The home directory path. An existing account with a different home is converged to this one; its contents are not moved."},
				{Name: "comment", Type: "string", Description: "The GECOS comment field, typically the account's full name."},
				{Name: "create_home", Type: "bool", Default: "true", Description: "Create the home directory when the account is created. Ignored when the account already exists."},
				{Name: "system", Type: "bool", Default: "false", Description: "Create the account as a system account (useradd -r). Ignored when the account already exists."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The account this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What getent reported about the account before this task and after it, each holding exists, uid, gid, comment, home and shell. Recorded even on a run that changed nothing."},
			},
			Examples: []collection.Example{
				{Name: "Create a plain account", RunbookYAML: "- name: Make sure deploy exists\n  identity.user.create:\n    name: deploy\n"},
				{Name: "Pin uid and shell", RunbookYAML: "- name: Create a service account\n  identity.user.create:\n    name: appsvc\n    uid: 5000\n    shell: /usr/sbin/nologin\n    system: true\n"},
			},
			SeeAlso: []string{"identity.user.modify", "identity.user.remove", "identity.group.create"},
		},
	},
	{
		Name:              "identity.user.modify",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Changes attributes of an existing POSIX user account on the target.",
			Description: "Converges an existing account's uid, primary group, shell, home or comment to whichever of those the runbook names, refusing outright if the account does not exist rather than creating one (use identity.user.create for that). Account state is read from getent before anything is sent, so an attribute already matching what was requested is left alone, and a run that requests nothing different reports no change and no command reaches the device.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The account to modify. Must already exist."},
				{Name: "uid", Type: "int", Description: "Converge the account to this numeric user ID."},
				{Name: "group", Type: "string", Description: "Converge the account's primary group, by name or numeric gid."},
				{Name: "shell", Type: "string", Description: "Converge the account's login shell."},
				{Name: "home", Type: "string", Description: "Converge the account's home directory path. Its contents are not moved."},
				{Name: "comment", Type: "string", Description: "Converge the account's GECOS comment field."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The account this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What getent reported about the account before this task and after it, each holding exists, uid, gid, comment, home and shell."},
			},
			Examples: []collection.Example{
				{Name: "Change a login shell", RunbookYAML: "- name: Switch deploy to a restricted shell\n  identity.user.modify:\n    name: deploy\n    shell: /usr/sbin/nologin\n"},
			},
			SeeAlso: []string{"identity.user.create", "identity.user.remove"},
		},
	},
	{
		Name:              "identity.user.remove",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Removes a POSIX user account from the target.",
			Description: "Makes sure a user account is absent from the target, removing it if present. This is ansible.builtin.user with state=absent. Account state is read from getent before anything is sent, so an account already absent reports no change and no command reaches the device.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The account name to remove."},
				{Name: "remove", Type: "bool", Default: "false", Description: "Also delete the home directory and mail spool (userdel -r). Left false, they are left on disk."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The account this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What getent reported about the account before this task and after it. After always reports exists: false on a successful run."},
			},
			Examples: []collection.Example{
				{Name: "Remove an account", RunbookYAML: "- name: Make sure the old deploy account is gone\n  identity.user.remove:\n    name: deploy\n"},
				{Name: "Remove an account and its home directory", RunbookYAML: "- name: Remove deploy entirely\n  identity.user.remove:\n    name: deploy\n    remove: true\n"},
			},
			SeeAlso: []string{"identity.user.create", "identity.user.modify"},
		},
	},
	{
		Name:              "identity.group.create",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes sure a POSIX group exists on the target.",
			Description: "Makes sure a group is present on the target, creating it if absent. This is ansible.builtin.group with state=present. Group state is read from getent before anything is sent, so a group already present at the requested gid (or present with none requested) reports no change and no command reaches the device; a group present at a different gid than requested is converged with groupmod rather than recreated.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The group name to create or converge."},
				{Name: "gid", Type: "int", Description: "The numeric group ID to assign. An existing group with a different gid is converged to this one."},
				{Name: "system", Type: "bool", Default: "false", Description: "Create the group as a system group (groupadd -r). Ignored when the group already exists."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The group this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What getent reported about the group before this task and after it, each holding exists and gid. Recorded even on a run that changed nothing."},
			},
			Examples: []collection.Example{
				{Name: "Create a plain group", RunbookYAML: "- name: Make sure admins exists\n  identity.group.create:\n    name: admins\n"},
				{Name: "Pin a gid", RunbookYAML: "- name: Create a service group\n  identity.group.create:\n    name: appsvc\n    gid: 5000\n    system: true\n"},
			},
			SeeAlso: []string{"identity.group.modify", "identity.group.remove", "identity.user.create"},
		},
	},
	{
		Name:              "identity.group.modify",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Changes the gid of an existing POSIX group on the target.",
			Description: "Converges an existing group's gid, refusing outright if the group does not exist rather than creating one (use identity.group.create for that). A POSIX group has no other mutable attribute this platform manages: renaming is not something groupmod supports and ansible.builtin.group does not offer it either, and membership is identity.user.*'s own concern. Group state is read from getent before anything is sent, so a gid already matching what was requested reports no change and no command reaches the device.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The group to modify. Must already exist."},
				{Name: "gid", Type: "int", Required: true, Description: "Converge the group to this numeric gid."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The group this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What getent reported about the group before this task and after it, each holding exists and gid."},
			},
			Examples: []collection.Example{
				{Name: "Change a group's gid", RunbookYAML: "- name: Renumber admins\n  identity.group.modify:\n    name: admins\n    gid: 6000\n"},
			},
			SeeAlso: []string{"identity.group.create", "identity.group.remove"},
		},
	},
	{
		Name:              "identity.group.remove",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Removes a POSIX group from the target.",
			Description: "Makes sure a group is absent from the target, removing it if present. This is ansible.builtin.group with state=absent. Group state is read from getent before anything is sent, so a group already absent reports no change and no command reaches the device. groupdel itself refuses to remove a group that is still any user's primary group; that refusal surfaces here as a plain task failure naming what groupdel said, not something this method works around.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The group name to remove."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The group this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What getent reported about the group before this task and after it. After always reports exists: false on a successful run."},
			},
			Examples: []collection.Example{
				{Name: "Remove a group", RunbookYAML: "- name: Make sure the old admins group is gone\n  identity.group.remove:\n    name: admins\n"},
			},
			SeeAlso: []string{"identity.group.create", "identity.group.modify"},
		},
	},
}
