// This file holds four small methods in namespaces that already exist:
// file.json and file.yaml (one value at a key path, the rest of the
// document kept), http.check (a URL's status and body, never the body in
// a result) and win.schtask (a Windows scheduled task over WinRM). The
// Doc literals are copies of each method's own docs, kept equal by
// internal/archtest's TestCatalogDataDocsMatchTheRegistry.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var smallCollections = []collectionscaffold.Config{
	{
		Name:          "file.json",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		Site:          collection.SiteTarget,
		Device:        collection.DeviceRequired,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Sets, or removes, the value at a key path in a JSON file on the device, keeping the rest of the file.",
			Description: "Reads the file (at most 4 MiB of UTF-8, refused when it holds a duplicate key, which two readers resolve differently) and sets the value at the key path, or removes it with state absent, keeping every other key in its place and every number and string exactly as written, so a 64-bit id or an escaped character survives. The file is written back at the indent unit it used (tabs, or a number of spaces, or on one line), one member per line, so a hand-aligned layout is normalized, and line endings are written as LF. A key already holding the value (by value, however it is spelled) is no change and nothing is written. The write keeps the file's owner, group and mode. The undo writes back the whole earlier text, withheld from the journal; a file the run created is removed. A check makes the same edit, reports the key's value before and after, and writes nothing.",
			Params: []collection.Param{
				{Name: "path", Type: "string", Required: true, Description: "The JSON file on the device, an absolute path."},
				{Name: "key", Type: "scalar or list", Required: true, Description: "The key path: dotted (server.port, items.0.name, a whole number indexing a list), or a list of segments ([server, \"a.b\", 0]) for a key holding a dot. A missing key is created, with a mapping for each missing step; an index one past a list's end appends."},
				{Name: "value", Type: "any", Description: "The value to set, any YAML value (a mapping, a list, a scalar, null). Required with state present."},
				{Name: "state", Type: "string", Default: "present", Choices: []string{"present", "absent"}, Description: "present sets the value; absent removes the key, which is no change when it is not there."},
				{Name: "create", Type: "bool", Default: "false", Description: "Start from an empty document when the file does not exist; without it a missing file is refused. A file it creates is mode 0644, owned by the account the device is reached as (file.attrs changes either)."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "path", Type: "string", Returned: "always", Description: "The file."},
				{Name: "key", Type: "string", Returned: "always", Description: "The key path, dotted."},
				{Name: "created", Type: "bool", Returned: "always", Description: "Whether the run created the file (or, in a check, would)."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The key's value before and after, as the format writes it; empty where there is none. Masked as every diff is."},
			},
			Examples: []collection.Example{
				{Name: "Move the service to another port", RunbookYAML: "- name: Port\n  file.json:\n    path: /etc/app/config.json\n    key: server.port\n    value: 8443\n"},
				{Name: "Drop a deprecated setting", RunbookYAML: "- name: Remove the old flag\n  file.json:\n    path: /etc/app/config.json\n    key: [features, \"legacy.mode\"]\n    state: absent\n"},
			},
			SeeAlso: []string{"file.yaml", "file.line", "file.copy"},
		},
	},
	{
		Name:          "file.yaml",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		Site:          collection.SiteTarget,
		Device:        collection.DeviceRequired,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Sets, or removes, the value at a key path in a YAML file on the device, keeping its comments and the rest of the file.",
			Description: "Reads the file (at most 4 MiB of UTF-8, one document; refused when it holds a duplicate key) and sets the value at the key path, or removes it with state absent, keeping comments and key order, and a replaced value's own comments. The rest of the document is written back in go.yaml.in/yaml/v3's style at the indent the file used, so quoting and spacing may be normalized; it is not a byte-for-byte edit. A path through an alias is refused, since one edit would change every place the alias is used. A key already holding the value is no change and nothing is written. The write keeps the file's owner, group and mode. The undo writes back the whole earlier text, withheld from the journal; a file the run created is removed. A check makes the same edit, reports the key's value before and after, and writes nothing.",
			Params: []collection.Param{
				{Name: "path", Type: "string", Required: true, Description: "The YAML file on the device, an absolute path."},
				{Name: "key", Type: "scalar or list", Required: true, Description: "The key path: dotted (server.port, items.0.name, a whole number indexing a list), or a list of segments ([server, \"a.b\", 0]) for a key holding a dot. A missing key is created, with a mapping for each missing step; an index one past a list's end appends."},
				{Name: "value", Type: "any", Description: "The value to set, any YAML value (a mapping, a list, a scalar, null). Required with state present."},
				{Name: "state", Type: "string", Default: "present", Choices: []string{"present", "absent"}, Description: "present sets the value; absent removes the key, which is no change when it is not there."},
				{Name: "create", Type: "bool", Default: "false", Description: "Start from an empty document when the file does not exist; without it a missing file is refused. A file it creates is mode 0644, owned by the account the device is reached as (file.attrs changes either)."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "path", Type: "string", Returned: "always", Description: "The file."},
				{Name: "key", Type: "string", Returned: "always", Description: "The key path, dotted."},
				{Name: "created", Type: "bool", Returned: "always", Description: "Whether the run created the file (or, in a check, would)."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The key's value before and after, as the format writes it; empty where there is none. Masked as every diff is."},
			},
			Examples: []collection.Example{
				{Name: "Raise the replica count", RunbookYAML: "- name: Replicas\n  file.yaml:\n    path: /srv/app/values.yaml\n    key: web.replicas\n    value: 3\n"},
			},
			SeeAlso: []string{"file.json", "file.line"},
		},
	},
	{
		Name:          "http.wait",
		Site:          collection.SiteController,
		Device:        collection.DeviceNone,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Waits, from the machine pleiades runs on, until a URL answers with the expected status, and optionally body.",
			Description: "Repeats http.check's request and its tests until they pass: the status is one of status_code, and the body contains body_contains or matches body_matches. A refused connection, a timeout or an answer that does not pass all mean not yet. It runs once with no target device, from the machine pleiades runs on, so it waits for a service published there (a container's port on 127.0.0.1) that wait.port, which looks from a device over SSH, cannot see. Each attempt is bounded by 10 seconds or by what is left of the timeout, and running out of time fails the task naming the last answer, or the last error. What it returns is http.check's, never the body: the last answer's status and final URL, how many attempts were made and how long the wait took. A URL carrying a user name or password is refused, a redirect from https to http is not followed, and certificates are verified unless validate_certs is false.",
			Params: []collection.Param{
				{Name: "url", Type: "string", Required: true, Description: "The http or https URL to request."},
				{Name: "status_code", Type: "int or list of int", Default: "200", Description: "The status, or statuses, that pass."},
				{Name: "body_contains", Type: "string", Description: "Text the body must contain."},
				{Name: "body_matches", Type: "string", Description: "A regular expression (RE2, at most 1024 bytes) the body must match. Give body_contains or body_matches, not both."},
				{Name: "method", Type: "string", Default: "GET", Choices: []string{"GET", "HEAD"}, ChoicesIgnoreCase: true, Description: "GET, or HEAD for the status alone."},
				{Name: "max_redirects", Type: "int", Default: "5", Description: "How many redirects to follow, 0 to 10."},
				{Name: "validate_certs", Type: "bool", Default: "true", Description: "Verify the server's certificate. False is set per task, never by a setting, and is visible in the runbook that sets it."},
				{Name: "timeout", Type: "int", Default: "300", Description: "How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0, and at most 86400."},
				{Name: "delay", Type: "int", Default: "0", Description: "How many seconds to wait before the first request. Must be shorter than the timeout, which it is spent out of."},
				{Name: "sleep", Type: "int", Default: "1", Description: "How many seconds to wait between requests. Must be more than 0."},
			},
			Returns: []collection.ReturnField{
				{Name: "status", Type: "int", Returned: "when the server answered", Description: "The last answer's status."},
				{Name: "final_url", Type: "string", Returned: "when the server answered", Description: "The URL that answered last, without query or fragment."},
				{Name: "attempts", Type: "int", Returned: "always", Description: "How many requests were made."},
				{Name: "elapsed", Type: "int", Returned: "always", Description: "How many whole seconds the task waited, including the delay."},
			},
			Examples: []collection.Example{
				{Name: "Wait for a service this machine runs to answer", RunbookYAML: "- name: Wait for Batfish's API\n  http.wait:\n    url: http://127.0.0.1:9996/v2/version\n    timeout: 180\n    sleep: 3\n"},
			},
			SeeAlso: []string{"http.check", "pleiades.builtin.wait.port"},
		},
	},
	{
		Name:          "http.check",
		Site:          collection.SiteController,
		Device:        collection.DeviceNone,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Checks that a URL answers with the expected status, and optionally body, without ever returning or logging the body.",
			Description: "Sends a GET (or a HEAD) to the URL, following at most max_redirects redirects and refusing one from https to http, since the check would then vouch for a page anyone on the path could have served. It fails when the status is not one of status_code, or when the body does not contain body_contains or match body_matches (RE2). The body is read, at most 4 MiB, only to match it: what is returned is the status, the final URL and each redirect (scheme, host and path, never a query or fragment, which can carry a token), the time taken, and the body's size and SHA-256, and a failure names the same. A URL carrying a user name or password is refused. Certificates are verified unless validate_certs is false, which is per task and loud. It only reads, so a check of it is the task itself, and it runs once with no target device.",
			Params: []collection.Param{
				{Name: "url", Type: "string", Required: true, Description: "The http or https URL to request."},
				{Name: "status_code", Type: "int or list of int", Default: "200", Description: "The status, or statuses, that pass."},
				{Name: "body_contains", Type: "string", Description: "Text the body must contain."},
				{Name: "body_matches", Type: "string", Description: "A regular expression (RE2, at most 1024 bytes) the body must match. Give body_contains or body_matches, not both."},
				{Name: "method", Type: "string", Default: "GET", Choices: []string{"GET", "HEAD"}, ChoicesIgnoreCase: true, Description: "GET, or HEAD for the status alone."},
				{Name: "max_redirects", Type: "int", Default: "5", Description: "How many redirects to follow, 0 to 10. A redirect past them is the answer checked, so max_redirects: 0 with status_code: 301 checks a redirect itself."},
				{Name: "timeout", Type: "int", Default: "30", Description: "Seconds the whole check may take, redirects included."},
				{Name: "validate_certs", Type: "bool", Default: "true", Description: "Verify the server's certificate. False is set per task, never by a setting, and is visible in the runbook that sets it."},
			},
			Returns: []collection.ReturnField{
				{Name: "status", Type: "int", Returned: "when the server answered", Description: "The final answer's status."},
				{Name: "final_url", Type: "string", Returned: "when the server answered", Description: "The URL that answered last, without query or fragment."},
				{Name: "redirects", Type: "list of string", Returned: "when the server answered", Description: "Each redirect followed, in order, without query or fragment."},
				{Name: "elapsed_ms", Type: "int", Returned: "when the server answered", Description: "How long the check took, in milliseconds."},
				{Name: "body_bytes", Type: "int", Returned: "when the server answered", Description: "How many bytes of body were read, at most 4 MiB."},
				{Name: "body_sha256", Type: "string", Returned: "when the server answered", Description: "The SHA-256 of the bytes read, in hex."},
				{Name: "body_over_limit", Type: "bool", Returned: "when the server answered", Description: "Whether the body was longer than 4 MiB."},
				{Name: "matched", Type: "bool", Returned: "with body_contains or body_matches", Description: "Whether the body matched."},
			},
			Examples: []collection.Example{
				{Name: "The release answers healthy", RunbookYAML: "- name: Health\n  http.check:\n    url: https://app.example.com/healthz\n    body_contains: \"\\\"status\\\":\\\"ok\\\"\"\n"},
			},
			SeeAlso: []string{"http.request", "wait.port"},
		},
	},
	{
		Name:          "win.schtask",
		Capabilities:  []capability.Name{capability.NameWinRM},
		Transports:    []string{"winrm"},
		Site:          collection.SiteTarget,
		Device:        collection.DeviceRequired,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Registers, updates or removes a Windows scheduled task over WinRM, or reports one as it is.",
			Description: "Reads the task first (Get-ScheduledTask, its XML definition and its last and next run), then: with state present, registers it with one action, one trigger and its principal when it is missing or differs in any of them, its description or whether it is enabled, and is no change when it matches; with absent, removes it; with query, only reports it. The script is a constant and every value reaches it as JSON on standard input, so a value can never be PowerShell syntax; every name is held to a strict pattern first. run_as SYSTEM, LOCAL SERVICE or NETWORK SERVICE runs as that service account; another account runs with S4U logon, so no password is stored with the task, and the task cannot reach the network as that user. A task this run registered undoes to its removal; one it changed or removed undoes to registering its earlier XML definition, withheld from the journal (xml, which also registers a task from a definition you give). A check reads and reports what would change.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The task's path: \\Folder\\Name, or Name in the root folder."},
				{Name: "state", Type: "string", Default: "present", Choices: []string{"present", "absent", "query"}, Description: "present registers or updates it; absent removes it; query only reports it."},
				{Name: "execute", Type: "string", Description: "The program the task runs. Required with state present, unless xml is given."},
				{Name: "arguments", Type: "string", Description: "Its arguments, as one string."},
				{Name: "working_directory", Type: "string", Description: "The directory it starts in."},
				{Name: "trigger", Type: "string", Choices: []string{"at_startup", "at_logon", "daily", "once"}, Description: "When it runs: at_startup, at_logon, daily (at, HH:MM, the device's local time) or once (at, an RFC 3339 time). Required with state present."},
				{Name: "at", Type: "string", Description: "The time of a daily or once trigger."},
				{Name: "run_as", Type: "string", Default: "SYSTEM", Description: "The account it runs as: SYSTEM, LOCAL SERVICE, NETWORK SERVICE, or an account (DOMAIN\\name or name) run with S4U logon and no stored password."},
				{Name: "run_level", Type: "string", Default: "limited", Choices: []string{"limited", "highest"}, Description: "Whether it runs with the account's highest privileges."},
				{Name: "enabled", Type: "bool", Default: "true", Description: "Whether the task is enabled."},
				{Name: "description", Type: "string", Description: "The task's description."},
				{Name: "xml", Type: "string", Description: "A whole task definition, as Export-ScheduledTask writes it, registered as given with state present; what this method's own undo uses."},
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
		},
	},
}
