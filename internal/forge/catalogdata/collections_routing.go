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
			Description: "Takes the whole intended configuration (config, or src: a file in the project) and applies only what differs, through FRR's own frr-reload.py. The candidate is first checked with vtysh -m -f, and a line vtysh refuses fails the task before anything reaches the router: frr-reload reads a file vtysh cannot parse as an empty configuration and would delete everything. The difference comes from frr-reload.py --test; an empty one is no change, and a check returns it as the prediction. A real run saves the running configuration to a file on the router (0600, under the connecting account's ~/.pleiades-frr), reloads, confirms the running configuration now matches the candidate, and saves the result beside the backup. Its undo names only that backup, so no configuration text, and no secret in one, reaches the journal; the undo restores exactly the configuration that was running, and refuses when the router no longer runs what this change left, unless its step names force. The undo is recorded before the reload and kept if the reload then fails, since restoring a backup when nothing changed is itself no change. With save, the running configuration is also written to frr.conf, keeping that file's owner and mode, so it survives a restart; the backup then keeps frr.conf as it was too, and the undo puts it back (where there was none, the undo writes the configuration it restores). expect_sha256 refuses a candidate whose bytes are not the ones a model check analysed.",
			Params: []collection.Param{
				{Name: "config", Type: "string", Description: "The whole intended configuration, as frr.conf text. Set config or src, not both. Lines not in it are removed from the running configuration."},
				{Name: "src", Type: "string", Description: "A file in the project directory holding the whole intended configuration, read by the command line; refused on a Walk-tier Runner, where config is the way. Set config or src, not both."},
				{Name: "save", Type: "bool", Default: "false", Description: "Also write the configuration to frr.conf (keeping that file's owner and mode), so it survives a restart. The undo puts frr.conf back as it was."},
				{Name: "expect_sha256", Type: "string", Description: "Refuse unless the candidate's SHA-256 is this, in hex: the digest net.batfish.check returned for the bytes it analysed, so the change applied is the change checked."},
				{Name: "restore", Type: "string", Description: "Restore the configuration this method saved before an earlier change, by the path its undo recorded. Set by an undo; with restore, config and src are not given."},
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
			Description: "Asks vtysh for each in JSON and returns it as data. With include_config, it also returns the running configuration with every secret in it (a password, an authentication or message-digest key, a neighbour's password) replaced by a placeholder that is the same for equal secrets throughout one command-line run, so a model can still compare two routers' keys without seeing one (a Runner reads each device apart, so there placeholders compare only within one router). The word after each secret word (password, secret, authentication-key, message-digest-key, key-string, community, key) is masked, past any encryption type, key id or algorithm between them. It changes nothing.",
			Params: []collection.Param{
				{Name: "include_config", Type: "bool", Default: "false", Description: "Also return the running configuration, its secrets replaced."},
				hostKeyParam,
			},
			Returns: []collection.ReturnField{
				{Name: "version", Type: "string", Returned: "always", Description: "The FRR version."},
				{Name: "hostname", Type: "string", Returned: "always", Description: "The router's hostname as FRR knows it."},
				{Name: "interfaces", Type: "dict", Returned: "always", Description: "Each interface, from show interface json."},
				{Name: "ospf_neighbors", Type: "dict", Returned: "always", Description: "show ip ospf neighbor json, empty when OSPF is not running."},
				{Name: "routes", Type: "dict", Returned: "always", Description: "show ip route json: every route by prefix."},
				{Name: "bgp_summary", Type: "dict", Returned: "always", Description: "show bgp summary json, empty when BGP is not running."},
				{Name: "running_config", Type: "string", Returned: "with include_config", Description: "The running configuration with its secrets replaced."},
			},
			Examples: []collection.Example{
				{Name: "Read a router for a model", RunbookYAML: "- name: Read r1\n  net.frr.facts:\n    include_config: true\n  register: r1\n"},
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
		Capabilities:  []capability.Name{capability.NameFRR, capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Waits until a router has a route to every prefix given and its routing table has stopped changing.",
			Description: "A full OSPF adjacency is not convergence: shortest paths are computed after it, and a router can route the long way, or not at all, for a while after every neighbour is up. This waits for what a change needs: a selected route to each prefix (through the given next hops, when next_hops names any), and the routing table, uptimes aside, unchanged for stable_for seconds. At timeout it fails naming each route still missing, or saying the table never held still. It changes nothing.",
			Params: []collection.Param{
				{Name: "prefixes", Type: "list of string", Required: true, Description: "The prefixes that must each have a selected route, e.g. 10.3.3.0/24."},
				{Name: "next_hops", Type: "dict", Description: "Per prefix, the next-hop addresses its route must use, e.g. {\"10.3.3.0/24\": [\"10.0.12.1\"]}."},
				{Name: "stable_for", Type: "int", Default: "5", Description: "Seconds the routing table must stay unchanged."},
				{Name: "timeout", Type: "int", Default: "120", Description: "Seconds to wait in all."},
				hostKeyParam,
			},
			Returns: []collection.ReturnField{
				{Name: "waited", Type: "float", Returned: "always", Description: "Seconds waited."},
				{Name: "routes", Type: "dict", Returned: "always", Description: "Each prefix given, with the next hops its route used."},
			},
			Examples: []collection.Example{
				{Name: "Wait for OSPF to settle", RunbookYAML: "- name: Every router routes both LANs\n  wait.routes:\n    prefixes: [10.1.1.0/24, 10.3.3.0/24]\n"},
			},
			SeeAlso: []string{"net.frr.config", "net.trace"},
		},
	},
	{
		Name:          "net.trace",
		Capabilities:  []capability.Name{capability.NameLinux, capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Traces the path from a Linux device to an address, and fails when it is not the path expected.",
			Description: "Runs tracepath, which needs no raw socket and so no privilege, and returns each hop's address. With expect, it fails the task when the destination was not reached, or a hop named in includes is missing, or one named in avoids is on the path. It changes nothing.",
			Params: []collection.Param{
				{Name: "dest", Type: "string", Required: true, Description: "The address to trace to.", Format: collection.ParamFormatCommand},
				{Name: "max_hops", Type: "int", Default: "30", Description: "The most hops to try."},
				{Name: "expect", Type: "dict", Description: "reached (bool), includes (list of addresses that must be hops) and avoids (list that must not be)."},
				hostKeyParam,
			},
			Returns: []collection.ReturnField{
				{Name: "hops", Type: "list of string", Returned: "always", Description: "Each hop's address, in order, the destination last when reached."},
				{Name: "reached", Type: "bool", Returned: "always", Description: "Whether the destination answered."},
			},
			Examples: []collection.Example{
				{Name: "Expect a path through r2", RunbookYAML: "- name: h1's LAN reaches h2 through r2\n  net.trace:\n    dest: 10.3.3.10\n    expect:\n      reached: true\n      includes: [10.0.12.1]\n"},
			},
			SeeAlso: []string{"wait.routes", "net.batfish.check"},
		},
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
				{Name: "state", Type: "string", Default: "present", Description: "present or absent."},
				{Name: "datapath_type", Type: "string", Description: "system (the kernel datapath) or netdev (userspace). Left unset, an existing bridge keeps its own and a new one gets Open vSwitch's default."},
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
				{Name: "state", Type: "string", Default: "present", Description: "present or absent."},
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
}
