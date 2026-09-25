// This file holds the catalog's session-control methods: native-only
// methods that act on how the platform reaches a device rather than on
// the device itself. pleiades.builtin.connection.reset is Ansible's
// `meta: reset_connection`.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var sessionCollections = []collectionscaffold.Config{
	{
		Name:          "pleiades.builtin.connection.reset",
		Capabilities:  []capability.Name{capability.NameSSHTransport},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Closes the SSH connection kept open to the device, so its next task logs in again.",
			Description: "A run keeps one SSH connection per device open between tasks unless persist_connections is turned off. A login made before a change to the account it logs in as does not see that change: a group the account was just added to, a new shell, a changed limit. The identity methods close the connection themselves after they run, so this is for the change they cannot see, such as a group membership edited by exec.command. It is Ansible's meta: reset_connection, and migrate-playbook converts that to it. It touches nothing on the device, so it never reports changed, and a check runs it as it is. Where connections do not persist it does nothing, since every task already logs in afresh.",
			Examples: []collection.Example{
				{
					Name:        "Pick up a group membership in the next task",
					RunbookYAML: "- name: Add deploy to the docker group\n  exec.command:\n    cmd: usermod -aG docker deploy\n- name: Log in again, so the new group applies\n  pleiades.builtin.connection.reset: {}\n- name: Use docker as deploy\n  exec.command:\n    cmd: docker ps\n",
				},
			},
			SeeAlso: []string{"identity.user.modify", "identity.group.modify", "exec.command"},
		},
	},
}
