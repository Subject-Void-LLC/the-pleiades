// This file holds win.firewall.rule, a Windows Defender Firewall rule over
// WinRM. The Doc literal is a copy of the method's own docs, kept equal by
// internal/archtest's TestCatalogDataDocsMatchTheRegistry.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var winFirewallCollections = []collectionscaffold.Config{
	{
		Name:          "win.firewall.rule",
		Capabilities:  []capability.Name{capability.NameWinRM},
		Transports:    []string{"winrm"},
		Site:          collection.SiteTarget,
		Device:        collection.DeviceRequired,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Makes, changes or removes a Windows Defender Firewall rule over WinRM, or reports one as it is.",
			Description: "A rule is named by its Name, its identity (OpenSSH-Server-In-TCP, not its display name), and read and written in the machine's own rule store (PersistentStore). " +
				"With state present, a rule that does not exist is made, with Windows' defaults for what the task leaves out (inbound, allow, any protocol, every profile, enabled, its name as its display name); " +
				"a rule that exists is compared on the settings the task gives and only those, and a setting that differs is changed, so a built-in rule's profiles can be widened without restating its ports or program. " +
				"absent removes it; query only reports it. A rule that exists only through Group Policy is refused, since a change here would not be kept. " +
				"Profiles, ports and addresses compare as sets, and an IPv4 network compares the same written as 10.0.0.0/8 or with the dotted mask Windows answers with. " +
				"Ports need protocol tcp or udp, the task's or the rule's. The script is a constant and every value reaches it as JSON on standard input, held to its form first. " +
				"A rule this run made undoes to its removal; a change, to the earlier value of each setting it changed; a removal, to making the rule again as it was, outside the group it belonged to. Lists may also be given as one comma-separated string, which is how an undo writes them; an undo restoring a display name or description is withheld from the journal. " +
				"A check reads and reports what would change.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The rule's Name, its identity: letters, digits, space and ._{}()@,+-, at most 255."},
				{Name: "state", Type: "string", Default: "present", Choices: []string{"present", "absent", "query"}, Description: "present makes or changes it; absent removes it; query only reports it."},
				{Name: "display_name", Type: "string", Description: "The name the firewall shows. A new rule takes its Name when this is left out."},
				{Name: "description", Type: "string", Description: "Its description."},
				{Name: "enabled", Type: "bool", Description: "Whether it is on. A new rule is."},
				{Name: "direction", Type: "string", Choices: []string{"inbound", "outbound"}, Description: "Which traffic it matches. A new rule's is inbound."},
				{Name: "action", Type: "string", Choices: []string{"allow", "block"}, Description: "What it does with that traffic. A new rule allows it."},
				{Name: "profiles", Type: "list", Description: "The network profiles it applies in: domain, private, public, or any (all three). A new rule's is any."},
				{Name: "protocol", Type: "string", Description: "tcp, udp, icmpv4, icmpv6, any, or an IP protocol number 0-255. A new rule's is any."},
				{Name: "local_port", Type: "list", Description: "The ports on this machine it matches: ports, ranges (8000-8100), any, or a keyword Windows knows (RPC, RPCEPMap, IPHTTPS, Teredo). Needs tcp or udp."},
				{Name: "remote_port", Type: "list", Description: "The other side's ports it matches: ports, ranges or any. Needs tcp or udp."},
				{Name: "local_address", Type: "list", Description: "This machine's addresses it matches: addresses, networks (10.0.0.0/8), ranges (10.0.0.1-10.0.0.9), any, or a keyword (LocalSubnet, Internet, Intranet, DNS, DHCP, WINS, DefaultGateway)."},
				{Name: "remote_address", Type: "list", Description: "The other side's addresses it matches, in the same forms."},
				{Name: "program", Type: "string", Description: "The program whose traffic it matches, a path, or any."},
				{Name: "service", Type: "string", Description: "The service whose traffic it matches, by its short name, or any."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The rule's Name."},
				{Name: "existed", Type: "bool", Returned: "always", Description: "Whether the rule existed before."},
				{Name: "group", Type: "string", Returned: "always", Description: "The group it belonged to before, as Windows names it, empty for none."},
				{Name: "rule", Type: "dict", Returned: "always", Description: "Its settings before, as this method's parameters name them, empty when it did not exist."},
				{Name: "differences", Type: "list of string", Returned: "always", Description: "How it differed from what was asked, empty when it matched."},
			},
			Examples: []collection.Example{
				{Name: "Let SSH in on a domain controller", RunbookYAML: "- name: OpenSSH's rule covers the domain network too\n  win.firewall.rule:\n    name: OpenSSH-Server-In-TCP\n    profiles: [domain, private]\n"},
				{Name: "An app's port from one network", RunbookYAML: "- name: The app's port, from the office only\n  win.firewall.rule:\n    name: Pleiades-App-In\n    display_name: App (TCP 8443)\n    protocol: tcp\n    local_port: [8443]\n    remote_address: [10.20.0.0/16]\n    profiles: [domain]\n"},
				{Name: "What a rule allows", RunbookYAML: "- name: Remote Desktop's rule\n  win.firewall.rule:\n    name: RemoteDesktop-UserMode-In-TCP\n    state: query\n  register: rdp\n"},
			},
			SeeAlso: []string{"win.schtask", "svc.windows.start", "fw.firewalld.allow"},
		},
	},
}
