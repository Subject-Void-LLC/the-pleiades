package legacy

import (
	"encoding/json"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// This file's inventory shape was chosen empirically, not from PLAN.md
// Section 23.2's own text alone. That section describes "inventory.json"
// as if any JSON document handed to `ansible-playbook -i` is
// interchangeable, but Ansible's real inventory plugin auto-detection
// (verified directly against ansible-core 2.19.11 in this repository's
// own development environment) does NOT read a plain, non-executable
// ".json" file through the "script" plugin's flat
// {"<group>": {"hosts": [...]}, "_meta": {"hostvars": {...}}} contract at
// all -- that shape is for an executable inventory script Ansible runs
// and captures the stdout of, and a static file carrying it instead
// produces real, observed parse failures ("Invalid \"hosts\" entry for
// ... group, requires a dictionary, found ... list").
//
// What actually works, confirmed by really running ansible-playbook
// against it: Ansible's "yaml" inventory plugin accepts a plain JSON file
// (JSON is valid YAML) written in the YAML inventory schema instead --
// nested groups under "all.children", each carrying a "hosts" object
// keyed by hostname, whose value is that host's own vars, with an
// ungrouped host placed directly under "all.hosts". A host listed under
// more than one group only needs its full vars attached once; this
// package attaches them to every group the host is in anyway (repeating a
// small, in-memory map costs nothing here, since Phase 17's dispatch
// model produces exactly one host per generated inventory) rather than
// building the extra indirection a single source of truth would need for
// no real benefit at this scale.

// sshPrivateKeyContainerPath is the fixed in-container path
// Adapter.Execute writes a device's SSH private key to (via
// ContainerSpec.Files, when payload.Secrets carries one), and the same
// path buildHostvars references from ansible_ssh_private_key_file. A
// single named constant, not two independently hand-typed string
// literals, so the two can never drift apart.
const sshPrivateKeyContainerPath = "/run/pleiades/id_key"

// ErrPassphraseProtectedKey is returned by BuildInventoryJSON when
// payload.Secrets carries a private key passphrase. Ansible's SSH
// connection plugin has no way to supply one non-interactively without an
// ssh-agent bridge this package does not build (a named, deliberate Phase
// 17 limitation, not an oversight): failing the dispatch with a clear
// error is preferable to a container that silently hangs at an
// interactive passphrase prompt it can never receive.
var ErrPassphraseProtectedKey = fmt.Errorf("legacy: passphrase-protected private keys are not supported")

// ansibleInventory is the root of the generated inventory document. Its
// JSON shape is Ansible's own "yaml" inventory plugin schema (see this
// file's own top-of-file comment for why, verified empirically), not the
// dynamic-inventory-script "_meta" shape PLAN.md's own prose might
// otherwise suggest.
type ansibleInventory struct {
	All ansibleAllGroup `json:"all"`
}

// ansibleAllGroup is the "all" group every Ansible inventory implicitly
// has: Hosts holds any ungrouped host directly, Children holds one entry
// per real Ansible group (one per payload.Tags value).
type ansibleAllGroup struct {
	Hosts    map[string]map[string]any `json:"hosts,omitempty"`
	Children map[string]ansibleGroup   `json:"children,omitempty"`
}

// ansibleGroup is one named group's own host membership.
type ansibleGroup struct {
	Hosts map[string]map[string]any `json:"hosts"`
}

// BuildInventoryJSON builds a single-host Ansible inventory document from
// payload, the shape internal/adapters/legacy's own file-level doc
// comment already explains the dispatch-model reasoning for (one
// container, one device, per existing runner.Agent dispatch model).
// payload.Tags becomes real Ansible groups (verified against this
// codebase's own worked example: examples/upgrade_ios/pleiades/
// inventory.yaml's "tags: [catalyst_lab]" pairs with
// examples/upgrade_ios/ansible/inventory.ini's "[catalyst_lab]" group
// header), and payload.Capabilities/Tags are also attached redundantly as
// pleiades_capabilities/pleiades_tags hostvars, so a task can read either
// Ansible's own group_names or the explicit hostvar.
func BuildInventoryJSON(payload wire.DispatchPayload) ([]byte, error) {
	if _, ok := payload.Secrets[credential.SecretPassphrase]; ok {
		return nil, ErrPassphraseProtectedKey
	}

	vars := hostvars(payload)

	group := ansibleAllGroup{}
	if len(payload.Tags) == 0 {
		group.Hosts = map[string]map[string]any{payload.DeviceName: vars}
	} else {
		group.Children = make(map[string]ansibleGroup, len(payload.Tags))
		for _, tag := range payload.Tags {
			group.Children[tag] = ansibleGroup{Hosts: map[string]map[string]any{payload.DeviceName: vars}}
		}
	}

	doc, err := json.Marshal(ansibleInventory{All: group})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal inventory: %w", err)
	}
	return doc, nil
}

// hostvars builds payload's own device's hostvars map. Every value comes
// from a field already on wire.DispatchPayload: this package builds the
// inventory Runner-side, from the wire payload the Controller already
// resolved and attached secrets to, never from a live
// pkg/inventory.InventoryItem (the Runner process has no inventory
// backend of its own to query one from), the same established precedent
// internal/adapters/native's own wireDevice sets.
func hostvars(payload wire.DispatchPayload) map[string]any {
	vars := map[string]any{
		"ansible_host":          payload.DeviceHost,
		"pleiades_device_id":    payload.DeviceID,
		"pleiades_capabilities": capabilityStrings(payload.Capabilities),
		"pleiades_tags":         payload.Tags,
	}
	if payload.SSHPort != 0 {
		vars["ansible_port"] = payload.SSHPort
	}
	if username, ok := payload.Secrets[credential.SecretUsername]; ok {
		vars["ansible_user"] = username
	}
	if password, ok := payload.Secrets[credential.SecretPassword]; ok {
		vars["ansible_password"] = password
	}
	if _, ok := payload.Secrets[credential.SecretPrivateKeyPEM]; ok {
		vars["ansible_ssh_private_key_file"] = sshPrivateKeyContainerPath
	}
	return vars
}

// capabilityStrings converts payload.Capabilities' own []capability.Name
// into []string for JSON encoding as the pleiades_capabilities hostvar. A
// bare slice conversion is not legal Go here, the identical reason
// internal/dispatch's own tagStrings helper exists: capability.Name and
// string are distinct named types even though the underlying type is
// identical.
func capabilityStrings(caps []capability.Name) []string {
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = string(c)
	}
	return out
}
