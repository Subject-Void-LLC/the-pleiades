// This file holds the catalog's session-control methods: native-only
// methods that act on the platform's own side of a run rather than on a
// device. pleiades.builtin.connection.reset is Ansible's
// `meta: reset_connection`; pleiades.builtin.project.write keeps a value
// a run read as a file in the project.
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
	{
		Name:          "pleiades.builtin.project.write",
		Site:          collection.SiteController,
		Device:        collection.DeviceNone,
		EngineVersion: engineVersion,
		Doc:           projectWriteDoc,
	},
}

// projectWriteDoc is pleiades.builtin.project.write's reference
// documentation, identical to the copy its implementation registers.
var projectWriteDoc = collection.Doc{
	Summary:     "Writes text a run produced, such as a router's running configuration, to a file in the project.",
	Description: "Writes content to path, a file in the project directory, so a value a task read (a registered result's running_config) can be kept, edited and used as a candidate: net.frr.config's src and net.batfish.check's src read such a file. The path is held to the rules a method reading the project is held to: relative, never leaving the project, never through a symbolic link, never into the project's .pleiades directory (matched by the directory itself, not only its name), and a regular file or none yet. It also never replaces the project's inventory (inventory.yaml and its generated state file), or an external Collections directory or anything in one (its programs and its approval list). The path is the author's choice: an expression in it may read only vars, never a registered result, so a device's output cannot choose which file is replaced. A missing directory on the way is created inside the project. The file is written whole to a temporary file beside it and renamed over it, so it is never seen half written, and the task reports changed only when the bytes or the mode differ. A check reads the file and predicts the same answer. It runs on the command line only (pleiades run and adhoc), which holds the project; a Walk-tier Runner refuses it. It acts on no device and needs no hosts. A configuration read by net.frr.facts or net.ios.* is masked: each secret in it is a pleiades-masked- placeholder, and net.frr.config refuses a candidate holding one, so put each secret back before applying the file. It cannot be undone by a rollback: the file's earlier content is never journaled, since the project is the author's working copy, kept under version control.",
	Params: []collection.Param{
		{Name: "path", Type: "string", Required: true, Description: "The file to write, a slash-separated path relative to the project directory, such as configs/r3.running.frr.conf. An expression in it may read only vars, such as configs/{{ vars.site }}.conf, never nodes, result or error.", Format: collection.ParamFormatProjectPath},
		{Name: "content", Type: "string", Required: true, Description: "The text to write, usually rendered from a registered result, such as \"{{ result.r3.running_config }}\". An empty string writes an empty file."},
		{Name: "mode", Type: "string", Default: "0600", Description: "The file's permission bits as a quoted octal string, such as \"0640\". Only the owner can read the default, since a configuration can hold more than its masked secrets. An execute bit is refused: the file holds text a run read, which is never made a program."},
	},
	Returns: []collection.ReturnField{
		{Name: "path", Type: "string", Returned: "always", Description: "The file written, as given."},
		{Name: "sha256", Type: "string", Returned: "always", Description: "The SHA-256 of the content, in hex: the digest net.batfish.check reports for the same bytes."},
		{Name: "size", Type: "int", Returned: "always", Description: "The content's length in bytes."},
	},
	Examples: []collection.Example{
		{Name: "Keep r3's running configuration as a candidate file", RunbookYAML: "- name: Read r3 as it runs\n  net.frr.facts:\n    target: r3\n    include_config: true\n  register: r3\n- name: Keep it in the project, to edit into a candidate\n  pleiades.builtin.project.write:\n    path: configs/r3.running.frr.conf\n    content: \"{{ result.r3.running_config }}\"\n"},
	},
	SeeAlso: []string{"net.frr.facts", "net.frr.config", "net.batfish.check", "file.copy"},
}
