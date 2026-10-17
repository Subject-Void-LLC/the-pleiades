// The container.network.* methods' documentation, kept equal to
// internal/forge/catalogdata's copy by internal/archtest's
// TestCatalogDataDocsMatchTheRegistry.
package network

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
)

// Shared parameters and returns.
var (
	nameParam        = collection.Param{Name: paramName, Type: "string", Required: true, Description: "The network's name."}
	networkParam     = collection.Param{Name: paramNetwork, Type: "string", Required: true, Description: "The network's name."}
	containerParam   = collection.Param{Name: paramContainer, Type: "string", Required: true, Description: "The container's name."}
	networkIDParam   = collection.Param{Name: paramNetworkID, Type: "string", Description: "The network's ID, which the network named must still have: an undo recorded against one network never acts on another that took its name since."}
	containerIDParam = collection.Param{Name: paramContainerID, Type: "string", Description: "The container's ID, which the container named must still have."}

	nameReturn = collection.ReturnField{Name: statName, Type: "string", Returned: "always", Description: "The network's name."}
	idReturn   = collection.ReturnField{Name: statID, Type: "string", Returned: "when the network exists", Description: "The network's ID."}

	pairReturns = []collection.ReturnField{
		{Name: statNetwork, Type: "string", Returned: "always", Description: "The network's name."},
		{Name: statContainer, Type: "string", Returned: "always", Description: "The container's name."},
		{Name: statNetworkID, Type: "string", Returned: "when both exist", Description: "The network's ID."},
		{Name: statContainerID, Type: "string", Returned: "when both exist", Description: "The container's ID."},
		{Name: statConnected, Type: "bool", Returned: "when the task succeeds", Description: "Whether the container is on the network after the task (by a check, would be)."},
	}
)

// createDoc is container.network.create's documentation.
func createDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Makes a network exist as declared.",
		Description: "Makes the network named name exist on the device's engine with the declared driver and settings. An absent network is created; one that matches is no change. One that differs (its driver, internal, enable_ipv6, or a declared subnet, gateway, IP range, MTU, parent or label) is refused, naming each difference, and never replaced, since replacing a network disconnects every container on it. A setting the task leaves out is the engine's choice and is not compared, so a network made with an engine-chosen subnet still matches. The engine's own networks (bridge, host, none, podman) are refused. Records removing the network, by ID, as the undo. A check reads the network and creates nothing." + containerengine.ReachNote,
		Params: []collection.Param{
			nameParam,
			{Name: paramDriver, Type: "string", Default: driverBridge, Choices: drivers, Description: "The network driver: bridge (a private network on the host), or macvlan or ipvlan (containers on the parent interface's own network)."},
			{Name: paramInternal, Type: "bool", Default: "false", Description: "Give the network no route outside the host: its containers reach each other and nothing else."},
			{Name: paramIPv6, Type: "bool", Default: "false", Description: "Give the network IPv6 addresses as well as IPv4 ones. Required for an IPv6 subnet."},
			{Name: paramSubnet, Type: "string", Description: "The network's subnet, an address with its prefix length (10.20.0.0/24). Left out, the engine chooses one from its pool."},
			{Name: paramGateway, Type: "string", Description: "The gateway address, inside subnet. Needs subnet."},
			{Name: paramIPRange, Type: "string", Description: "The part of subnet containers are given addresses from, an address with its prefix length. Needs subnet."},
			{Name: paramMTU, Type: "int", Description: "The bridge's MTU, from 68 to 65535. Bridge only: macvlan and ipvlan take their parent's."},
			{Name: paramParent, Type: "string", Description: "The host interface a macvlan or ipvlan network sits on (eth0, or eth0.10 for a VLAN). Left out, the engine makes a network with no way out."},
			{Name: paramLabels, Type: "dict", Description: "Labels on the network. Labels it has beyond these are allowed."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			nameReturn, idReturn,
			{Name: statDriver, Type: "string", Returned: "always", Description: "The network's driver."},
		},
		Examples: []collection.Example{
			{Name: "A private network for an application", RunbookYAML: "- name: App network\n  container.network.create:\n    name: appnet\n    subnet: 10.20.0.0/24\n    labels:\n      app: shop\n"},
			{Name: "A network with no way out", RunbookYAML: "- name: Back end\n  container.network.create:\n    name: backend\n    internal: true\n"},
		},
		SeeAlso: []string{"container.network.remove", "container.network.connect", "container.network.info"},
	}
}

