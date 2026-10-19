// win.firewall.profile's reference documentation.
package firewall

import "github.com/Subject-Void-LLC/the-pleiades/pkg/collection"

func profileDoc() collection.Doc {
	return collection.Doc{
		Summary: "Sets, or reports, one Windows Defender Firewall profile over WinRM: whether it is on, its default actions and its logging.",
		Description: "Names one profile, domain, private or public, and compares the settings the task gives with the machine's own (PersistentStore), changing only those that differ; a task giving none only reports the profile. " +
			"A setting Windows holds as NotConfigured compares as what it does: on, block inbound, allow outbound, log nothing. " +
			"The setting in force is read too (ActiveStore): one Group Policy sets to another value fails the task, naming it, since the machine's own setting has no effect there, after making the change if one was needed. " +
			"The script is a constant and every value reaches it as JSON on standard input, held to its form first. " +
			"The undo puts back the earlier value of each setting changed, as the value that was in force for a NotConfigured one. A check reads and reports what would change.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Choices: []string{"domain", "private", "public"}, Description: "The profile. One per task, so its undo is exact."},
			{Name: pfEnabled, Type: "bool", Description: "Whether the firewall is on for this profile."},
			{Name: pfInbound, Type: "string", Choices: []string{"allow", "block"}, Description: "What it does with inbound traffic no rule matches. Windows' default is block."},
			{Name: pfOutbound, Type: "string", Choices: []string{"allow", "block"}, Description: "What it does with outbound traffic no rule matches. Windows' default is allow."},
			{Name: pfLogAllowed, Type: "bool", Description: "Whether allowed connections are logged."},
			{Name: pfLogBlocked, Type: "bool", Description: "Whether dropped packets are logged."},
			{Name: pfLogFile, Type: "string", Description: "The log file's path on the machine, as Windows takes it (%systemroot% may appear)."},
			{Name: pfLogMaxSizeK, Type: "int", Description: "The log's largest size in kilobytes, 1 to 32767."},
		},
		Returns: []collection.ReturnField{
			{Name: "name", Type: "string", Returned: "always", Description: "The profile."},
			{Name: "settings", Type: "dict", Returned: "always", Description: "Its settings before, as the machine keeps them, in this method's parameters' form."},
			{Name: "in_force", Type: "dict", Returned: "always", Description: "Its settings in force before, Group Policy's included."},
			{Name: "differences", Type: "list of string", Returned: "always", Description: "How the machine's own settings differed from what was asked, empty when they matched."},
			{Name: "overridden", Type: "list of string", Returned: "always", Description: "The settings asked for that Group Policy held at another value."},
		},
		Examples: []collection.Example{
			{Name: "Log what the domain profile drops", RunbookYAML: "- name: Log dropped packets on the domain network\n  win.firewall.profile:\n    name: domain\n    log_blocked: true\n    log_max_size_kb: 16384\n"},
			{Name: "Make sure the public profile is on and closed", RunbookYAML: "- name: The public profile is on and lets nothing in\n  win.firewall.profile:\n    name: public\n    enabled: true\n    default_inbound_action: block\n"},
		},
		SeeAlso: []string{"win.firewall.rule"},
	}
}
