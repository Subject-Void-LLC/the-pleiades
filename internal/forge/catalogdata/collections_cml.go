// This file holds the virt.cml.* Collection: labs, nodes and links on a
// Cisco Modeling Labs 2 controller, built and taken apart through its REST
// API against a cml_controller device, one verb per method, each with its
// own undo.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// cmlTargetNote is what every virt.cml method says about its target.
const cmlTargetNote = " The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API."

// cmlLabParam names the lab a method acts on.
var cmlLabParam = collection.Param{Name: "lab", Type: "string", Required: true,
	Description: "The lab, by id or by title. A title two labs share is refused, naming both ids; an undo always names the id."}

// cmlNodesParam narrows a start or a stop to some nodes.
var cmlNodesParam = collection.Param{Name: "nodes", Type: "list",
	Description: "The nodes to act on, by label or id; every node of the lab when empty. A rollback sets it to exactly the nodes the run changed."}

// cmlTimeoutParam bounds a wait.
var cmlTimeoutParam = collection.Param{Name: "timeout", Type: "int", Default: "900",
	Description: "Seconds to wait for every node started to report BOOTED."}

var cmlCollections = []collectionscaffold.Config{
	{
		Name:          "virt.cml.lab.list",
		Capabilities:  []capability.Name{capability.NameCMLAPI},
		Transports:    []string{"https"},
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Lists the labs on a CML controller.",
			Description: "Reads every lab the controller's account can see, with its title, state and node count, and changes nothing." + cmlTargetNote + " A check is the same read.",
			Returns: []collection.ReturnField{
				{Name: "labs", Type: "list", Returned: "always", Description: "Each lab as id, title, state (DEFINED_ON_CORE, STARTED or STOPPED) and nodes (its node count), by title."},
			},
			Examples: []collection.Example{
				{Name: "List the labs", RunbookYAML: "- name: List the controller's labs\n  virt.cml.lab.list: {}\n  register: labs\n"},
			},
			SeeAlso: []string{"virt.cml.lab.info"},
		},
	},
	{
		Name:          "virt.cml.lab.info",
		Capabilities:  []capability.Name{capability.NameCMLAPI},
		Transports:    []string{"https"},
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Reports one CML lab: its nodes, their interfaces and its links.",
			Description: "Reads one lab and changes nothing. A lab that does not exist is not a failure: exists is false and nothing else is reported, so a later task can decide what to do." + cmlTargetNote + " A check is the same read.",
			Params:      []collection.Param{cmlLabParam},
			Returns: []collection.ReturnField{
				{Name: "exists", Type: "bool", Returned: "always", Description: "Whether the controller holds the lab."},
				{Name: "id", Type: "string", Returned: "when exists", Description: "The lab's id."},
				{Name: "title", Type: "string", Returned: "when exists", Description: "The lab's title."},
				{Name: "state", Type: "string", Returned: "when exists", Description: "DEFINED_ON_CORE, STARTED or STOPPED."},
				{Name: "nodes", Type: "list", Returned: "when exists", Description: "Each node as id, label, definition, state and interfaces (each as label and connected)."},
				{Name: "links", Type: "list", Returned: "when exists", Description: "Each link as id, a, a_interface, b and b_interface, by node label and interface label."},
			},
			Examples: []collection.Example{
				{Name: "Read a lab", RunbookYAML: "- name: Read the CVD lab\n  virt.cml.lab.info:\n    lab: cvd\n  register: lab\n"},
			},
			SeeAlso: []string{"virt.cml.lab.list", "virt.cml.lab.create"},
		},
	},
	{
		Name:          "virt.cml.lab.create",
		Capabilities:  []capability.Name{capability.NameCMLAPI},
		Transports:    []string{"https"},
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Creates an empty CML lab with a title.",
			Description: "Makes a lab with no nodes. A title a lab already has is refused, since CML lets two labs share one and every later task names its lab by title; a second run of the same task is therefore a refusal, not a second lab. Its undo is virt.cml.lab.delete of the lab's id, so a rollback removes exactly the lab this task made." + cmlTargetNote + " A check reads whether the title is free and changes nothing.",
			Params: []collection.Param{
				{Name: "title", Type: "string", Required: true, Description: "The new lab's title."},
			},
			Returns: []collection.ReturnField{
				{Name: "id", Type: "string", Returned: "on change", Description: "The new lab's id."},
				{Name: "title", Type: "string", Returned: "always", Description: "The lab's title."},
			},
			Examples: []collection.Example{
				{Name: "Make a lab", RunbookYAML: "- name: Make the CVD lab\n  virt.cml.lab.create:\n    title: cvd\n"},
			},
			SeeAlso: []string{"virt.cml.node.create", "virt.cml.lab.delete"},
		},
	},
	{
		Name:          "virt.cml.lab.start",
		Capabilities:  []capability.Name{capability.NameCMLAPI},
		Transports:    []string{"https"},
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Starts a CML lab's nodes and waits until they boot.",
			Description: "Starts each of the lab's nodes (or those nodes names) that is not running, then waits until every one of them reports BOOTED, up to timeout. A node already running is left alone, and a lab whose nodes all run is unchanged. Its undo is virt.cml.lab.stop of exactly the nodes this task started, so a rollback never stops a node someone else started." + cmlTargetNote + " A check reads which nodes it would start and changes nothing.",
			Params:      []collection.Param{cmlLabParam, cmlNodesParam, cmlTimeoutParam},
			Returns: []collection.ReturnField{
				{Name: "started", Type: "list", Returned: "always", Description: "The labels of the nodes this task started."},
				{Name: "lab_id", Type: "string", Returned: "always", Description: "The lab's id."},
			},
			Examples: []collection.Example{
				{Name: "Start a lab", RunbookYAML: "- name: Start the CVD lab\n  virt.cml.lab.start:\n    lab: cvd\n    timeout: 1200\n"},
			},
			SeeAlso: []string{"virt.cml.lab.stop"},
		},
	},
	{
		Name:          "virt.cml.lab.stop",
		Capabilities:  []capability.Name{capability.NameCMLAPI},
		Transports:    []string{"https"},
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Stops a CML lab's nodes.",
			Description: "Stops each of the lab's nodes (or those nodes names) that is running, keeping their disks. A node already stopped is left alone. Its undo is virt.cml.lab.start of exactly the nodes this task stopped." + cmlTargetNote + " A check reads which nodes it would stop and changes nothing.",
			Params:      []collection.Param{cmlLabParam, cmlNodesParam},
			Returns: []collection.ReturnField{
				{Name: "stopped", Type: "list", Returned: "always", Description: "The labels of the nodes this task stopped."},
				{Name: "lab_id", Type: "string", Returned: "always", Description: "The lab's id."},
			},
			Examples: []collection.Example{
				{Name: "Stop one node", RunbookYAML: "- name: Take CORE1 down to prove the second hub carries the branches\n  virt.cml.lab.stop:\n    lab: cvd\n    nodes:\n      - CORE1\n"},
			},
			SeeAlso: []string{"virt.cml.lab.start"},
		},
	},
	{
		Name:          "virt.cml.lab.delete",
		Capabilities:  []capability.Name{capability.NameCMLAPI},
		Transports:    []string{"https"},
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Stops, wipes and deletes a CML lab.",
			Description: "Stops every node, wipes every node's disks and removes the lab. A lab that does not exist is unchanged. It cannot be undone: every node's disk and configuration is gone, which is why it is a method of its own and never part of another." + cmlTargetNote + " A check reads whether the lab exists and changes nothing.",
			Params:      []collection.Param{cmlLabParam},
			Returns: []collection.ReturnField{
				{Name: "lab_id", Type: "string", Returned: "on change", Description: "The id of the lab removed."},
			},
			Examples: []collection.Example{
				{Name: "Remove a lab", RunbookYAML: "- name: Remove the CVD lab\n  virt.cml.lab.delete:\n    lab: cvd\n"},
			},
			SeeAlso: []string{"virt.cml.lab.create"},
		},
	},
	{
		Name:          "virt.cml.node.create",
		Capabilities:  []capability.Name{capability.NameCMLAPI},
		Transports:    []string{"https"},
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Creates a node in a CML lab, optionally seeded with an inventory device's login and address.",
			Description: "Makes a node from a node definition, with the definition's interfaces, stopped. With device, the node stands for that inventory device (a cisco_router or cisco_switch; only an IOS or IOS XE definition): its day-0 configuration sets the hostname to the label, puts the device's host on management_interface in the Mgmt-intf VRF with a default route to management_gateway, enables SSH, and creates the device's login as \"username <user> privilege 15 secret 9 <hash>\" (and \"enable secret 9\" when the credential has an enable password), from the device's stored credential. The passwords never reach the runbook or the task's output: the method is handed type 9 hashes only. The hashes are in the node's configuration, which the controller keeps and anyone who can read the lab can read, so seed a node only from a device whose credential exists only in the lab. The console line logs an idle session out after five minutes. configuration adds lines after those. A label the lab already uses is refused, so a second run is a refusal rather than a second node. Its undo is virt.cml.node.delete of the node's id." + cmlTargetNote + " A check reads the lab, the definition and the label and changes nothing.",
			Params: []collection.Param{
				cmlLabParam,
				{Name: "label", Type: "string", Required: true, Description: "The node's label in the lab, also its hostname when device is set."},
				{Name: "definition", Type: "string", Required: true, Description: "The node definition's id, e.g. iol-xe."},
				{Name: "device", Type: "string", Description: "The inventory device the node stands for: its host becomes the management address and its stored login is seeded as IOS secrets."},
				{Name: "management_interface", Type: "string", Default: "Ethernet0/0", Description: "The interface that carries the management address."},
				{Name: "management_prefix", Type: "int", Default: "24", Description: "The management address's prefix length."},
				{Name: "management_gateway", Type: "string", Description: "The management network's gateway, the Mgmt-intf VRF's default route. Required with device."},
				{Name: "configuration", Type: "string", Description: "Day-0 configuration lines added after the seeded ones, or the whole day-0 configuration without device. Never put a password here: use device."},
				{Name: "interfaces", Type: "int", Description: "The least number of physical interfaces the node has. A definition gives a few (iol-xe four, Ethernet0/0 to Ethernet0/3); more are added slot by slot, named as the definition names them (iol-xe's fifth is Ethernet1/0). Up to 128, and no more than the definition allows, which the controller enforces."},
				{Name: "x", Type: "int", Default: "0", Description: "The node's place on the lab's canvas, left to right."},
				{Name: "y", Type: "int", Default: "0", Description: "The node's place on the lab's canvas, top to bottom."},
			},
			Returns: []collection.ReturnField{
				{Name: "id", Type: "string", Returned: "on change", Description: "The new node's id."},
				{Name: "interfaces", Type: "list", Returned: "on change", Description: "Its interfaces' labels, in slot order."},
				{Name: "management_address", Type: "string", Returned: "when device is set", Description: "The address the node's management interface was given."},
			},
			Examples: []collection.Example{
				{Name: "A core router standing for an inventory device", RunbookYAML: "- name: Make CORE1\n  virt.cml.node.create:\n    lab: cvd\n    label: CORE1\n    definition: iol-xe\n    device: CORE1\n    management_gateway: 10.10.20.254\n    x: 0\n    y: 0\n"},
			},
			SeeAlso: []string{"virt.cml.link.create", "virt.cml.node.delete", "virt.cml.lab.start"},
		},
	},
	{
		Name:          "virt.cml.node.delete",
		Capabilities:  []capability.Name{capability.NameCMLAPI},
		Transports:    []string{"https"},
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Stops, wipes and deletes one node of a CML lab.",
			Description: "Stops the node, wipes its disks and removes it with its links. A node that does not exist is unchanged. It cannot be undone: the node's disk and configuration are gone." + cmlTargetNote + " A check reads whether the node exists and changes nothing.",
			Params: []collection.Param{
				cmlLabParam,
				{Name: "node", Type: "string", Required: true, Description: "The node, by label or id."},
			},
			Returns: []collection.ReturnField{
				{Name: "node_id", Type: "string", Returned: "on change", Description: "The id of the node removed."},
			},
			Examples: []collection.Example{
				{Name: "Remove a node", RunbookYAML: "- name: Remove BR3\n  virt.cml.node.delete:\n    lab: cvd\n    node: BR3\n"},
			},
			SeeAlso: []string{"virt.cml.node.create"},
		},
	},
	{
		Name:          "virt.cml.link.create",
		Capabilities:  []capability.Name{capability.NameCMLAPI},
		Transports:    []string{"https"},
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Links two nodes' interfaces in a CML lab.",
			Description: "Links interface a_interface of node a to interface b_interface of node b. The same link already there is unchanged; an interface already linked to anything else is refused. Its undo is virt.cml.link.delete of the link's id." + cmlTargetNote + " A check reads both interfaces and changes nothing.",
			Params: []collection.Param{
				cmlLabParam,
				{Name: "a", Type: "string", Required: true, Description: "One node, by label or id."},
				{Name: "a_interface", Type: "string", Required: true, Description: "Its interface, by label, e.g. Ethernet0/1."},
				{Name: "b", Type: "string", Required: true, Description: "The other node, by label or id."},
				{Name: "b_interface", Type: "string", Required: true, Description: "Its interface, by label."},
			},
			Returns: []collection.ReturnField{
				{Name: "id", Type: "string", Returned: "always", Description: "The link's id."},
			},
			Examples: []collection.Example{
				{Name: "Link a branch to a core", RunbookYAML: "- name: Link BR1 to CORE1\n  virt.cml.link.create:\n    lab: cvd\n    a: CORE1\n    a_interface: Ethernet0/1\n    b: BR1\n    b_interface: Ethernet0/1\n"},
			},
			SeeAlso: []string{"virt.cml.link.delete", "virt.cml.node.create"},
		},
	},
	{
		Name:          "virt.cml.link.delete",
		Capabilities:  []capability.Name{capability.NameCMLAPI},
		Transports:    []string{"https"},
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Removes a link from a CML lab.",
			Description: "Removes the link named by link, or the one between a's a_interface and b's b_interface (one way or the other, never both). A link that does not exist is unchanged. Its undo is virt.cml.link.create of the same two interfaces." + cmlTargetNote + " A check reads whether the link exists and changes nothing.",
			Params: []collection.Param{
				cmlLabParam,
				{Name: "link", Type: "string", Description: "The link's id."},
				{Name: "a", Type: "string", Description: "One node, by label or id, with a_interface, b and b_interface."},
				{Name: "a_interface", Type: "string", Description: "Its interface, by label."},
				{Name: "b", Type: "string", Description: "The other node, by label or id."},
				{Name: "b_interface", Type: "string", Description: "Its interface, by label."},
			},
			Returns: []collection.ReturnField{
				{Name: "id", Type: "string", Returned: "on change", Description: "The id of the link removed."},
			},
			Examples: []collection.Example{
				{Name: "Cut a branch's uplink", RunbookYAML: "- name: Cut BR1's link to CORE1\n  virt.cml.link.delete:\n    lab: cvd\n    a: CORE1\n    a_interface: Ethernet0/1\n    b: BR1\n    b_interface: Ethernet0/1\n"},
			},
			SeeAlso: []string{"virt.cml.link.create"},
		},
	},
}
