// win.schtask's reference documentation.
package win

import "github.com/Subject-Void-LLC/the-pleiades/pkg/collection"

func schtaskDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Registers, updates or removes a Windows scheduled task over WinRM, or reports one as it is.",
		Description: "Reads the task first (Get-ScheduledTask, its XML definition and its last and next run), then: with state present, registers it with one action, one trigger and its principal when it is missing or differs in any of them, its description or whether it is enabled, and is no change when it matches; with absent, removes it; with query, only reports it. The script is a constant and every value reaches it as JSON on standard input, so a value can never be PowerShell syntax; every name is held to a strict pattern first. run_as SYSTEM, LOCAL SERVICE or NETWORK SERVICE runs as that service account; another account runs with S4U logon, so no password is stored with the task, and the task cannot reach the network as that user. A task this run registered undoes to its removal; one it changed or removed undoes to registering its earlier XML definition, withheld from the journal (xml, which also registers a task from a definition you give). A check reads and reports what would change.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The task's path: \\Folder\\Name, or Name in the root folder."},
			{Name: paramState, Type: "string", Default: "present", Choices: []string{"present", "absent", "query"}, Description: "present registers or updates it; absent removes it; query only reports it."},
			{Name: paramExecute, Type: "string", Description: "The program the task runs. Required with state present, unless xml is given."},
			{Name: paramArguments, Type: "string", Description: "Its arguments, as one string."},
			{Name: paramWorkingDir, Type: "string", Description: "The directory it starts in."},
			{Name: paramTrigger, Type: "string", Choices: triggers, Description: "When it runs: at_startup, at_logon, daily (at, HH:MM, the device's local time) or once (at, an RFC 3339 time). Required with state present."},
			{Name: paramAt, Type: "string", Description: "The time of a daily or once trigger."},
			{Name: paramRunAs, Type: "string", Default: "SYSTEM", Description: "The account it runs as: SYSTEM, LOCAL SERVICE, NETWORK SERVICE, or an account (DOMAIN\\name or name) run with S4U logon and no stored password."},
			{Name: paramRunLevel, Type: "string", Default: "limited", Choices: []string{"limited", "highest"}, Description: "Whether it runs with the account's highest privileges."},
			{Name: paramEnabled, Type: "bool", Default: "true", Description: "Whether the task is enabled."},
			{Name: paramDescription, Type: "string", Description: "The task's description."},
			{Name: paramXML, Type: "string", Description: "A whole task definition, as Export-ScheduledTask writes it, registered as given with state present; what this method's own undo uses."},
		},
		Returns: []collection.ReturnField{
			{Name: "name", Type: "string", Returned: "always", Description: "The task's full path."},
			{Name: "existed", Type: "bool", Returned: "always", Description: "Whether the task existed before."},
			{Name: "state", Type: "string", Returned: "always", Description: "Its state before (Ready, Running, Disabled), empty when it did not exist."},
			{Name: "last_run_time", Type: "string", Returned: "always", Description: "When it last ran."},
			{Name: "last_task_result", Type: "string", Returned: "always", Description: "Its last result code."},
			{Name: "next_run_time", Type: "string", Returned: "always", Description: "When it runs next."},
			{Name: "differences", Type: "list of string", Returned: "always", Description: "How it differed from what was asked, empty when it matched."},
		},
		Examples: []collection.Example{
			{Name: "A nightly cleanup", RunbookYAML: "- name: Cleanup\n  win.schtask:\n    name: \\Pleiades\\Cleanup\n    execute: C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe\n    arguments: -NoProfile -File C:\\ops\\cleanup.ps1\n    trigger: daily\n    at: \"02:30\"\n"},
			{Name: "Is the backup task healthy", RunbookYAML: "- name: Backup task\n  win.schtask:\n    name: \\Pleiades\\Backup\n    state: query\n  register: backup\n"},
		},
		SeeAlso: []string{"exec.winrm.shell", "svc.windows.start"},
	}
}
