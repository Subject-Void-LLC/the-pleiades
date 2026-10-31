// Package catalogdata: the routing and switching section (Phase 127b):
// FRRouting through vtysh, Open vSwitch through ovs-vsctl, waiting for a
// routing table to settle, tracing a path, and asking Batfish's model.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// hostKeyParam is the SSH host key parameter every SSH method takes.
var hostKeyParam = collection.Param{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."}

var routingCollections = []collectionscaffold.Config{
	{
		Name:          "net.frr.config",
		Capabilities:  []capability.Name{capability.NameFRR, capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes an FRRouting router run exactly the configuration given, and saves what it ran before so the change can be undone.",
			Description: "Takes the whole intended configuration (config, or src: a file in the project) and applies only what differs, through FRR's own frr-reload.py. The candidate is first checked with vtysh -m -f, and a line vtysh refuses fails the task before anything reaches the router: frr-reload reads a file vtysh cannot parse as an empty configuration and would delete everything. The difference comes from frr-reload.py --test; an empty one is no change, and a check returns it as the prediction. A real run saves the running configuration to a file on the router (0600, under the connecting account's ~/.pleiades-frr), reloads, confirms the running configuration now matches the candidate, and saves the result beside the backup. Its undo names only that backup, so no configuration text, and no secret in one, reaches the journal; the undo restores exactly the configuration that was running, and refuses when the router no longer runs what this change left, unless its step names force. The undo is recorded before the reload and kept if the reload then fails, since restoring a backup when nothing changed is itself no change. With save, the running configuration is also written to frr.conf, keeping that file's owner and mode, so it survives a restart; the backup then keeps frr.conf as it was too, and the undo puts it back (where there was none, the undo writes the configuration it restores). expect_sha256 refuses a candidate whose bytes are not the ones a model check analysed. Three facts about FRR decide whether this works on the first try. FRR rewrites a configuration in its own form (it adds lines such as frr version and frr defaults, drops comments and reorders), so a hand-written candidate that differs from that form fails after the reload with the difference shown as - and + lines; start from net.frr.facts's running_config, which is FRR's own form, and edit that. The account that saves must be able to replace frr.conf: it needs write access to the file and to its directory (/etc/frr), since the save writes a new file beside it, and a check refuses a save the account cannot make before anything is reloaded, naming the account that owns the file. Rollback restores from a backup under the connecting account's home, so undo with the same account that made the change.",
			Params: []collection.Param{
				{Name: "config", Type: "string", Description: "The whole intended configuration, as frr.conf text. Set config or src, not both. Lines not in it are removed from the running configuration."},
				{Name: "src", Type: "string", Description: "A file in the project directory holding the whole intended configuration, read by the command line; refused on a Walk-tier Runner, where config is the way. Set config or src, not both."},
				{Name: "save", Type: "bool", Default: "false", Description: "Also write the configuration to frr.conf (keeping that file's owner and mode), so it survives a restart. The undo puts frr.conf back as it was, or, where there was none, writes the configuration it restores."},
				{Name: "expect_sha256", Type: "string", Description: "Refuse unless the candidate's SHA-256 is this, in hex: the digest net.batfish.check returned for the bytes it analysed, so the change applied is the change checked."},
				{Name: "restore", Type: "string", Description: "Restore the configuration this method saved before an earlier change, by the path its undo recorded. Set by an undo; with restore, config and src are not given."},
				{Name: "login", Type: "string", Description: "With restore, the account that made the backup, as its undo recorded it. A backup lives under that account's home, so an undo run as another account is refused before it reads anything, naming the account to connect as. Set by an undo."},
				{Name: "force", Type: "bool", Default: "false", Description: "With restore, restore even though the router no longer runs what the earlier change left (someone changed it since)."},
				hostKeyParam,
			},
			Returns: []collection.ReturnField{
				{Name: "lines_added", Type: "list of string", Returned: "always", Description: "The configuration lines frr-reload added, as it reported them."},
				{Name: "lines_deleted", Type: "list of string", Returned: "always", Description: "The configuration lines frr-reload removed."},
				{Name: "sha256", Type: "string", Returned: "always", Description: "The SHA-256 of the configuration applied, in hex."},
				{Name: "backup", Type: "string", Returned: "when something changed", Description: "The path of the running configuration saved before the change, on the router."},
				{Name: "inverse", Type: "dict", Returned: "when something changed", Description: "The undo: this method with restore set to backup (and save when the change saved), held whole in the journal."},
			},
			Examples: []collection.Example{
				{Name: "Apply a router's intended configuration", RunbookYAML: "- name: r1 runs its intended configuration\n  net.frr.config:\n    target: r1\n    src: configs/r1.frr.conf\n    save: true\n"},
			},
			SeeAlso: []string{"net.frr.facts", "net.frr.interface", "net.batfish.check", "wait.routes"},
		},
	},
	{
		Name:          "net.frr.interface",
		Capabilities:  []capability.Name{capability.NameFRR, capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Sets an FRRouting interface administratively up or down, recording the state it found.",
			Description: "Reads the interface's administrative state through vtysh, and changes it only when it differs (shutdown or no shutdown in the running configuration, not frr.conf). Its undo sets the state it found, never simply the opposite, so undoing a run against an interface that was already down leaves it down.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The interface, as FRR names it (eth1, ens3)."},
				{Name: "enabled", Type: "bool", Required: true, Description: "true for up (no shutdown), false for down (shutdown)."},
				hostKeyParam,
			},
			Returns: []collection.ReturnField{
				{Name: "enabled", Type: "bool", Returned: "always", Description: "Whether the interface is administratively up now."},
				{Name: "inverse", Type: "dict", Returned: "when something changed", Description: "The undo: this method with the state found."},
			},
			Examples: []collection.Example{
				{Name: "Take a link down", RunbookYAML: "- name: Shut r1's link to r3\n  net.frr.interface:\n    name: eth2\n    enabled: false\n"},
			},
			SeeAlso: []string{"net.frr.config", "wait.routes"},
		},
	},
	{
		Name:          "net.frr.facts",
		Capabilities:  []capability.Name{capability.NameFRR, capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Reads an FRRouting router's version, interfaces, OSPF neighbours, routes and BGP peers as data.",
			Description: "Asks vtysh for each in JSON and returns it as data, and by default also the running configuration with every secret in it (a password, an authentication or message-digest key, a neighbour's password) replaced by a placeholder that is the same for equal secrets throughout one command-line run, so a model can still compare two routers' keys without seeing one (a Runner reads each device apart, so there placeholders compare only within one router). The word after each secret word (password, secret, authentication-key, message-digest-key, key-string, community, key) is masked, past any encryption type, key id or algorithm between them. It changes nothing.",
			Params: []collection.Param{
				{Name: "include_config", Type: "bool", Default: "true", Description: "Return the running configuration, its secrets replaced, the default; false leaves it out (config_sha256 is returned either way)."},
				hostKeyParam,
			},
			Returns: []collection.ReturnField{
				{Name: "version", Type: "string", Returned: "always", Description: "The FRR version."},
				{Name: "hostname", Type: "string", Returned: "always", Description: "The router's hostname as FRR knows it."},
				{Name: "interfaces", Type: "dict", Returned: "always", Description: "Each interface, from show interface json."},
				{Name: "ospf_neighbors", Type: "dict", Returned: "always", Description: "show ip ospf neighbor json, empty when OSPF is not running."},
				{Name: "routes", Type: "dict", Returned: "always", Description: "show ip route json: every route by prefix."},
				{Name: "bgp_summary", Type: "dict", Returned: "always", Description: "show bgp summary json, empty when BGP is not running."},
				{Name: "config_sha256", Type: "string", Returned: "always", Description: "The SHA-256 of the running configuration as it runs (from its frr version line), in hex: equal to a candidate file's digest exactly when the router runs that file."},
				{Name: "running_config", Type: "string", Returned: "unless include_config is false", Description: "The running configuration with its secrets replaced."},
			},
			Examples: []collection.Example{
				{Name: "Read a router for a model", RunbookYAML: "- name: Read r1\n  net.frr.facts:\n  register: r1\n"},
			},
			SeeAlso: []string{"net.frr.command", "net.batfish.check"},
		},
	},
	{
		Name:          "net.frr.command",
		Capabilities:  []capability.Name{capability.NameFRR, capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Runs one FRRouting show command through vtysh and returns its output.",
			Description: "Only show commands are accepted, so it changes nothing. A command ending in json returns its output parsed as data too.",
			Params: []collection.Param{
				{Name: "command", Type: "string", Required: true, Description: "The show command, e.g. \"show ip ospf neighbor\".", Format: collection.ParamFormatCommand},
				hostKeyParam,
			},
			Returns: []collection.ReturnField{
				{Name: "stdout", Type: "string", Returned: "always", Description: "What vtysh printed."},
				{Name: "data", Type: "dict", Returned: "when the command ends in json", Description: "stdout parsed."},
			},
			Examples: []collection.Example{
				{Name: "Read OSPF neighbours", RunbookYAML: "- name: OSPF neighbours\n  net.frr.command:\n    command: show ip ospf neighbor\n"},
			},
			SeeAlso: []string{"net.frr.facts"},
		},
	},
	{
		Name:          "wait.routes",
		Capabilities:  []capability.Name{},
		Transports:    []string{"ssh", "telnet", "console"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Waits until a router has a route to every prefix given, or with state absent has none left, and its routing table has stopped changing.",
			Description: "A full OSPF adjacency is not convergence: shortest paths are computed after it, and a router can route the long way, or not at all, for a while after every neighbour is up. This waits for what a change needs: a selected route to each prefix (through the given next hops, when next_hops names any), and the routing table, uptimes aside, unchanged for stable_for seconds. With state absent it waits for the opposite, such as a withdrawn network or a failed link: no selected route to any prefix given (or, for a prefix next_hops names, a route through none of those next hops, so a failover away from one neighbour can be waited for), with the same stable_for. A table read with no selected route at all (not even a connected one, as a cut or refused read or output the parser does not recognize leaves it) is never taken as every route gone: the wait goes on. At timeout it fails naming each route still missing (or, with state absent, still there, or that the table read held no route), or saying the table never held still. It changes nothing. It reads an FRR router's table through vtysh (show ip route json, over SSH, the device granted FRRCapable and CommandExecCapable) and a Cisco IOS router's global table through its CLI (show ip route, over SSH, the controller's console or Telnet with insecure_telnet), and refuses any other device before contacting it. That refusal is made when the task runs: no single capability names both, so validate and a Controller's admission cannot hold the device to it beforehand.",
			Params: []collection.Param{
				{Name: "prefixes", Type: "list of string", Required: true, Description: "The IPv4 prefixes that must each have a selected route (with state absent, none), e.g. 10.3.3.0/24; the IPv4 routing table is the one read."},
				{Name: "next_hops", Type: "dict", Description: "Per prefix, the next-hop addresses its route must use, e.g. {\"10.3.3.0/24\": [\"10.0.12.1\"]}; with state absent, the next hops its route must no longer use."},
				{Name: "state", Type: "string", Default: "present", Choices: []string{"present", "absent"}, ChoicesIgnoreCase: true, Description: "present waits for a selected route to every prefix given; absent waits until none of them has one (or, for a prefix next_hops names, until its route uses none of those next hops). Case is ignored."},
				{Name: "stable_for", Type: "int", Default: "5", Description: "Seconds the routing table must stay unchanged."},
				{Name: "timeout", Type: "int", Default: "120", Description: "Seconds to wait in all."},
				hostKeyParam,
				{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a Cisco IOS device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
			},
			Returns: []collection.ReturnField{
				{Name: "waited", Type: "float", Returned: "always", Description: "Seconds waited."},
				{Name: "routes", Type: "dict", Returned: "always", Description: "Each prefix given, with the next hops its route used; with state absent, an empty list for a prefix with no route left."},
			},
			Examples: []collection.Example{
				{Name: "Wait for OSPF to settle", RunbookYAML: "- name: Every router routes both LANs\n  wait.routes:\n    prefixes: [10.1.1.0/24, 10.3.3.0/24]\n"},
				{Name: "Wait for a withdrawn network to leave the table", RunbookYAML: "- name: R1 no longer routes the retired LAN\n  wait.routes:\n    target: R1\n    prefixes: [10.9.9.0/24]\n    state: absent\n"},
				{Name: "Wait for a branch to learn the data center over its tunnel", RunbookYAML: "- name: BR1 routes the data center through the hub\n  wait.routes:\n    target: BR1\n    prefixes: [10.0.0.0/16]\n    next_hops: {10.0.0.0/16: [10.255.0.1]}\n    timeout: 180\n"},
			},
			SeeAlso: []string{"net.frr.config", "net.trace"},
		},
	},
	{
		Name:          "net.trace",
		Capabilities:  []capability.Name{capability.NameLinux, capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		// The container shape (container: a container on a container host,
		// through its engine) is declared on the method's own manifest as a
		// collection.CallShape, which the scaffold has no field for; the
		// capabilities and transports above are the default shape's.
		Doc: traceDoc,
	},
	{
		Name:          "net.ovs.bridge",
		Capabilities:  []capability.Name{capability.NameOpenVSwitch, capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes an Open vSwitch bridge exist with the datapath given, or not exist.",
			Description: "Reads the bridge through ovs-vsctl and changes only what differs. Its undo puts back what it found: a bridge it created is removed, a datapath it changed is set back. Removing a bridge also removes its ports, which its undo does not recreate, so that undo is partial and says so.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The bridge."},
				{Name: "state", Type: "string", Default: "present", Choices: []string{"present", "absent"}, Description: "present or absent."},
				{Name: "datapath_type", Type: "string", Choices: []string{"system", "netdev"}, Description: "system (the kernel datapath) or netdev (userspace). Left unset, an existing bridge keeps its own and a new one gets Open vSwitch's default."},
				hostKeyParam,
			},
			Returns: []collection.ReturnField{
				{Name: "exists", Type: "bool", Returned: "always", Description: "Whether the bridge exists now."},
				{Name: "datapath_type", Type: "string", Returned: "when it exists", Description: "Its datapath now."},
				{Name: "inverse", Type: "dict", Returned: "when something changed", Description: "The undo."},
			},
			Examples: []collection.Example{
				{Name: "A userspace bridge", RunbookYAML: "- name: br0 exists\n  net.ovs.bridge:\n    name: br0\n    datapath_type: netdev\n"},
			},
			SeeAlso: []string{"net.ovs.port"},
		},
	},
	{
		Name:          "net.ovs.port",
		Capabilities:  []capability.Name{capability.NameOpenVSwitch, capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes a port exist on an Open vSwitch bridge with the VLAN tag given, or not exist.",
			Description: "Reads the port through ovs-vsctl and changes only what differs. Its undo puts back what it found: a port it added is removed, one it removed is added back on the bridge it was on, and a tag it changed is set back.",
			Params: []collection.Param{
				{Name: "bridge", Type: "string", Required: true, Description: "The bridge the port belongs on."},
				{Name: "name", Type: "string", Required: true, Description: "The port, usually an interface name (eth1)."},
				{Name: "state", Type: "string", Default: "present", Choices: []string{"present", "absent"}, Description: "present or absent."},
				{Name: "tag", Type: "int", Description: "An access VLAN, 1 to 4094; 0 removes the tag. Left unset, an existing port keeps its own."},
				hostKeyParam,
			},
			Returns: []collection.ReturnField{
				{Name: "exists", Type: "bool", Returned: "always", Description: "Whether the port exists now."},
				{Name: "tag", Type: "int", Returned: "when it exists", Description: "Its access VLAN now, 0 for none."},
				{Name: "inverse", Type: "dict", Returned: "when something changed", Description: "The undo."},
			},
			Examples: []collection.Example{
				{Name: "A port on br0", RunbookYAML: "- name: h1's port is on br0\n  net.ovs.port:\n    bridge: br0\n    name: eth2\n"},
			},
			SeeAlso: []string{"net.ovs.bridge"},
		},
	},
	{
		Name:          "net.batfish.check",
		Capabilities:  []capability.Name{capability.NameBatfishAPI},
		Transports:    []string{"https"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Checks configurations against Batfish's model of the network before any of them is applied, and fails closed.",
			Description: "Builds a snapshot from each device's whole configuration (config, or src: a file in the project), its secrets masked before anything is sent (equal secrets alike, so key mismatches still show), asks Batfish to parse it, and holds the model to the assertions given: OSPF sessions established, a route present (through the next hops named) or absent, a flow delivered or not and through or around the nodes named, and a node's modelled routes equal to the routes net.frr.facts read from it live. Each scenario forks the snapshot with interfaces or nodes down and holds it to its own assertions, so a link failure is checked before it happens. Its target is the batfish device. FRR configurations go to Batfish in the layout it reads FRR in, with each ip ospf passive moved under router ospf; Cisco IOS ones go as they are. It fails closed: an assertion that does not hold, a file Batfish could not parse, a question it could not answer, a convert error Batfish reports (part of a device missing from the model), or a line it did not recognise that the task does not accept by name with a reason (lines with no meaning of their own, exit, end and !, need none). Recognising a line is not modelling it, so a feature measured to be parsed and then ignored (on Cisco IOS: GLBP, IP SLA, SLA tracking, BFD, NHRP) is refused the same way until accepted by name: the model cannot predict its failover, and a passing gate must not imply that it did. The result reports three things apart: whether the model parsed (each file's status and every unrecognised line), whether it agrees (each assertion), and what it does not model (the lines accepted). It returns the SHA-256 of each configuration as given, for net.frr.config's expect_sha256, so the change applied is the one checked. It changes nothing on any device; Batfish keeps the snapshot in a network of the run's own, removed when the task ends. A model that agrees is evidence, not proof: rehearse on the same OS image, and verify the change live.",
			Params: []collection.Param{
				{Name: "devices", Type: "list of dict", Required: true, Description: "Each device's whole configuration: name (the node's name, which a Cisco configuration's hostname must match), format (frr, the default, or cisco_ios), and config (the text, e.g. a registered net.frr.facts result's running_config) or src (a file in the project, read by the command line only)."},
				{Name: "assertions", Type: "list of dict", Description: "What must hold of the configurations as given, each a map with a single key: ospf_established: true (every OSPF session Batfish finds is established, and there is one); bgp_established: true (the same for BGP); route: {node, prefix, next_hops, protocol, vrf}, where protocol is bgp, connected, eigrp, isis, kernel, local, ospf, rip or static (Batfish's own ibgp, aggregate, ospfIA, ospfE1, ospfE2, isisL1, isisL2 and eigrpEX are read as their family, so ibgp is written bgp); no_route: {node, prefix, vrf} (vrf narrows either to one VRF, such as default or a front-door VRF; without it every VRF the model has counts, so a front door's default route is a route to 0.0.0.0/0); reach: {from (node, every interface of it; node[interface], the node sending from that interface's address in its VRF; or @enter(node[interface]), a packet arriving on it from the wire, which Batfish cannot trace for an interface in a VRF; the interface may be quoted, node[\"Ethernet0/1\"]), dst (an address), delivered (default true), through (nodes), avoids (nodes), accepted_by (the node that must answer dst, such as an HSRP or VRRP group's elected owner of a virtual address)}; routes_match: {node, routes (net.frr.facts' routes for that node), ignore (prefixes the configurations do not hold, such as a management network), vrf}, comparing connected, static, OSPF, BGP, IS-IS, EIGRP and RIP routes by prefix, protocol and next hop; question: {name (any Batfish question, e.g. hsrpProperties, filterLineReachability), variables, where (column: value), min_rows (default 1), max_rows}, the rows matching where counted."},
				{Name: "scenarios", Type: "list of dict", Description: "Each a name, down_interfaces (node[interface], e.g. r1[eth2]) and down_nodes, and its own assertions, checked on the snapshot with those down."},
				{Name: "network", Type: "string", Default: "pleiades", Description: "What the name of the Batfish network this task makes begins with, followed by a hyphen and 16 random hexadecimal digits (a lab's name, such as pleiades-cvd-opus, makes pleiades-cvd-opus-1f3a...), so the networks each lab makes on one shared Batfish service read as that lab's. 1 to 64 letters, digits, dots, hyphens and underscores, beginning with a letter or digit. The random part keeps two runs given the same name apart, and each task deletes only the network it made, when it ends. The whole name is returned as network."},
				{Name: "expect_devices", Type: "list of string", Description: "Names of every device the lab holds that this check is meant to stand for. One with no entry in devices (a Linux bridge, a host) is listed in not_modelled as absent from the model, so a passing check is not read as covering a device it never saw. Reported only; it never fails the task."},
				{Name: "accept_unrecognized", Type: "list of dict", Description: "Lines Batfish did not recognise, or features it does not model, that the task accepts: each a text (the line, trimmed; a line holding a secret is matched without it, so ip ospf authentication-key accepts that line) or a feature (glbp, ip_sla, sla_tracking, bfd, nhrp), and a reason (why the model may ignore it). Every other unrecognised line fails the task."},
			},
			Returns: []collection.ReturnField{
				{Name: "network", Type: "string", Returned: "once the network is made", Description: "The Batfish network the task made and removed when it ended, its name beginning with the network parameter."},
				{Name: "passed", Type: "bool", Returned: "always", Description: "Whether every assertion held and nothing went unrecognised unaccepted."},
				{Name: "sha256", Type: "dict", Returned: "always", Description: "Each device's configuration's SHA-256, in hex, over the bytes as given."},
				{Name: "parse", Type: "list of dict", Returned: "always", Description: "Each file's parse status (device, status)."},
				{Name: "unrecognized", Type: "list of dict", Returned: "always", Description: "Each unrecognised line (device, line, text) and, when accepted, why."},
				{Name: "assertions", Type: "list of dict", Returned: "always", Description: "Each assertion (scenario, kind, passed, detail)."},
				{Name: "not_modelled", Type: "list of string", Returned: "always", Description: "What the model ignores: each accepted line, with its reason."},
			},
			Examples: []collection.Example{
				{Name: "Check a candidate and a link failure first", RunbookYAML: "- name: Read r1 as it runs\n  net.frr.facts:\n    target: r1\n    include_config: true\n  register: r1\n- name: Read r2 as it runs\n  net.frr.facts:\n    target: r2\n    include_config: true\n  register: r2\n- name: The model routes both LANs with r3's change, and without r1's link to r2\n  net.batfish.check:\n    target: batfish\n    devices:\n      - {name: r1, config: \"{{ result.r1.running_config }}\"}\n      - {name: r2, config: \"{{ result.r2.running_config }}\"}\n      - {name: r3, src: configs/r3.frr.conf}\n    assertions:\n      - ospf_established: true\n      - route: {node: r1, prefix: 10.3.3.0/24}\n    scenarios:\n      - name: r1-r2 down\n        down_interfaces: [\"r1[eth1]\"]\n        assertions:\n          - reach: {from: \"r1[eth3]\", dst: 10.3.3.10, through: [r3]}\n    accept_unrecognized:\n      - {text: domainname localdomain, reason: \"DNS search domain, not routing\"}\n  register: gate\n"},
			},
			SeeAlso: []string{"net.frr.config", "net.frr.facts", "wait.routes", "net.trace"},
		},
	},
}

