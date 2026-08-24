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
		Capabilities:  []capability.Name{capability.NameNetconf, capability.NameSSHTransport},
		Transports:    []string{"netconf"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Applies a configuration document to a device over NETCONF, with an optional pre-change backup.",
			Description: "Opens an RFC 6241 NETCONF session over the SSH \"netconf\" subsystem and applies content to the target datastore with edit-config. Parameter names are ansible.netcommon.netconf_config's own. The session negotiates RFC 6242 chunked framing whenever the device offers base:1.1, and requests rollback-on-error whenever the device advertises it, so a rejected document leaves the device unchanged rather than half configured; that matters most on a device offering only writable-running, which is what Cisco IOS XE offers, because such a device has no staging area and every element lands on the live configuration as it is applied. A datastore the device never advertised support for is refused when the session opens rather than at the first write, naming the missing capability. Unlike the net.cli.* and net.ios.config methods, a rejected element comes back as a structured error carrying the device's own error-tag and the XPath of the element it objected to. Reports changed whenever the document reaches the device and the device answers ok.",
			Params: []collection.Param{
				{Name: "content", Type: "string", Required: true, Description: "The configuration document to apply, as the XML that goes inside edit-config's <config> element. Per-element operations are expressed the standard way, with an nc:operation attribute; pair that with default_operation: none so the device changes only what the document explicitly names."},
				{Name: "target", Type: "string", Default: "running", Description: "The datastore to configure: running, candidate or startup. A datastore the device does not advertise support for is refused before anything is applied. Cisco IOS XE offers only running."},
				{Name: "default_operation", Type: "string", Default: "merge", Description: "What the device does with elements carrying no explicit operation attribute: merge, replace or none. RFC 6241 defines no \"delete\" here; express a delete with an nc:operation attribute in content."},
				{Name: "error_option", Type: "string", Default: "rollback-on-error when the device supports it, otherwise the device's own stop-on-error default", Description: "How the device handles a rejected element: stop-on-error, continue-on-error or rollback-on-error. Left unset this method asks for rollback-on-error whenever the device advertises the capability, because stop-on-error leaves a rejected document half applied."},
				{Name: "lock", Type: "string", Default: "never", Description: "Whether to lock the target datastore for the duration: never, always, or if_supported. Locking prevents another client changing the datastore mid-edit; it also blocks every other client, which matters on a shared device."},
				{Name: "commit", Type: "bool", Default: "true", Description: "Commit after a successful edit. Only meaningful when target is candidate, since a running-datastore edit is already live; ignored otherwise."},
				{Name: "backup", Type: "bool", Default: "false", Description: "Capture the target datastore's full contents with get-config before applying anything, recorded under the backup stat."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "backup", Type: "string", Returned: "when backup is true", Description: "The target datastore's full contents as XML, captured immediately before this task's own document was applied. Not sanitized: a device's configuration genuinely contains its enable secret, local user password hashes, and any TACACS+/RADIUS shared key, in whatever strength of encoding the device applies. Mask it with \"register_mask: backup\" on this task."},
			},
			Examples: []collection.Example{
				{
					Name:        "Set a device's hostname over NETCONF",
					RunbookYAML: "- name: Set the hostname\n  net.netconf.config:\n    content: |\n      <native xmlns=\"http://cisco.com/ns/yang/Cisco-IOS-XE-native\">\n        <hostname>edge-01</hostname>\n      </native>\n    backup: true\n  register_mask: backup\n",
				},
				{
					Name:        "Remove an interface, changing nothing else",
					RunbookYAML: "- name: Remove the loopback\n  net.netconf.config:\n    default_operation: none\n    content: |\n      <native xmlns=\"http://cisco.com/ns/yang/Cisco-IOS-XE-native\">\n        <interface>\n          <Loopback xmlns:nc=\"urn:ietf:params:xml:ns:netconf:base:1.0\" nc:operation=\"delete\">\n            <name>8990</name>\n          </Loopback>\n        </interface>\n      </native>\n",
				},
			},
			SeeAlso: []string{"net.ios.config", "net.ios.save", "net.cli.config"},
		},
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
		Name:          "net.ios.facts",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Gathers structured facts from a Cisco IOS device over its CLI.",
			Description: "Closes a real gap: facts.gather requires FactGathererCapable, which no Cisco device type declares, so before this method a Cisco device could be commanded and configured but never described. Opens an interactive PTY session over SSH using netcli.IOS's own paging and prompt conventions, runs the read-only show commands the requested subsets need, and emits what it parses through EmitFact rather than SetStat, the same choice facts.gather and net.catalyst.device_facts both make: a fact is long-lived drift data worth comparing across weeks, and a software version recorded as a stat answers nothing next month. Every parser in this method was written against output captured from a real Cisco IOS XE device rather than from documentation or memory. A field the device does not report is left out entirely rather than emitted as an empty string, so a condition can tell \"this device does not say\" from \"this device says nothing\". Nothing is changed, so this always reports no change. Two of cisco.ios.ios_facts's own subsets are deliberately absent rather than accepted and ignored: config, because net.ios.config's own backup parameter already captures a running-config and doing it twice invites two answers, and hardware, because the memory and flash figures IOS reports vary enough by platform that parsing them generically would be a guess.",
			Params: []collection.Param{
				{Name: "gather_subset", Type: "list of string", Default: "[\"min\"]", Description: "Which subsets to gather: \"min\" (hostname, version, model, serial number, image, uptime, from \"show version\" and \"show inventory\"), \"interfaces\" (from \"show ip interface brief\"), or \"all\" for both. An unrecognized subset is refused rather than skipped."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "ansible_net_hostname", Type: "string", Returned: "when the min subset is gathered and the device reports it", Description: "The device's own hostname, read from the \"<hostname> uptime is ...\" line of \"show version\"."},
				{Name: "ansible_net_version", Type: "string", Returned: "when the min subset is gathered and the device reports it", Description: "The IOS XE version string, e.g. \"17.15.04c\"."},
				{Name: "ansible_net_model", Type: "string", Returned: "when the min subset is gathered and the device reports it", Description: "The platform model, e.g. \"C8000V\"."},
				{Name: "ansible_net_serialnum", Type: "string", Returned: "when the min subset is gathered and the device reports it", Description: "The chassis serial number, preferring \"show inventory\"'s Chassis SN and falling back to \"show version\"'s Processor board ID."},
				{Name: "ansible_net_image", Type: "string", Returned: "when the min subset is gathered and the device reports it", Description: "The running system image file, e.g. \"bootflash:packages.conf\"."},
				{Name: "ansible_net_uptime", Type: "string", Returned: "when the min subset is gathered and the device reports it", Description: "Uptime exactly as the device words it, e.g. \"1 hour, 32 minutes\". Not converted to seconds: IOS reports a rounded phrase, and parsing it into a precise number would invent precision the device never gave."},
				{Name: "ansible_net_interfaces", Type: "list of map", Returned: "when the interfaces subset is gathered", Description: "One entry per interface: name, ip_address (omitted when the device says \"unassigned\"), status, and protocol. Status is taken whole, so \"administratively down\" is reported as written rather than truncated at the first space."},
				{Name: "ansible_net_gather_subset", Type: "list of string", Returned: "always", Description: "The subsets actually gathered, after \"all\" is expanded."},
			},
			Examples: []collection.Example{
				{
					Name:        "Gather a device's identity before deciding anything",
					RunbookYAML: "- name: Learn what this router is\n  net.ios.facts:\n  register: device\n",
				},
				{
					Name:        "Gather interfaces as well",
					RunbookYAML: "- name: Learn the interface list too\n  net.ios.facts:\n    gather_subset:\n      - all\n",
				},
			},
			SeeAlso: []string{"facts.gather", "net.catalyst.device_facts", "net.ios.config"},
		},
	},
	{
		Name:          "net.ios.ping",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Runs a ping from a Cisco IOS device and reports the result.",
			Description: "Answers a different question from net.ssh.ping, and the difference is the point: net.ssh.ping proves this platform can reach the device, while this method proves the DEVICE can reach somewhere else, which is the question that actually matters when a routing or ACL change is under review. Runs IOS's own ping from an interactive PTY session and parses its \"Success rate is N percent (rx/tx)\" line, including the trailing \"round-trip min/avg/max = a/b/c ms\" clause that IOS omits entirely when nothing came back. Nothing is changed on the device, so this always reports no change. Use state to turn the result into a gate: state present (the default) fails the task when every packet is lost, and state absent fails it when anything answers, so a runbook can assert reachability or its absence without a separate condition.",
			Params: []collection.Param{
				{Name: "dest", Type: "string", Required: true, Description: "The address or hostname to ping from the device."},
				{Name: "count", Type: "int", Default: "5", Description: "How many echoes to send, passed to IOS as \"repeat\"."},
				{Name: "source", Type: "string", Description: "Source address or interface for the ping, passed to IOS as \"source\"."},
				{Name: "vrf", Type: "string", Description: "VRF to ping from, passed to IOS as \"vrf\"."},
				{Name: "state", Type: "string", Default: "present", Description: "\"present\" fails the task if the destination is unreachable (0 percent success); \"absent\" fails it if the destination answers at all. Set neither expectation by using a when condition on the returned facts instead."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "packet_loss", Type: "string", Returned: "always", Description: "Percentage of packets lost, as a string with a trailing percent sign, e.g. \"0%\"."},
				{Name: "packets_tx", Type: "int", Returned: "always", Description: "How many echoes the device sent."},
				{Name: "packets_rx", Type: "int", Returned: "always", Description: "How many replies the device received."},
				{Name: "rtt", Type: "map", Returned: "when at least one packet returned", Description: "Round-trip times in milliseconds: min, avg, max. Absent entirely when every packet was lost, because IOS prints no round-trip clause in that case."},
			},
			Examples: []collection.Example{
				{
					Name:        "Assert the device can still reach its gateway",
					RunbookYAML: "- name: Confirm the upstream gateway answers\n  net.ios.ping:\n    dest: 192.0.2.1\n",
				},
				{
					Name:        "Record reachability without failing the run",
					RunbookYAML: "- name: Measure reachability to a peer\n  net.ios.ping:\n    dest: 198.51.100.10\n    count: 10\n    state: absent\n  register: peer\n",
				},
			},
			SeeAlso: []string{"net.ssh.ping", "net.ios.facts"},
		},
	},
	{
		Name:          "net.ios.save",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Saves a Cisco IOS device's running configuration to startup.",
			Description: "Runs IOS's \"write memory\", copying running-config over startup-config so the current configuration survives a reload. This is the step that makes every earlier net.ios.config task permanent, and it is deliberately a separate method rather than a parameter on net.ios.config: persisting configuration is a decision about blast radius, not a detail of applying a line, and a runbook that applies several changes should be able to decide once, at the end, whether any of them should outlive the next reload. Reports changed whenever the save completes, since IOS gives no way to know whether startup-config already matched. Aborts on a real IOS \"% ...\" error rather than reporting a save that did not happen.",
			Params: []collection.Param{
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "stdout", Type: "string", Returned: "always", Description: "Whatever the device printed in response to \"write memory\", typically a \"Building configuration...\" line followed by \"[OK]\"."},
			},
			Examples: []collection.Example{
				{
					Name:        "Persist a change after verifying it",
					RunbookYAML: "- name: Save the running configuration\n  net.ios.save:\n",
				},
			},
			SeeAlso: []string{"net.ios.config"},
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
