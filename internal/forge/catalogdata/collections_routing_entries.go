// Package catalogdata: the entries of net.batfish.check's structured
// parameters (collection.Param.Entries), the same declarations
// internal/catalog/net/batfish registers, held equal by internal/archtest's
// TestCatalogDataDocsMatchTheRegistry.
package catalogdata

import "github.com/Subject-Void-LLC/the-pleiades/pkg/collection"

// batfishDeviceEntries are the keys of one devices entry.
var batfishDeviceEntries = []collection.Param{
	{Name: "name", Type: "string", Required: true, Description: "The node's name, which a Cisco configuration's hostname must match."},
	{Name: "format", Type: "string", Default: "frr", Choices: []string{"frr", "cisco_ios"}, Description: "The configuration's format."},
	{Name: "config", Type: "string", Description: "The whole configuration's text, e.g. a registered net.frr.facts result's running_config. Give config or src."},
	{Name: "src", Type: "string", Format: collection.ParamFormatProjectPath, Description: "A file in the project holding the whole configuration, read by the command line only. Give config or src."},
}

// batfishAssertionEntries are the kinds one assertions entry may name.
var batfishAssertionEntries = []collection.Param{
	{Name: "ospf_established", Type: "bool", Description: "true: every OSPF session Batfish finds is established, and there is one."},
	{Name: "bgp_established", Type: "bool", Description: "true: the same for BGP."},
	{Name: "route", Type: "dict", Description: "A route present: node, prefix, next_hops, protocol, vrf."},
	{Name: "no_route", Type: "dict", Description: "A route absent: node, prefix, vrf."},
	{Name: "reach", Type: "dict", Description: "A flow: from, dst, delivered, through, avoids, accepted_by."},
	{Name: "routes_match", Type: "dict", Description: "A node's modelled routes equal to the ones read live: node, routes, ignore, vrf."},
	{Name: "question", Type: "dict", Description: "Any Batfish question: name, variables, where, min_rows, max_rows."},
}

// batfishScenarioEntries are the keys of one scenarios entry.
var batfishScenarioEntries = []collection.Param{
	{Name: "name", Type: "string", Required: true, Description: "The scenario's name."},
	{Name: "down_interfaces", Type: "list of string", Description: "Interfaces taken down, each node[interface], e.g. r1[eth2]."},
	{Name: "down_nodes", Type: "list of string", Description: "Nodes taken down."},
	{Name: "assertions", Type: "list of dict", Required: true, Description: "What must hold with those down, as the assertions parameter.", Entries: batfishAssertionEntries},
}

// batfishAcceptEntries are the keys of one accept_unrecognized entry.
var batfishAcceptEntries = []collection.Param{
	{Name: "text", Type: "string", Description: "The line Batfish did not recognise, trimmed. Give text or feature."},
	{Name: "feature", Type: "string", Description: "A feature Batfish parses and does not model (glbp, ip_sla, sla_tracking, bfd, nhrp, management_vrf). Give text or feature."},
	{Name: "reason", Type: "string", Required: true, Description: "Why the model may ignore it."},
}