// traceDoc is net.trace's reference documentation, identical to the copy
// its implementation registers.
var traceDoc = collection.Doc{
	Summary:     "Traces the path to an address from a Linux device or a container on a container host, and fails when it is not the path expected.",
	Description: "Runs tracepath, which needs no raw socket and so no privilege, and returns each hop's address. Without container it runs on the device, a Linux machine reached over SSH. With container it runs inside that container on the device, a container host, through the engine's own exec as container.exec does, so a lab host with no SSH of its own (a containerlab end host) can be traced from; the container needs tracepath (iputils) and /bin/sh. pleiades validate refuses a call whose device cannot take the shape it chose: container on a device that is not a container host, or none on one that is not a Linux SSH device. With expect, it fails the task when the destination was not reached, or a hop named in includes is missing, or one named in avoids is on the path. It changes nothing.",
	Params: []collection.Param{
		{Name: "dest", Type: "string", Required: true, Description: "The address to trace to.", Format: collection.ParamFormatCommand},
		{Name: "max_hops", Type: "int", Default: "30", Description: "The most hops to try."},
		{Name: "expect", Type: "dict", Description: "reached (bool), includes (list of addresses that must be hops) and avoids (list that must not be)."},
		{Name: "container", Type: "string", Description: "Trace from this container on the device, a container host, rather than from the device itself. The device then needs a container engine, not SSH and Linux. An empty value is refused rather than read as none: leave container out to trace from the device."},
		{Name: "engine", Type: "string", Choices: []string{"docker", "podman"}, Description: "With container, the engine on a device that runs both Docker and Podman: docker or podman. Never inferred."},
		hostKeyParam,
	},
	Returns: []collection.ReturnField{
		{Name: "hops", Type: "list of string", Returned: "always", Description: "Each hop's address, in order, the destination last when reached."},
		{Name: "reached", Type: "bool", Returned: "always", Description: "Whether the destination answered."},
	},
	Examples: []collection.Example{
		{Name: "Expect a path through r2", RunbookYAML: "- name: h1's LAN reaches h2 through r2\n  net.trace:\n    dest: 10.3.3.10\n    expect:\n      reached: true\n      includes: [10.0.12.1]\n"},
		{Name: "Trace from a lab host container", RunbookYAML: "- name: h1 reaches h2 through r1\n  net.trace:\n    target: dockerhost\n    container: clab-routing-h1\n    dest: 10.3.3.10\n    expect:\n      reached: true\n      includes: [10.1.1.1]\n"},
	},
	SeeAlso: []string{"wait.routes", "net.batfish.check", "container.exec"},
}
