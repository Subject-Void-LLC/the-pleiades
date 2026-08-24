// This file holds the Network devices section of docs/hephaestus.md's
// catalog: ansible.netcommon.cli_command/cli_config/netconf_config, and
// the three vendor-specific config modules (cisco.ios, junipernetworks.junos,
// arista.eos). RequiresElevation does not map cleanly onto a network OS's
// own enable-mode privilege model, so every entry here leaves it false
// rather than asserting a Linux-specific semantic onto network gear.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var networkCollections = []collectionscaffold.Config{
	{
		Name:          "net.cli.command",
		Capabilities:  []capability.Name{capability.NameNetworkCLI},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Runs one show/exec-mode command against a network device's CLI.",
			Description: "Opens an interactive PTY session over SSH and runs one command, matched against the device's own declared cli_prompt property (a generic Dialect, built by netcli.FromPrompt, with no vendor-specific paging, configuration-mode or error convention of its own). Reports the command's own output, with its echoed input line and the trailing prompt both stripped. A command cannot be inspected, so this reports changed every time it reaches the device without error, the same convention exec.command established: pair it with when/when_or/when_cel when idempotence matters. Refuses outright when the target device's cli_prompt property is unset, rather than guessing at a prompt shape. Because this method has no known paging convention for a generic device, a command whose output is longer than the device's own terminal length can pause on a pager prompt this method cannot answer (verified directly against a real device); each command is bounded to 30 seconds so that failure is a clear, timely error rather than an indefinite hang. Pipe a long-output command through the device's own output filter (e.g. \"show running-config | include hostname\") to avoid triggering it, or use net.ios.config, whose vendor-specific dialect disables paging for real.",
			Params: []collection.Param{
				{Name: "command", Type: "string", Required: true, Description: "The single CLI line to run, e.g. \"show version\"."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "stdout", Type: "string", Returned: "always", Description: "Everything the device printed in response to the command, with the echoed command line and the trailing prompt removed."},
			},
			Examples: []collection.Example{
				{
					Name:        "Read a device's software version",
					RunbookYAML: "- name: Check the running version\n  net.cli.command:\n    command: show version\n  register: version\n",
				},
			},
			SeeAlso: []string{"net.cli.config", "net.ios.config"},
		},
	},
	{
		Name:          "net.cli.config",
		Capabilities:  []capability.Name{capability.NameNetworkCLI},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Applies configuration lines to a network device over its CLI.",
			Description: "Splits config into non-blank lines and sends each one through the same generic session net.cli.command uses, one line at a time, matched against the device's own declared cli_prompt property. It carries no vendor-specific configuration-mode knowledge of its own: it never enters or leaves a configuration mode on the caller's behalf, so a device that needs one (Cisco IOS's \"configure terminal\"/\"end\", for instance) must have those lines included in config itself. net.ios.config is the Cisco-specific sibling that does drive configuration mode, and is what a runbook targeting Cisco IOS should use instead. Reports changed every time every line reaches the device without error, the same convention net.cli.command and exec.command both establish: a CLI line's effect cannot be inspected, so this platform does not guess at one. Each line is bounded to 30 seconds for the same reason net.cli.command's own line is: a generic device has no known paging convention, so an unexpectedly long response can pause on a pager prompt this method cannot answer, and a bounded, clear error is preferable to an indefinite hang.",
			Params: []collection.Param{
				{Name: "config", Type: "string", Required: true, Description: "The configuration lines to send, one per line. Blank lines are skipped. Include any vendor-specific mode commands (e.g. \"configure terminal\" and \"end\") this device needs, since this method sends none on its own."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Examples: []collection.Example{
				{
					Name:        "Apply configuration on a device with no vendor-specific method yet",
					RunbookYAML: "- name: Set a banner\n  net.cli.config:\n    config: |\n      configure terminal\n      banner motd ^Cauthorized access only^C\n      end\n",
				},
			},
			SeeAlso: []string{"net.cli.command", "net.ios.config"},
		},
	},
	// net.ssh.ping is Phase 16 (Native Go Execution Adapter)'s own real,
	// StatusImplemented method: a lightweight connectivity check that
	// dials a real SSH connection and echoes a value back, the artifact
	// that phase's Release Gate proves the full distributed execution
	// chain (dispatch, a real JIT-delivered secret, the Runner's DAG
	// executor, the per-task subprocess boundary, a real device) through.
	{
		Name:          "net.ssh.ping",
		Capabilities:  []capability.Name{capability.NameSSHTransport},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Opens a real SSH connection to the target and echoes a value back, to prove reachability.",
			Description: "Dials the device's SSHTransportCapable host and port, authenticates with the credential the Controller attached to this dispatch, and runs a trivial, read-only remote command that echoes params.data (default \"pong\") back. Never reports changed: a connectivity check does not alter device state.",
			Returns: []collection.ReturnField{
				{Name: "reply", Type: "string", Returned: "always", Description: "The trimmed value the remote command echoed back."},
			},
			Examples: []collection.Example{
				{Name: "Check a device is reachable over SSH", RunbookYAML: "- name: Ping the device\n  net.ssh.ping:\n  register: reachability\n"},
			},
		},
	},
	{
		Name:          "net.netconf.config",
		Capabilities:  []capability.Name{capability.NameNetconf},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Applies configuration to a device over NETCONF."},
	},
	{
		Name:          "net.ios.config",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Applies configuration lines to a Cisco IOS device, with an optional pre-change backup.",
			Description: "Opens an interactive PTY session over SSH, using netcli.IOS's own real paging, configuration-mode and error conventions (verified directly against a real Cisco IOS XE device, not assumed), and applies lines as a batch: \"configure terminal\", each line in order, then \"end\". Aborts on the first line the device rejects (a real IOS \"% ...\" error), still leaving configuration mode before returning that error. When backup is true, runs \"show running-config\" before applying anything and records it under the backup stat, giving an operator something to restore from by hand; this platform does not attempt an automatic rollback (see this method's own Reversibility notes for why). Reports changed whenever every line reaches the device without error: a configuration line's effect cannot be inspected before it runs, the same reasoning exec.command and net.cli.command both apply.",
			Params: []collection.Param{
				{Name: "lines", Type: "list of string", Required: true, Description: "The configuration lines to apply, in order, WITHOUT \"configure terminal\" or \"end\": this method supplies both itself."},
				{Name: "backup", Type: "bool", Default: "false", Description: "Capture the device's running-config with \"show running-config\" before applying any line, recorded under the backup stat."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "backup", Type: "string", Returned: "when backup is true", Description: "The device's full running-config, captured immediately before this task's own lines were applied. Not sanitized: it genuinely contains this device's own enable secret, enable password, local user password hashes, and any TACACS+/RADIUS shared key in whatever strength (or weakness -- IOS's own \"type 7\" is trivially reversible) encoding the device applies. Mask it with \"register_mask: backup\" on this task."},
			},
			Examples: []collection.Example{
				{
					Name:        "Create a loopback interface with a backup",
					RunbookYAML: "- name: Add a loopback interface\n  net.ios.config:\n    backup: true\n    lines:\n      - interface Loopback0\n      - description managed by pleiades\n",
				},
			},
			SeeAlso: []string{"net.cli.command", "net.cli.config"},
		},
	},
	{
		Name:          "net.junos.config",
		Capabilities:  []capability.Name{capability.NameJunos},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Applies configuration to a Juniper Junos device."},
	},
	{
		Name:          "net.eos.config",
		Capabilities:  []capability.Name{capability.NameAristaEOS},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Applies configuration to an Arista EOS device."},
	},
	// The net.catalyst.* namespace is controller-side and read-only: each
	// method addresses a Cisco Catalyst Center over its REST API and
	// gathers facts about the fleet it manages, changing nothing. They are
	// the first methods in this catalog to reach status implemented, having
	// been verified against Cisco's public DevNet sandbox.
	{
		Name:          "net.catalyst.device_facts",
		Capabilities:  []capability.Name{capability.NameCatalystAPI},
		Transports:    []string{"https"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Gathers every device a Cisco Catalyst Center manages, as facts.",
			Description: "Pages through the Catalyst Center's device inventory and emits one fact entry per device: identity, platform, software, role, and reachability/collection status. Never reports changed: reading an inventory does not alter it.",
			Fragments:   []string{"catalyst_connection", "catalyst_pagination"},
			Returns: []collection.ReturnField{
				{Name: "devices", Type: "list of map", Returned: "always", Description: "One entry per device: id, hostname, management_ip, family, series, platform_id, software_type, software_version, role, serial_number, reachability_status, collection_status."},
				{Name: "device_count", Type: "int", Returned: "always", Description: "The number of devices in the devices fact."},
			},
			Examples: []collection.Example{
				{Name: "Gather the managed fleet", RunbookYAML: "- name: Gather Catalyst Center device facts\n  net.catalyst.device_facts:\n  register: fleet\n"},
			},
			SeeAlso: []string{"net.catalyst.reachability", "net.catalyst.site_facts"},
		},
	},
	{
		Name:          "net.catalyst.site_facts",
		Capabilities:  []capability.Name{capability.NameCatalystAPI},
		Transports:    []string{"https"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Gathers a Cisco Catalyst Center's site hierarchy, as facts.",
			Description: "Reads every site the targeted Catalyst Center manages. Emits the full site_name_hierarchy for each site, not just its bare name, since two sites in different regions can share a bare name.",
			Fragments:   []string{"catalyst_connection"},
			Returns: []collection.ReturnField{
				{Name: "sites", Type: "list of map", Returned: "always", Description: "One entry per site: id, name, site_name_hierarchy."},
				{Name: "site_count", Type: "int", Returned: "always", Description: "The number of sites in the sites fact."},
			},
			Examples: []collection.Example{
				{Name: "Gather the site hierarchy", RunbookYAML: "- name: Gather Catalyst Center site facts\n  net.catalyst.site_facts:\n  register: sites\n"},
			},
			SeeAlso: []string{"net.catalyst.device_facts"},
		},
	},
	{
		Name:          "net.catalyst.tag_facts",
		Capabilities:  []capability.Name{capability.NameCatalystAPI},
		Transports:    []string{"https"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Gathers the tags defined on a Cisco Catalyst Center, as facts.",
			Description: "Reads every tag the targeted Catalyst Center knows about, and separates the controller's own system tags (its internal bookkeeping) from operator-created ones, so a runbook grouping devices by operator intent does not have to filter system tags out itself.",
			Fragments:   []string{"catalyst_connection"},
			Returns: []collection.ReturnField{
				{Name: "tags", Type: "list of map", Returned: "always", Description: "One entry per tag: id, name, system_tag."},
				{Name: "operator_tags", Type: "list of string", Returned: "always", Description: "Names of every tag not created by the controller itself."},
			},
			Examples: []collection.Example{
				{Name: "Gather operator-created tags", RunbookYAML: "- name: Gather Catalyst Center tag facts\n  net.catalyst.tag_facts:\n  register: tags\n"},
			},
		},
	},
	{
		Name:          "net.catalyst.reachability",
		Capabilities:  []capability.Name{capability.NameCatalystAPI},
		Transports:    []string{"https"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Reports which devices a Cisco Catalyst Center can currently reach and manage.",
			Description: "Answers a different question than device_facts: device_facts describes what the fleet is, gathered once; reachability describes what the fleet is doing right now, the check a gate task waits on before acting. Reports reachability_status (can the controller talk to the device at all) and collection_status (is it successfully collecting from it) separately, since a device can be reachable and still not managed.",
			Fragments:   []string{"catalyst_connection", "catalyst_pagination"},
			Returns: []collection.ReturnField{
				{Name: "devices", Type: "list of map", Returned: "always", Description: "One entry per device: hostname, management_ip, reachability_status, collection_status, reachable."},
				{Name: "unreachable", Type: "list of string", Returned: "always", Description: "Hostnames (or management IPs, if hostname is unset) of every unreachable device."},
				{Name: "reachable_count", Type: "int", Returned: "always", Description: "Count of devices with reachability_status Reachable."},
				{Name: "managed_count", Type: "int", Returned: "always", Description: "Count of devices with collection_status Managed."},
			},
			Examples: []collection.Example{
				{Name: "Gate a change on full fleet reachability", RunbookYAML: "- name: Confirm the fleet is reachable before changing anything\n  net.catalyst.reachability:\n  register: health\n\n- name: Apply the change\n  net.ios.config:\n  when_cel: \"stat.health[''].unreachable.size() == 0\"\n"},
			},
			SeeAlso: []string{"net.catalyst.device_facts"},
		},
	},
}
