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
	"github.com/Subject-Void-LLC/the-pleiades/pkg/clidialog"
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
			Description: netCLICommandDescription,
			Params:      netCLICommandParams(),
			Returns:     netCLICommandReturns(),
			Examples: []collection.Example{
				{
					Name:        "Read a device's software version",
					RunbookYAML: "- name: Check the running version\n  net.cli.command:\n    command: show version\n  register: version\n",
				},
				{
					Name:        "Answer the question a command asks",
					RunbookYAML: "- name: Copy the configuration to a file\n  net.cli.command:\n    command: copy running-config flash:before-change.cfg\n    prompts:\n      - match: 'Destination filename \\[[^\\]]*\\]\\?'\n        answer: \"\"\n      - match: 'over ?write\\? \\[confirm\\]'\n        refuse: true\n",
				},
			},
			SeeAlso: []string{"net.cli.config", "net.ios.command", "net.ios.config"},
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
				{Name: "config", Type: "string", Required: true, Description: "The configuration lines to send, one per line. Blank lines are skipped. Include any vendor-specific mode commands (e.g. \"configure terminal\" and \"end\") this device needs, since this method sends none on its own.", Format: collection.ParamFormatCommand},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
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
			Description: "Opens an RFC 6241 NETCONF session over the SSH \"netconf\" subsystem and applies content to the chosen datastore with edit-config. Parameter names are ansible.netcommon.netconf_config's own, with its target parameter under its own alias, datastore, since target names the device a task runs on. The session negotiates RFC 6242 chunked framing whenever the device offers base:1.1, and requests rollback-on-error whenever the device advertises it, so a rejected document leaves the device unchanged rather than half configured; that matters most on a device offering only writable-running, which is what Cisco IOS XE offers, because such a device has no staging area and every element lands on the live configuration as it is applied. A datastore the device never advertised support for is refused when the session opens rather than at the first write, naming the missing capability. Unlike the net.cli.* and net.ios.config methods, a rejected element comes back as a structured error carrying the device's own error-tag and the XPath of the element it objected to. Reports changed whenever the document reaches the device and the device answers ok.",
			Params: []collection.Param{
				{Name: "content", Type: "string", Required: true, Description: "The configuration document to apply, as the XML that goes inside edit-config's <config> element. Per-element operations are expressed the standard way, with an nc:operation attribute; pair that with default_operation: none so the device changes only what the document explicitly names."},
				{Name: "datastore", Type: "string", Default: "running", Description: "The datastore to configure: running, candidate or startup. A datastore the device does not advertise support for is refused before anything is applied. Cisco IOS XE offers only running."},
				{Name: "default_operation", Type: "string", Default: "merge", Description: "What the device does with elements carrying no explicit operation attribute: merge, replace or none. RFC 6241 defines no \"delete\" here; express a delete with an nc:operation attribute in content."},
				{Name: "error_option", Type: "string", Default: "rollback-on-error when the device supports it, otherwise the device's own stop-on-error default", Description: "How the device handles a rejected element: stop-on-error, continue-on-error or rollback-on-error. Left unset this method asks for rollback-on-error whenever the device advertises the capability, because stop-on-error leaves a rejected document half applied."},
				{Name: "lock", Type: "string", Default: "never", Description: "Whether to lock the datastore for the duration: never, always, or if_supported. Locking prevents another client changing the datastore mid-edit; it also blocks every other client, which matters on a shared device."},
				{Name: "commit", Type: "bool", Default: "true", Description: "Commit after a successful edit. Only meaningful when datastore is candidate, since a running-datastore edit is already live; ignored otherwise."},
				{Name: "backup", Type: "bool", Default: "false", Description: "Capture the datastore's full contents with get-config before applying anything, recorded under the backup stat."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "backup", Type: "string", Returned: "when backup is true", Description: "The datastore's full contents as XML, captured immediately before this task's own document was applied. Not sanitized: a device's configuration genuinely contains its enable secret, local user password hashes, and any TACACS+/RADIUS shared key, in whatever strength of encoding the device applies. Mask it with \"register_mask: backup\" on this task."},
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
			Summary: "Applies configuration lines to a Cisco IOS device, keeping a checkpoint of the configuration before them so the change can be undone exactly.",
			Description: "Opens an interactive session over SSH (or over Telnet, with insecure_telnet), using netcli.IOS's own paging, configuration-mode and error conventions, and applies lines as a batch: \"configure terminal\", each line in order, then \"end\". Aborts on the first line the device rejects (an IOS \"% ...\" error), still leaving configuration mode before returning that error. A \"% \" line IOS prints for a line it accepted is a notice, not a rejection, and is reported under notices: an access list entry, IKEv2 proposal or profile match already there, a key pair being made or replaced, and what removing one says first; accept adds the task's own. A line that changes key material (crypto key generate, zeroize, import, move, decrypt, encrypt) changes what the configuration does not hold, so its run reports changed whatever the configuration shows, and no checkpoint returns it: such a call's undo is never exact. " +
				"A line that asks a question (removing a key pair asks to confirm) is answered from prompts, each answer written in the task, or by a model with ask, which picks one of the task's own choices by id or refuses; a question nothing answers (a line left ending in ?, [confirm], [yes/no] or asking for a password) fails the line at once, naming it, rather than waiting; anything else a device waits on, such as a banner's text, still waits until the read limit. " +
				"Before the lines, it copies the running configuration to a checkpoint file on the device (its default writable disk or flash, such as unix: or flash:), and after them the device's own show archive config differences says what changed: no difference reports unchanged and deletes the checkpoint, so a second run of the same lines changes nothing. A change copies the configuration it left to a second checkpoint and records its undo as net.ios.replace naming both files, which returns the device exactly to the first (configure replace) and refuses if the device no longer matches the second. The files stay on the device until that undo runs. " +
				"A device with no writable file system, or no configure replace, records no undo, with a warning: the configuration lines that would undo the change, written from its configuration read before and after it (the negation of each added line, each removed line put back), are reported under undo_lines, masked, for the task's rollback: list, since lines read off a device never reach the journal. A change inside an access list, a banner or a certificate has no such lines. checkpoint: false does the same on any device. The differences this reports are masked as net.batfish.check masks a candidate.",
			Params: append(append([]collection.Param{
				{Name: "lines", Type: "list of string", Required: true, Description: "The configuration lines to apply, in order, WITHOUT \"configure terminal\" or \"end\": this method supplies both itself. A line may name a stored secret, such as a pre-shared key, with a secret reference, {{ secret.<name>.<field> }} (pleiades add-secret): it is resolved just before the line is sent, and masked in everything the run reports. A line holding one holds no other template expression, and a value with whitespace, a control character or ? is refused.", Format: collection.ParamFormatCommand, SecretRefs: true},
				{Name: "checkpoint", Type: "bool", Default: "true", Description: "Keep a checkpoint of the running configuration on the device before the change, which its undo returns to exactly. false records no undo, and reports the lines that would undo the change for the task's rollback: list."},
				{Name: "backup", Type: "bool", Default: "false", Description: "Capture the device's running-config with \"show running-config\" before applying any line, recorded under the backup stat."},
				{Name: "accept", Type: "list of string", Description: "Further IOS \"% \" lines to take as notices rather than rejections, each RE2 matched from the start of the line and so beginning with %, e.g. \"% Frob is deprecated\". Every other \"% \" line still fails the task."},
			}, clidialog.ConfigParams()...),
				collection.Param{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				collection.Param{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
			),
			Returns: append([]collection.ReturnField{
				{Name: "checkpoint", Type: "string", Returned: "when a checkpoint was kept and the lines changed the configuration", Description: "The checkpoint file on the device holding the configuration before the change, which the undo returns to."},
				{Name: "differences", Type: "list of string", Returned: "when a checkpoint was kept", Description: "The device's own contextual differences the change made (+ added, - removed), masked."},
				{Name: "undo_lines", Type: "list of string", Returned: "when no checkpoint was kept and the lines changed the configuration", Description: "The configuration lines that would undo the change, masked; copy them into the task's rollback: list, putting each masked secret back."},
				{Name: "backup", Type: "string", Returned: "when backup is true", Description: "The device's full running-config, captured immediately before this task's own lines were applied. Not sanitized: it genuinely contains this device's own enable secret, enable password, local user password hashes, and any TACACS+/RADIUS shared key in whatever strength (or weakness -- IOS's own \"type 7\" is trivially reversible) encoding the device applies. Mask it with \"register_mask: backup\" on this task."},
				{Name: "notices", Type: "list of string", Returned: "when the device printed one", Description: "The \"% \" lines the device printed that were taken as notices rather than rejections, masked."},
			}, clidialog.ConfigReturns()...),
			Examples: []collection.Example{
				{
					Name:        "Create a loopback interface, undoable by pleiades rollback",
					RunbookYAML: "- name: Add a loopback interface\n  net.ios.config:\n    lines:\n      - interface Loopback0\n      - description managed by pleiades\n",
				},
				{
					Name:        "Remove a key pair, answering the question IOS asks first",
					RunbookYAML: "- name: Remove the old key pair, which no rollback brings back\n  net.ios.config:\n    lines:\n      - crypto key zeroize rsa OLDKEY\n    prompts:\n      - match: 'Do you really want to remove these keys\\? \\[yes/no\\]'\n        answer: \"yes\"\n    accept:\n      - '% The specified keypair does not exist'\n",
				},
			},
			SeeAlso: []string{"net.ios.replace", "net.ios.parse", "net.cli.command", "net.cli.config"},
		},
	},
	{
		Name:          "net.ios.command",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Runs one exec-mode command on a Cisco IOS device, answering the questions it asks as the task says.",
			Description: netIOSCommandDescription,
			Params:      netIOSCommandParams(),
			Returns:     netCLICommandReturns(),
			Examples:    netIOSCommandExamples(),
			SeeAlso:     []string{"net.cli.command", "net.ios.config", "net.ios.save", "ai.chat"},
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
				{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
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
				{Name: "dest", Type: "string", Required: true, Description: "The address or hostname to ping from the device.", Format: collection.ParamFormatCommand},
				{Name: "count", Type: "int", Default: "5", Description: "How many echoes to send, passed to IOS as \"repeat\"."},
				{Name: "source", Type: "string", Description: "Source address or interface for the ping, passed to IOS as \"source\".", Format: collection.ParamFormatCommand},
				{Name: "vrf", Type: "string", Description: "VRF to ping from, passed to IOS as \"vrf\".", Format: collection.ParamFormatCommand},
				{Name: "state", Type: "string", Default: "present", Description: "\"present\" fails the task if the destination is unreachable (0 percent success); \"absent\" fails it if the destination answers at all. Set neither expectation by using a when condition on the returned facts instead."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
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
			Description: "Runs IOS's \"write memory\", copying running-config over startup-config so the current configuration survives a reload. This is the step that makes every earlier net.ios.config task permanent, and it is deliberately a separate method rather than a parameter on net.ios.config: persisting configuration is a decision about blast radius, not a detail of applying a line, and a runbook that applies several changes should be able to decide once, at the end, whether any of them should outlive the next reload. Reports changed whenever the save completes, since IOS gives no way to know whether startup-config already matched. Aborts on a real IOS \"% ...\" error rather than reporting a save that did not happen. A check opens the session a real run opens and predicts the change, since a save always reports one, without sending write memory.",
			Params: []collection.Param{
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
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
		Name:          "net.ios.replace",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh", "telnet", "console"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Replaces a Cisco IOS device's running configuration with a checkpoint file on the device.",
			Description: "Runs IOS's configure replace with a checkpoint file the device holds (net.ios.config writes one before each change and records this method as its undo), so the device itself puts back exactly the configuration the checkpoint holds: lines a change added are removed and lines it replaced or removed come back, with no inverse line written by anyone. When expect names a second checkpoint (the one net.ios.config writes after its change), the device must still match it, by the device's own show archive config differences, or this refuses before replacing anything, unless force is true: configure replace returns the whole configuration to the checkpoint, so a change someone made since would be undone with it. After the replace, the device must report no differences from the checkpoint, or the task fails; a line in IOS's \"% \" form the replace prints fails it only then, and when the device matches it is reported as a warning, masked. The checkpoint files net.ios.config made (named pleiades-<id>.cfg) are deleted afterwards unless keep is true; any other file, such as nvram:startup-config or a configuration kept on flash:, is never deleted. A check reports what the replace would change, from the same comparison, and changes nothing. The configuration lines this reports are masked as net.batfish.check masks a candidate: a password, secret or key is never shown.",
			Params: []collection.Param{
				{Name: "checkpoint", Type: "string", Required: true, Description: "The checkpoint to return to, a file on the device such as unix:pleiades-3f2a.cfg or flash:pleiades-3f2a.cfg."},
				{Name: "expect", Type: "string", Description: "A checkpoint the running configuration must still match before anything is replaced, refusing otherwise: the configuration as the change left it."},
				{Name: "force", Type: "bool", Default: "false", Description: "Replace even when the running configuration no longer matches expect, undoing whatever changed since along with the change."},
				{Name: "keep", Type: "bool", Default: "false", Description: "Keep the checkpoint files net.ios.config made on the device after a replace that leaves no differences, rather than deleting them. A file it did not make is never deleted."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
			},
			Returns: []collection.ReturnField{
				{Name: "differences", Type: "list of string", Returned: "always", Description: "The device's own contextual differences between the running configuration and the checkpoint before the replace, masked: what the replace changes, or would change in a check."},
			},
			Examples: []collection.Example{
				{
					Name:        "Return a device to a checkpoint a change left",
					RunbookYAML: "- name: Undo the loopback change\n  net.ios.replace:\n    checkpoint: unix:pleiades-3f2a.cfg\n    expect: unix:pleiades-3f2a-after.cfg\n",
				},
			},
			SeeAlso: []string{"net.ios.config", "net.ios.parse"},
		},
	},
	{
		Name:          "net.ios.checkpoints",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh", "telnet", "console"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Lists the checkpoints net.ios.config left on a Cisco IOS device, and with prune deletes the older ones.",
			Description: "Reads the file system net.ios.config keeps its checkpoints on (the device's default writable disk or flash, such as unix: or flash:) with dir, and reports each change's checkpoints, newest first: its id, the configuration before it and, for a change that completed, after it, with the time and size the device gives. Only files named as net.ios.config names them (pleiades-<12 hex digits>.cfg and -after.cfg) are ever listed or deleted, so a startup configuration or an image is never touched. " +
				"Each checkpoint holds the whole configuration, its secrets included, and stays on the device until its change's undo runs. With prune, the checkpoints of every change but the newest keep, or of every change older than older_than by the device's own clock (show clock, so a router whose clock was never set is measured against itself), or both (a change goes when either says so), are deleted with delete /force; a check reports what would go. A deleted checkpoint's undo (the net.ios.replace net.ios.config recorded) then refuses, so prune only changes no rollback will need, such as those of a lab about to be deleted.",
			Params: []collection.Param{
				{Name: "prune", Type: "bool", Default: "false", Description: "Delete the checkpoints keep and older_than select. Without it the method only lists."},
				{Name: "keep", Type: "int", Description: "With prune, keep the checkpoints of this many of the newest changes and delete the rest; 0 deletes every one."},
				{Name: "older_than", Type: "string", Description: "With prune, delete the checkpoints of every change older than this, a Go duration such as 72h, measured by the device's own clock against the times it gives its files."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
			},
			Returns: []collection.ReturnField{
				{Name: "checkpoints", Type: "list of dict", Returned: "always", Description: "Each change's checkpoints, newest first: id, before, after (empty when the change did not complete), modified (RFC 3339, empty when the device's time did not read) and bytes."},
				{Name: "deleted", Type: "list of string", Returned: "when prune is true", Description: "The files deleted, or in a check the files that would be."},
			},
			Examples: []collection.Example{
				{Name: "Keep the checkpoints of the last five changes", RunbookYAML: "- name: Prune old checkpoints\n  net.ios.checkpoints:\n    prune: true\n    keep: 5\n"},
			},
			SeeAlso: []string{"net.ios.config", "net.ios.replace"},
		},
	},
	{
		Name:          "net.pcap.summary",
		Capabilities:  []capability.Name{},
		Transports:    []string{},
		EngineVersion: engineVersion,
		Site:          collection.SiteController,
		Device:        collection.DeviceNone,
		Doc:           collection.Doc{Summary: "Counts a packet capture in the project by protocol and address pair, encrypted apart from cleartext.", Description: "Reads a pcap or pcapng file from the project (virt.cml.capture.stop writes one; up to 16 MiB) and counts every frame by protocol: IPsec's ESP, ESP inside UDP 4500 (NAT traversal) and IKE (UDP 500, or 4500 behind the non-ESP marker), and in the clear GRE, ICMP, OSPF, EIGRP, BGP, HSRP, VRRP, PIM, TCP, UDP, ARP and frames carrying no IP (CDP, keepalives). Each IP packet is one of three kinds: encrypted (ESP, ESP in UDP), the key exchange that set it up (IKE), or cleartext (everything else carried over IP, which anyone on the wire can read). The same counts are given for each address pair, most packets first, so \"the sites' traffic crosses encrypted\" is a pair whose cleartext is 0 and whose encrypted count grew while traffic was sent; GRE in the clear between two sites is DMVPN without its IPsec. It reads Ethernet (VLAN tags included), raw IP and Linux cooked captures; a frame of another link type is counted as unread. A frame carrying no IP (CDP, a keepalive, but also anything inside MPLS or PPPoE) and an unread frame are not examined, so they are counted apart as unexamined, never as cleartext: \"nothing crossed in the clear\" is cleartext 0 and unexamined 0 (or every unexamined frame accounted for by its protocol). IKE and ESP in UDP are told by their ports, and ESP is counted encrypted whatever its cipher, ESP-NULL included. A file cut short is read up to the cut, and the task fails naming where. It changes nothing, and a check runs it as a run does. It runs on this machine with no device: write it without hosts.", Params: []collection.Param{{Name: "src", Type: "string", Required: true, Description: "The capture file, a path relative to the project directory, such as captures/core1-wan.pcap."}, {Name: "pairs", Type: "int", Default: "20", Description: "How many address pairs to list, most packets first; 0 lists none."}}, Returns: []collection.ReturnField{{Name: "format", Type: "string", Returned: "always", Description: "pcap or pcapng."}, {Name: "packets", Type: "int", Returned: "always", Description: "Every frame in the capture."}, {Name: "bytes", Type: "int", Returned: "always", Description: "Their lengths on the wire, summed."}, {Name: "protocols", Type: "dict", Returned: "always", Description: "Packets and bytes by protocol name (esp, esp-udp, ike, gre, icmp, ospf, bgp, tcp, udp, arp, non-ip and the rest)."}, {Name: "encrypted", Type: "int", Returned: "always", Description: "IP packets that were ESP or ESP in UDP."}, {Name: "key_exchange", Type: "int", Returned: "always", Description: "IP packets that were IKE."}, {Name: "cleartext", Type: "int", Returned: "always", Description: "Every other IP packet: what a reader of the wire saw in the clear."}, {Name: "pairs", Type: "list of dict", Returned: "always", Description: "Each address pair (a, b), most packets first: its protocols, encrypted, key_exchange and cleartext."}, {Name: "link_types", Type: "list of string", Returned: "always", Description: "The link types the capture declares."}, {Name: "unread", Type: "int", Returned: "always", Description: "Frames of a link type this method does not read."}, {Name: "unexamined", Type: "int", Returned: "always", Description: "Frames this method could not judge encrypted or not: unread ones, and every one carrying no IP (non-ip)."}}, Examples: []collection.Example{{Name: "Count what crossed a WAN link", RunbookYAML: "- name: Count what crossed CORE1's WAN link\n  net.pcap.summary:\n    src: captures/core1-wan.pcap\n  register: wan\n"}}, SeeAlso: []string{"virt.cml.capture.start", "virt.cml.capture.stop"}},
	},
	{
		Name:          "net.ios.parse",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh", "telnet", "console"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Runs a show command on a Cisco IOS or IOS XE device and returns its output parsed into Genie's schema.",
			Description: "Runs one show command over the same session net.ios.command opens and parses its output with a parser ported from Cisco's Genie (genieparser, Apache License 2.0), returning the same nested structure Genie returns for that command, so Genie's own schema documentation describes the result and a later task can compare two reads field by field, such as the state before a change and after its rollback. Each parser is held to every one of genieparser's own recorded device outputs and expected results for the command forms it covers. Output in which the parser finds nothing (no OSPF neighbors yet) is an empty result with empty true, not a failure, so a before and after can compare it; a command the device refuses (% BGP not active) fails the task. A command no parser covers is refused before the device is reached, naming the commands that are covered. It only reads, so it reports unchanged, and a check runs it as a real run does.",
			Params: []collection.Param{
				{Name: "command", Type: "string", Required: true, Description: "The show command to run and parse, one of the commands a parser covers (pleiades doc net.ios.parse lists them), for example \"show ip interface brief\" or \"show ip route\". IOS's abbreviations are not accepted: write the command in full."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
			},
			Returns: []collection.ReturnField{
				{Name: "parsed", Type: "dict", Returned: "always", Description: "The command's output in Genie's schema for it: for show ip interface brief, interface, keyed by interface name, each with ip_address, interface_is_ok, method, status and protocol."},
				{Name: "parser", Type: "string", Returned: "always", Description: "The Genie parser class whose port parsed it, such as ShowIpInterfaceBrief."},
				{Name: "empty", Type: "bool", Returned: "always", Description: "Whether the parser found nothing in the output, parsed then being empty."},
				{Name: "stdout", Type: "string", Returned: "always", Description: "The command's output as the device printed it, except that the secrets in show archive config differences (a password, secret, key or SNMP community) are masked, as net.ios.replace masks them, before it is parsed or recorded."},
			},
			Examples: []collection.Example{
				{
					Name:        "Read the interfaces before a change, to compare after its rollback",
					RunbookYAML: "- name: Read the interfaces\n  net.ios.parse:\n    command: show ip interface brief\n  register: interfaces_before\n",
				},
			},
			SeeAlso: []string{"net.ios.command", "net.ios.facts", "net.ios.replace"},
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
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
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
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
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
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
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
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
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
				{Name: "Gate a change on full fleet reachability", RunbookYAML: "- name: Confirm the fleet is reachable before changing anything\n  net.catalyst.reachability:\n  register: health\n\n- name: Apply the change\n  net.ios.config:\n    lines:\n      - ntp server 192.0.2.10\n  when_cel: \"stat.health[''].unreachable.size() == 0\"\n"},
			},
			SeeAlso: []string{"net.catalyst.device_facts"},
		},
	},
}
