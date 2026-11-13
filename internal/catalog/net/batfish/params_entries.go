// Package batfish: the entries of net.batfish.check's structured
// parameters, which validate holds each entry to before the run
// (collection.Param.Entries). internal/forge/catalogdata carries the same
// declarations, held equal by internal/archtest.
package batfish

import "github.com/Subject-Void-LLC/the-pleiades/pkg/collection"

// deviceEntries are the keys of one devices entry, as readDevice reads it.
var deviceEntries = []collection.Param{
	{Name: "name", Type: "string", Required: true, Description: "The node's name, which a Cisco configuration's hostname must match."},
	{Name: "format", Type: "string", Default: "frr", Choices: []string{"frr", "cisco_ios"}, Description: "The configuration's format."},
	{Name: "config", Type: "string", Description: "The whole configuration's text, e.g. a registered net.frr.facts result's running_config. Give config or src."},
	{Name: "src", Type: "string", Format: collection.ParamFormatProjectPath, Description: "A file in the project holding the whole configuration, read by the command line only. Give config or src."},
}

// assertionEntries are the kinds one assertions entry may name, as
// readAssertion reads them; an entry names exactly one, which the method
// checks.
var assertionEntries = []collection.Param{
	{Name: kindOSPF, Type: "bool", Description: "true: every OSPF session Batfish finds is established, and there is one."},
	{Name: kindBGP, Type: "bool", Description: "true: the same for BGP."},
	{Name: kindRoute, Type: "dict", Description: "A route present: node, prefix, next_hops, protocol, vrf."},
	{Name: kindNoRoute, Type: "dict", Description: "A route absent: node, prefix, vrf."},
	{Name: kindReach, Type: "dict", Description: "A flow: from, dst, delivered, through, avoids, accepted_by."},
	{Name: kindRoutesMatch, Type: "dict", Description: "A node's modelled routes equal to the ones read live: node, routes, ignore, vrf."},
	{Name: kindQuestion, Type: "dict", Description: "Any Batfish question: name, variables, where, min_rows, max_rows."},
}

// scenarioEntries are the keys of one scenarios entry, as readScenario
// reads it.
var scenarioEntries = []collection.Param{
	{Name: "name", Type: "string", Required: true, Description: "The scenario's name."},
	{Name: "down_interfaces", Type: "list of string", Description: "Interfaces taken down, each node[interface], e.g. r1[eth2]."},
	{Name: "down_nodes", Type: "list of string", Description: "Nodes taken down."},
	{Name: "assertions", Type: "list of dict", Required: true, Description: "What must hold with those down, as the assertions parameter.", Entries: assertionEntries},
}

// acceptEntries are the keys of one accept_unrecognized entry: a text or a
// feature, and a reason, as readParams reads it.
var acceptEntries = []collection.Param{
	{Name: "text", Type: "string", Description: "The line Batfish did not recognise, trimmed. Give text or feature."},
	{Name: "feature", Type: "string", Description: "A feature Batfish parses and does not model (glbp, ip_sla, sla_tracking, bfd, nhrp). Give text or feature."},
	{Name: "reason", Type: "string", Required: true, Description: "Why the model may ignore it."},
}