// removeDoc is container.network.remove's documentation.
func removeDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Removes a network no container uses.",
		Description: "Removes the network named name; an absent one is no change. A network any container uses, running or stopped, is refused, naming them: a stopped container that declares the network could not start once it is gone. The engine's own networks are refused. With id, a network that no longer has that ID is refused, so an undo never removes another network that took the name. Records creating the network again with the settings it had as the undo: driver, internal, IPv6, its first subnet with its gateway and range, MTU and parent are kept in the journal, and labels are not, so an undo for a labeled network cannot be replayed from the journal. The undo is partial when the network had an option or a second subnet no parameter of container.network.create sets. A check reads the network and the containers using it, and removes nothing." + containerengine.ReachNote,
		Params: []collection.Param{
			nameParam,
			{Name: paramID, Type: "string", Description: "The network's ID, which the network named must still have: an undo recorded against one network never acts on another that took its name since."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			nameReturn, idReturn,
			{Name: statRemoved, Type: "bool", Returned: "when the network existed", Description: "Whether the network was removed (by a check, would be)."},
		},
		Examples: []collection.Example{{Name: "Remove an old network", RunbookYAML: "- name: Remove the old network\n  container.network.remove:\n    name: appnet-old\n"}},
		SeeAlso:  []string{"container.network.create", "container.network.disconnect"},
	}
}

// connectDoc is container.network.connect's documentation.
func connectDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Connects a container to a network.",
		Description: "Connects the container named container to the network named network, with the declared aliases and static IPv4 address. A container already on the network with them is no change; one on it without them is refused, naming each difference, since changing a container's place on a network means disconnecting it. Both must exist. A container whose network mode is none, host or another container's has no network to join, and is refused, naming container.recreate. With network_id or container_id, an object that no longer has that ID is refused. Records disconnecting it, by both IDs, as the undo. A check reads both and connects nothing." + containerengine.ReachNote,
		Params: []collection.Param{
			networkParam, containerParam,
			{Name: paramAliases, Type: "list", Description: "Names the container answers to on this network, beside its own."},
			{Name: paramIPv4, Type: "string", Description: "A static IPv4 address for the container on this network, inside a subnet the network was created with."},
			networkIDParam, containerIDParam,
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns:   pairReturns,
		Examples: []collection.Example{
			{Name: "Put the web server on the application network", RunbookYAML: "- name: Join appnet\n  container.network.connect:\n    network: appnet\n    container: web\n    aliases: [shop]\n"},
		},
		SeeAlso: []string{"container.network.disconnect", "container.network.create", "container.run"},
	}
}

// disconnectDoc is container.network.disconnect's documentation.
func disconnectDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Disconnects a container from a network.",
		Description: "Disconnects the container named container from the network named network. A container not on the network, or a network or container that does not exist, is no change. With network_id or container_id, an object that no longer has that ID is refused, so an undo never acts on another that took the name. Records connecting it again, by both IDs, with the aliases and static IPv4 address it had, as the undo. A check reads both and disconnects nothing." + containerengine.ReachNote,
		Params: []collection.Param{
			networkParam, containerParam, networkIDParam, containerIDParam,
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns:   pairReturns,
		Examples: []collection.Example{
			{Name: "Take the web server off the old network", RunbookYAML: "- name: Leave appnet-old\n  container.network.disconnect:\n    network: appnet-old\n    container: web\n"},
		},
		SeeAlso: []string{"container.network.connect", "container.network.remove"},
	}
}

// infoDoc is container.network.info's documentation.
func infoDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Reads a network.",
		Description: "Reads the network named name as the engine reports it: its ID, driver, scope, internal, IPv6, subnets, labels, driver options and the running containers on it. An absent network is reported as exists false rather than refused, so a runbook can branch on it. Reading changes nothing, and a check reads the same way." + containerengine.ReachNote,
		Params:      []collection.Param{{Name: paramName, Type: "string", Required: true, Description: "The network's name."}, containerengine.HostKeyParam},
		Fragments:   []string{"container_engine"},
		Returns: []collection.ReturnField{
			nameReturn,
			{Name: statExists, Type: "bool", Returned: "always", Description: "Whether the network exists."},
			idReturn,
			{Name: statDriver, Type: "string", Returned: "when the network exists", Description: "The network's driver."},
			{Name: statScope, Type: "string", Returned: "when the network exists", Description: "The network's scope: local, or swarm."},
			{Name: statInternal, Type: "bool", Returned: "when the network exists", Description: "Whether the network has no route outside the host."},
			{Name: statIPv6, Type: "bool", Returned: "when the network exists", Description: "Whether the network has IPv6 addresses."},
			{Name: statSubnets, Type: "list of dict", Returned: "when the network exists", Description: "Each subnet, with its gateway and IP range when it has them."},
			{Name: statLabels, Type: "dict", Returned: "when the network exists", Description: "The network's labels."},
			{Name: statOptions, Type: "dict", Returned: "when the network exists", Description: "The network's driver options, as the engine reports them."},
			{Name: statContainers, Type: "list of string", Returned: "when the network exists", Description: "The names of the running containers on the network. A stopped container that declares it is not listed."},
		},
		Examples: []collection.Example{
			{Name: "Read a network a later task branches on", RunbookYAML: "- name: Read appnet\n  container.network.info:\n    name: appnet\n  register: appnet\n"},
		},
		SeeAlso: []string{"container.network.create", "container.engine.info"},
	}
}
