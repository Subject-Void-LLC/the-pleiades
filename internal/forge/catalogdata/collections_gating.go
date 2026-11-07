// This file holds the Gating and facts section of docs/hephaestus.md's
// catalog: ansible.builtin.uri/wait_for/setup. http.request declares no
// capability at all, matching the catalog table's own "none" entry: an
// arbitrary HTTP call has no device-side prerequisite to check.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var gatingCollections = []collectionscaffold.Config{
	{
		Name:          "http.request",
		Site:          collection.SiteController,
		Device:        collection.DeviceOptional,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes an HTTP request and reports its status code and body.",
			Description: "Calls a URL from wherever the task runs, not from the target device, and records the status, body and response headers. A response whose status is not one of the expected ones fails the task, after recording what came back, since the body is usually the only thing that explains the failure. Certificates are verified unless a task says otherwise in its own text. Reporting changed follows the verb: GET, HEAD, OPTIONS and TRACE are read-only by HTTP's own definition and report no change, while any other verb reports a change, because what it did to the far side cannot be inspected from here. That is a deliberate difference from ansible.builtin.uri, which never reports changed at all. Four of that module's parameters are absent rather than accepted and ignored: body_format (the body is sent exactly as written, so set Content-Type in headers), return_content (the body is always recorded), follow_redirects (redirects are always followed), and the url_username and url_password pair (a credential belongs in the credential store, not in a runbook file). Only a request in a safe method (GET, HEAD, OPTIONS or TRACE) can be checked, and a check sends it for real, since reading is all it does. Any other method may change something on the server, so such a call is named as unchecked and sends nothing, and check_mode on one is refused when the runbook is validated.",
			Params: []collection.Param{
				{Name: "url", Type: "string", Required: true, Description: "The URL to call. It must be http or https: any other scheme is refused rather than attempted, since this method speaks one protocol and a file or ftp URL is a mistake in the runbook rather than a request this could make. A path beginning with one / instead calls the target device's own API: it is joined to the base URL of a generic_http device that onboarding proved (HTTPAPICapable), and only such a request carries a credential, the device's own from the credential store, sent as its http_auth property says. It cannot leave that origin: a scheme, a host, a second leading / and a .. segment are refused, a redirect elsewhere is not followed, and the task may set neither Authorization nor Host. It speaks the device's own TLS (its pinned authority, its client certificate, and any weakening its record allows, reported as a warning), so validate_certs false is refused for it.", Format: collection.ParamFormatURL},
				{Name: "method", Type: "string", Default: "GET", Description: "The HTTP method. It is upper-cased before being sent, because HTTP method names are case sensitive and a server given get will answer 501 rather than doing what the author meant."},
				{Name: "body", Type: "string", Description: "The request body, sent exactly as written. Set its Content-Type through headers: nothing here inspects the body or guesses a type for it."},
				{Name: "headers", Type: "dict", Description: "Request headers as a mapping of name to value. Every value must be text, so a numeric one is quoted in the runbook. A Host header is honored as the request's real Host rather than added as an ordinary header, which is what makes name-based routing testable against an address."},
				{Name: "status_code", Type: "int or list of int", Default: "200", Description: "The status code, or codes, that count as success. Anything else fails the task. Written as one number or a list of them, so an endpoint answering 200 or 201 depending on whether it created something can be accepted without a follow-up condition."},
				{Name: "timeout", Type: "float", Default: "30", Description: "How long to wait, in seconds, for the whole request including reading the body. Fractions are allowed, unlike Ansible's whole-second version, since a health check with a half-second budget is a real thing to want. Zero and negative values are refused: neither is a wait."},
				{Name: "validate_certs", Type: "bool", Default: "true", Description: "Verify the server's TLS certificate. Setting it false accepts any certificate, including one an attacker in the middle presents, so it belongs only on a target you have decided does not need it. There is no setting anywhere that changes the default: turning verification off is written in the task it applies to."},
			},
			Returns: []collection.ReturnField{
				{Name: "status", Type: "int", Returned: "always", Description: "The status code the server answered with, recorded even when it is not one of the expected ones."},
				{Name: "content", Type: "string", Returned: "always", Description: "The whole response body as text. Held in memory, so this method is for calling an API rather than for fetching a large file: a body over 16 MiB fails the task."},
				{Name: "headers", Type: "dict", Returned: "always", Description: "The response headers, names lower-cased, with a header sent more than once joined by a comma and a space."},
				{Name: "url", Type: "string", Returned: "always", Description: "The URL the response actually came from, which differs from the one asked for when redirects were followed."},
				{Name: "elapsed", Type: "float", Returned: "always", Description: "How long the request took, in seconds, with its fraction kept. Ansible reports whole seconds here, which is zero for every call that went well."},
				{Name: "msg", Type: "string", Returned: "always", Description: "The status line, for example \"404 Not Found\", for a person reading a run log."},
				{Name: "json", Type: "any", Returned: "when the response is JSON", Description: "A JSON response body, decoded, so a later task can read one field of it (Ansible uri's own key). Recorded only when the response says its content is JSON (application/json, or a type ending in +json), the body parses as JSON and it is at most 1 MiB; otherwise the key is left out and content still holds the text."},
			},
			Examples: []collection.Example{
				{
					Name:        "Check that a service answers",
					RunbookYAML: "- name: Wait for the health endpoint\n  http.request:\n    url: https://api.example.com/healthz\n    timeout: 5\n",
				},
				{
					Name:        "Post JSON to an API",
					RunbookYAML: "- name: Register the release\n  http.request:\n    url: https://api.example.com/releases\n    method: POST\n    body: '{\"version\": \"1.4.0\"}'\n    headers:\n      Content-Type: application/json\n    status_code:\n      - 200\n      - 201\n",
				},
				{
					Name:        "Call a device's own API with its stored credential",
					RunbookYAML: "- name: Read the device's interfaces\n  http.request:\n    url: /interfaces\n",
				},
			},
			SeeAlso: []string{"exec.command", "pleiades.builtin.wait.port"},
		},
	},
	{
		// Namespaced under pleiades.builtin, not the bare "wait.port" its
		// wait.path/wait.search siblings still use: this is the first
		// (and, as of this entry, only) native-only method in the catalog
		// deliberately marked as belonging to The Pleiades' own reserved
		// namespace rather than mapping 1:1 from an Ansible module name.
		// See docs/hephaestus.md's "four of the thirty six are not
		// collections at all" section for set_metadata's sibling case
		// (internal/engine/action.go), and note this rename intentionally
		// leaves wait.path/wait.search un-renamed rather than expanding
		// scope to "fix" the now-visible inconsistency among the three.
		Name:          "pleiades.builtin.wait.port",
		Capabilities:  []capability.Name{capability.NameNetworkAddressable},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Waits for a TCP port on the target to start (or stop) accepting connections.",
			Description: "Holds the runbook until a TCP port accepts a connection, or until it stops accepting one. The check runs ON THE DEVICE, over the SSH connection the task already holds, which is where Ansible's wait_for runs it and is the only place it answers the useful question: a service bound to 127.0.0.1 is invisible from the runner, and a firewall between the runner and the device would report on the path rather than on the service. Running there costs a prerequisite, stated rather than assumed: the device needs python3 or bash, checked once before polling starts and refused by name when neither is present. python3 is preferred because its exit status is this method's own to define; bash is the fallback because it needs no package installed, using its /dev/tcp redirection, and a bash built without that feature is detected and reported rather than mistaken for a closed port. nc is deliberately not used: its flags and exit statuses differ between the openbsd, traditional and busybox builds, so a wrong answer from it could not be told apart from a real one. Never reports changed: waiting observes, it does not act.",
			Params: []collection.Param{
				{Name: "port", Type: "int", Required: true, Description: "The TCP port to wait on, 1 to 65535. Write it unquoted: a quoted \"8080\" is text rather than a number and is refused by name."},
				{Name: "host", Type: "string", Default: "127.0.0.1", Description: "The address to connect to, resolved and dialed ON THE DEVICE. The default is loopback, which means the device itself, and is what makes this method see a service bound only to 127.0.0.1."},
				{Name: "timeout", Type: "int", Default: "300", Description: "How many seconds to wait in total before giving up, at most 86400. Measured from the start of the task, so delay, the SSH connection and the tool check all come out of this budget, exactly as Ansible measures it. Running out is an error, not a quiet pass."},
				{Name: "delay", Type: "int", Default: "0", Description: "How many seconds to wait before the first check, at most 86400. Useful when a service is known to accept connections briefly before it is really ready."},
				{Name: "sleep", Type: "int", Default: "1", Description: "How many seconds to wait between checks, at least 1 and at most 86400. The sleep happens on the runner, not on the device, so it costs the device nothing. Zero is refused because it would open SSH sessions in a tight loop."},
				{Name: "state", Type: "string", Default: "started", Choices: []string{"started", "stopped"}, Description: "Wait for the port to start accepting connections, or to stop. Ansible's present and absent describe a path and its drained describes connection counts, so all three are refused here rather than silently read as started."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "elapsed", Type: "int", Returned: "always", Description: "Whole seconds spent waiting, including the delay. Recorded on a timeout too, so a failed task still says how long it held."},
				{Name: "port", Type: "int", Returned: "always", Description: "The port that was waited on."},
				{Name: "host", Type: "string", Returned: "always", Description: "The address that was dialed from the device, filled in with the default when the task did not name one."},
				{Name: "state", Type: "string", Returned: "always", Description: "The state that was waited for, either started or stopped."},
			},
			Examples: []collection.Example{
				{
					Name:        "Wait for a database to come back after a restart",
					RunbookYAML: "- name: Wait for postgres to accept connections\n  pleiades.builtin.wait.port:\n    port: 5432\n    timeout: 120\n",
				},
				{
					Name:        "Wait for a port to be released before rebinding it",
					RunbookYAML: "- name: Wait for the old listener to go away\n  pleiades.builtin.wait.port:\n    port: 8080\n    state: stopped\n    timeout: 60\n",
				},
				{
					Name:        "Give a service a head start, then poll slowly",
					RunbookYAML: "- name: Wait for the API on its private address\n  pleiades.builtin.wait.port:\n    host: 10.0.0.7\n    port: 443\n    delay: 10\n    sleep: 5\n",
				},
			},
			SeeAlso: []string{"wait.path", "wait.search", "exec.command"},
		},
	},
	{
		Name:          "wait.path",
		Capabilities:  []capability.Name{capability.NameNetworkAddressable},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Waits for a file path on the target to exist (or stop existing).",
			Description: "Polls a path on the device until it exists, or until it is gone when state is absent, and fails when the timeout runs out first. It changes nothing: state: absent waits for something else to remove the path, it does not remove it. Anything at the path counts as existing, including a directory and a symbolic link whose target is missing, since the link itself is a thing that is there; that differs from wait_for, which follows a link and calls a broken one absent. The delay is spent out of the timeout rather than added to it, matching wait_for, so delay must be shorter than timeout. A run that gives up returns an error, because a task that waited for something that never happened has failed.",
			Params: []collection.Param{
				{Name: "path", Type: "string", Required: true, Description: "The path on the device to watch."},
				{Name: "state", Type: "string", Default: "present", Choices: []string{"present", "absent", "started", "stopped"}, Description: "Whether to wait for the path to exist (present) or to stop existing (absent). started and stopped are accepted as wait_for's own spellings of the same two, so a converted playbook needs no edit. drained is refused, since it asks about a socket's send queue and means nothing for a file."},
				{Name: "timeout", Type: "int", Default: "300", Description: "How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0."},
				{Name: "delay", Type: "int", Default: "0", Description: "How many seconds to wait before looking the first time. Must be shorter than the timeout, which it is spent out of."},
				{Name: "sleep", Type: "int", Default: "1", Description: "How many seconds to wait between looks. Must be more than 0: a sleep of zero would probe the device as fast as it can answer."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "path", Type: "string", Returned: "always", Description: "The path this task watched."},
				{Name: "elapsed", Type: "int", Returned: "always", Description: "How many whole seconds the task waited, including the delay."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The state of the path when the wait ended, holding its path, kind, mode, owner, group, size and modification time. Both halves are identical, since a wait changes nothing."},
			},
			Examples: []collection.Example{
				{
					Name:        "Wait for a service to write its socket",
					RunbookYAML: "- name: Wait for the socket to appear\n  wait.path:\n    path: /run/pleiades/api.sock\n    timeout: 60\n",
				},
				{
					Name:        "Wait for a lock file to be released",
					RunbookYAML: "- name: Wait for the package manager to finish\n  wait.path:\n    path: /var/lib/dpkg/lock-frontend\n    state: absent\n    timeout: 300\n    sleep: 5\n",
				},
				{
					Name:        "Give a slow starter a head start",
					RunbookYAML: "- name: Wait for the pid file, but not immediately\n  wait.path:\n    path: /run/app.pid\n    delay: 10\n    timeout: 120\n",
				},
			},
			SeeAlso: []string{"wait.search", "pleiades.builtin.wait.port"},
		},
	},
	{
		Name:          "wait.search",
		Capabilities:  []capability.Name{capability.NameNetworkAddressable},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Waits for a pattern to appear in a file's contents on the target.",
			Description: "Reads a file on the device on an interval until search_regex matches it, or until it stops matching when state is absent, and fails when the timeout runs out first. It changes nothing. The whole file is read on every look, so this suits a service's startup log rather than a file that grows without limit, and a file past 64 MiB fails the task rather than being matched in part. The pattern is matched in multiline mode, as wait_for matches it, so ^ and $ mean the start and end of a line; it is a Go RE2 pattern, so a backreference or a lookahead is refused when the task is read rather than silently never matching. Only a regular file is read: a path that is missing, or that is a directory, counts as not matching, which satisfies state: absent at once and never satisfies state: present.",
			Params: []collection.Param{
				{Name: "path", Type: "string", Required: true, Description: "The file on the device to read."},
				{Name: "search_regex", Type: "string", Required: true, Description: "The pattern to look for, matched in multiline mode against the whole file. Use wait.path when only the file's existence matters."},
				{Name: "state", Type: "string", Default: "present", Choices: []string{"present", "absent", "started", "stopped"}, Description: "Whether to wait for the pattern to match (present) or to stop matching (absent). started and stopped are accepted as wait_for's own spellings of the same two. drained is refused, since it asks about a socket's send queue and means nothing for a file."},
				{Name: "timeout", Type: "int", Default: "300", Description: "How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0."},
				{Name: "delay", Type: "int", Default: "0", Description: "How many seconds to wait before reading the first time. Useful when the file still holds the previous run's matching line. Must be shorter than the timeout, which it is spent out of."},
				{Name: "sleep", Type: "int", Default: "1", Description: "How many seconds to wait between reads. Must be more than 0: a sleep of zero would read the file as fast as the device can send it."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "path", Type: "string", Returned: "always", Description: "The file this task read."},
				{Name: "search_regex", Type: "string", Returned: "always", Description: "The pattern this task looked for, exactly as the task wrote it."},
				{Name: "elapsed", Type: "int", Returned: "always", Description: "How many whole seconds the task waited, including the delay."},
				{Name: "match_groups", Type: "list of string", Returned: "when the pattern matched", Description: "The capture groups of the match, in order, without the whole match itself."},
				{Name: "match_groupdict", Type: "dict", Returned: "when the pattern matched", Description: "The named capture groups of the match, keyed by name. Empty when the pattern names none."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the last read found: the path, whether it was a readable file, the pattern, and whether it matched. Both halves are identical, since a wait changes nothing."},
			},
			Examples: []collection.Example{
				{
					Name:        "Wait for a service to log that it started",
					RunbookYAML: "- name: Wait for the API to finish starting\n  wait.search:\n    path: /var/log/pleiades/api.log\n    search_regex: \"^Listening on \"\n    timeout: 120\n",
				},
				{
					Name:        "Capture the port a service chose",
					RunbookYAML: "- name: Read the port out of the log\n  wait.search:\n    path: /var/log/app.log\n    search_regex: \"listening on port (?P<port>[0-9]+)\"\n  register: startup\n",
				},
				{
					Name:        "Wait for an error line to be rotated away",
					RunbookYAML: "- name: Wait for the log to stop showing the failure\n  wait.search:\n    path: /var/log/app.log\n    search_regex: \"FATAL\"\n    state: absent\n    timeout: 60\n    sleep: 5\n",
				},
			},
			SeeAlso: []string{"wait.path", "pleiades.builtin.wait.port"},
		},
	},
	{
		Name:          "wait.connection",
		Capabilities:  []capability.Name{capability.NameNetworkAddressable},
		Transports:    []string{"ssh", "winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Waits until the target answers a command over its own connection, SSH or WinRM.",
			Description: "Tries the device over the connection its other tasks use (WinRM for a device reached that way, such as a Windows server, and SSH for any other) until a command runs there, and fails when the timeout runs out first. It is Ansible's wait_for_connection: the task to put after one that starts or restarts a machine, before the tasks that need it. Each try logs in afresh with the device's credential and runs a command that does nothing, so a machine whose port answers before its account can log in is not taken for ready, and a session kept open from an earlier task is never taken for a login. A network device's CLI is not a shell with a command that does nothing, so for one that declares CiscoIOSCapable, or a cli_prompt, a try opens its terminal and waits for its prompt instead. A failed try is not an error, since it is what the task waits to see stop; the last one is quoted when the timeout ends the wait. The delay is spent out of the timeout rather than added to it, as wait_for_connection does. With backoff: exponential the gap after each failed try doubles from sleep up to max_sleep, each gap a random point in its upper half so machines restarted together do not try in step. With steady above 1 the machine must answer that many tries in a row, steady_interval apart, which a machine just resumed or restarted that answers once and then refuses for a while does not; a refusal mid-streak starts the streak, and the backoff, again. It changes nothing.",
			Params: []collection.Param{
				{Name: "timeout", Type: "int", Default: "600", Description: "How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0."},
				{Name: "delay", Type: "int", Default: "0", Description: "How many seconds to wait before the first try. Must be shorter than the timeout, which it is spent out of."},
				{Name: "sleep", Type: "int", Default: "5", Description: "How many seconds to wait between tries, or with backoff: exponential before the second try, the gap doubling after each failed one. Must be more than 0."},
				{Name: "backoff", Type: "string", Default: "fixed", Choices: []string{"fixed", "exponential"}, Description: "How the gap between failed tries grows: fixed keeps it at sleep, as wait_for_connection does; exponential doubles it after each failed try, up to max_sleep, each gap a random point in its upper half."},
				{Name: "max_sleep", Type: "int", Default: "60", Description: "With backoff: exponential, the longest gap between tries, in seconds. Not less than sleep; refused with backoff: fixed, where it would do nothing."},
				{Name: "steady", Type: "int", Default: "1", Description: "How many tries in a row must answer before the machine counts as ready. Each is its own login. Must be at least 1, and the streak must fit in the timeout after the delay."},
				{Name: "steady_interval", Type: "int", Default: "10", Description: "With steady above 1, the seconds between the answering tries. Must be more than 0; refused with steady 1, where it would do nothing."},
				{Name: "connect_timeout", Type: "int", Default: "20", Description: "How many seconds one try may take before it counts as failed. Must be more than 0."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task, on a device reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "elapsed", Type: "int", Returned: "always", Description: "How many whole seconds the task waited, including the delay."},
				{Name: "transport", Type: "string", Returned: "always", Description: "The connection that answered: ssh or winrm."},
				{Name: "tries", Type: "int", Returned: "always", Description: "How many tries the task made, answered or not."},
				{Name: "failures", Type: "int", Returned: "always", Description: "How many of those tries did not answer."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The connection that answered. Both halves are identical, since a wait changes nothing."},
			},
			Examples: []collection.Example{
				{
					Name:        "Wait for a new Windows VM's first boot",
					RunbookYAML: "- name: Wait for the Windows lab VM to let its Administrator in\n  wait.connection:\n    timeout: 1800\n    sleep: 15\n",
				},
				{
					Name:        "Wait out a reboot",
					RunbookYAML: "- name: Give the machine time to go down, then wait for it\n  wait.connection:\n    delay: 30\n    timeout: 600\n",
				},
				{
					Name:        "Wait for a resumed VM that keeps answering",
					RunbookYAML: "- name: Wait until the resumed VM lets its account in three times running\n  wait.connection:\n    timeout: 900\n    backoff: exponential\n    sleep: 2\n    max_sleep: 60\n    steady: 3\n    steady_interval: 10\n",
				},
			},
			SeeAlso: []string{"wait.path", "virt.vbox.vm.start", "pleiades.builtin.wait.port"},
		},
	},
	{
		Name:          "facts.gather",
		Capabilities:  []capability.Name{capability.NameFactGatherer},
		Transports:    []string{"ssh", "winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Gathers system facts from the target: OS, kernel, distribution, and on request its packages, services, listening ports, accounts and named files.",
			Description: "Reads every system fact it knows by default, over SSH or, from a Windows host, over WinRM (identity, kernel and system, distribution and its release, memory and swap, processors, uptime) and emits each one as a fact, so it is kept as drift data, which is also a stat a register, until or when reads as result.<register>.<name>. filter narrows it. A fact the device cannot answer for is left out entirely: there is no guessed default and no empty string, so a condition reading ansible_distribution can tell \"this device does not say\" from \"this device says nothing\". Nothing is changed and no command is elevated, so this always reports no change. gather_subset chooses what is read: min (identity, kernel, distribution) and hardware (memory, processors, uptime), the default; packages, one fact per installed package (package.<name>: version, arch and source, from dpkg, rpm, apk, FreeBSD's pkg, Homebrew, pkgutil's receipts, or a Windows host's Uninstall keys); services, two facts per service (service.<name>: enabled and status, and service_state.<name>: its state, which moves on its own and so is kept as its latest value only, never as drift), from systemd, OpenRC, FreeBSD's rc, launchd, or the Windows service control manager, leaving out a systemd unit that lasts only as long as a login, a connection or a process: a transient unit, a user@ or user-runtime-dir@ instance, or one connection's instance of an Accept=yes socket (a lingering user's user@<uid>.service is left out too, though it lasts as long as the host and not a login: that is a choice, so a user's own service manager is never listed as a service; and systemd before version 230 does not report a transient unit as transient, so there such a unit is still listed); ports, one fact per listening socket (port.<protocol>:<address>:<port>, from the kernel's socket tables on Linux, sockstat on FreeBSD, netstat on macOS, and the TCP/IP cmdlets on Windows); users, one fact per local account and group (user.<name>, group.<name>, from /etc/passwd and /etc/group read without their password field, a Mac's local directory, or a Windows host's local accounts), never a password or its hash; and files, one fact per path named in files (file.<path>: metadata and a SHA-256 digest, plain for a file anyone may read and keyed for any other, never the content), where a path the login cannot reach is unreadable, never absent. The sources each return names are Linux's: FreeBSD and macOS answer the same facts from sysctl (and macOS its distribution from sw_vers), and Windows from CIM, where ansible_hostname is the computer name, ansible_system is Win32NT, the kernel and the distribution version are the OS version, and the distribution is the edition's caption, as Ansible reports them. A family is read or the task says it was not: a source on the device whose listing fails, or answers without the structure it always prints, fails the task, so no item reads as removed; a family no source on the device answers fails a task that named its subset, and is a warning for one that reached it through all. Two of ansible.builtin.setup's parameters are absent rather than accepted and ignored: gather_timeout is the task's own timeout here, and fact_path's local fact files are a separate feature this does not implement.",
			Params: []collection.Param{
				{Name: "filter", Type: "list of string", Description: "Shell-style patterns, as ansible.builtin.setup takes them, narrowing which facts are gathered. A fact is gathered when its name matches any pattern, so [\"ansible_distribution*\"] gathers the distribution and its version and nothing else. Leaving this out gathers everything the chosen subsets gather, and so does an empty list. A pattern set matching none of the facts this method knows about is refused rather than gathering nothing, since that is a typo far more often than an intention. Narrowing also skips the commands behind the facts it excludes, so it is a real saving rather than a filter on the way out. A service's state is service_state.<name>, which a pattern for service.<name> alone does not match."},
				{Name: "gather_subset", Type: "list of string", Description: "The subsets to gather: min and hardware as ansible.builtin.setup names them, packages and services (Ansible's package_facts and service_facts), ports, users and files, or all, every one; a name with a leading ! leaves that subset out wherever it stands in the list, unless the task also names that subset itself, as Ansible reads it (files with paths in files and !files together are still refused), and a list of only such names starts from min and hardware. min is gathered unless !min is named. Leaving this out gathers min and hardware, what this method always read, so a task never starts listing a host's packages without asking. files is gathered whenever files names paths, all gathers it only then, and naming files with no paths is refused. An unknown name is refused."},
				{Name: "files", Type: "list of string", Description: "Absolute paths to describe, at most 64: for each, whether it is present, absent or unreadable and, when present, its type, size, owner and group (uid and gid on a POSIX device), mode (attributes on Windows), the SHA-256 and size of a regular file anyone may read, and a link's target. Content is never read back. A file not world-readable (on a POSIX device, one its mode or a directory above it keeps from others; on Windows, one whose ACL does not let Everyone, Authenticated Users or Users read it) keeps no plain digest and no size, since a stored digest of a small secret can be guessed offline by anyone who may read the facts: its digest is kept only as hmac_sha256, HMAC-SHA-256 under a key the project (from its master key) or the Controller (PLEIADES_FACTS_DIGEST_KEY) holds, with hmac_key naming the key, so an unchanged file keeps its value and a change still shows. Where no such key is held the file is described without a digest. A path is absent only when the device says it does not exist; one inside a directory the login may not search, or that Windows will not open, is unreadable. A link is described as itself, not what it points to. A network path (a UNC path such as \\\\server\\share) is refused."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "ansible_hostname", Type: "string", Returned: "when the device reports one", Description: "The short host name, which is everything before the first dot of what uname reports."},
				{Name: "ansible_kernel", Type: "string", Returned: "when the device reports one", Description: "The kernel release, as uname -r reports it."},
				{Name: "ansible_architecture", Type: "string", Returned: "when the device reports one", Description: "The machine hardware name, as uname -m reports it, for example x86_64."},
				{Name: "ansible_distribution", Type: "string", Returned: "when the device names one", Description: "The distribution's name as Ansible reports it: from ID in /etc/os-release for each distribution Ansible names its own way (Debian, Ubuntu, Kali, Devuan, RedHat, CentOS, Rocky, AlmaLinux, OracleLinux, Fedora, Amazon, Alpine, Archlinux), NAME on the SUSE family as Ansible's SUSE parser reports it (openSUSE Leap, SLES), and NAME for any other, which is Ansible's own fallback but not always its answer, since Ansible names some derivatives by a word in the file; MacOSX on macOS and the edition's caption on Windows."},
				{Name: "ansible_distribution_version", Type: "string", Returned: "when the device names one", Description: "The distribution version as Ansible reports it: VERSION_ID in /etc/os-release, on Debian the point release /etc/debian_version holds when it is more precise (12.12 where VERSION_ID is 12), and on the SUSE family VERSION_ID's major and minor parts. A rolling release that publishes no VERSION_ID reports no such fact."},
				{Name: "ansible_memtotal_mb", Type: "int", Returned: "when /proc/meminfo reports it", Description: "Total usable memory in megabytes, from MemTotal in /proc/meminfo, truncated the way Ansible truncates it; on Windows rounded up, as Ansible's Windows setup rounds it."},
				{Name: "ansible_processor_count", Type: "int", Returned: "when /proc/cpuinfo reports it", Description: "The number of physical processor packages, counted as the distinct physical id values in /proc/cpuinfo. An architecture whose /proc/cpuinfo carries no physical id field reports no such fact rather than a guessed 1."},
				{Name: "ansible_uptime_seconds", Type: "int", Returned: "when /proc/uptime reports it", Description: "How long the device has been up, in whole seconds, from the first field of /proc/uptime."},
				{Name: "ansible_nodename", Type: "string", Returned: "when the device reports one", Description: "The full node name uname -n reports, before ansible_hostname cuts it at the first dot."},
				{Name: "ansible_system", Type: "string", Returned: "when the device reports one", Description: "The kernel's name, as uname -s reports it: Linux, FreeBSD, Darwin."},
				{Name: "ansible_distribution_major_version", Type: "string", Returned: "when the device names a version", Description: "The distribution version up to its first dot, such as 24 for 24.04."},
				{Name: "ansible_os_family", Type: "string", Returned: "when the device names its distribution", Description: "The family the distribution belongs to, from Ansible's own table: RedHat, Debian, Suse, Archlinux, Alpine, FreeBSD, Darwin (macOS) or Windows, or for a distribution the table does not list but this method names as Ansible does, the distribution itself, as Ansible reports it. A distribution reported by its NAME alone has a family only where Ansible's table lists that NAME, since Ansible may name it otherwise: a family is never guessed."},
				{Name: "ansible_distribution_release", Type: "string", Returned: "when the distribution names one", Description: "The release as Ansible reports it: the codename (VERSION_CODENAME in /etc/os-release, then UBUNTU_CODENAME, then the name in parentheses in VERSION, such as Blue Onyx), Stream on CentOS Stream, and the minor version on the SUSE family (6 for 15.6, 0 on an enterprise release with none). A release Ansible reports as NA or as empty, as it does on Fedora, Alpine and Amazon Linux, is left out. FreeBSD and macOS report none yet, where Ansible reports the kernel release."},
				{Name: "ansible_swaptotal_mb", Type: "int", Returned: "when the device reports it, never on macOS", Description: "Total swap in megabytes, from SwapTotal in /proc/meminfo (vm.swap_total on FreeBSD); 0 on a device with no swap, and always 0 on Windows, as Ansible reports it there, since a page file is not swap. macOS reports none, as Ansible does: its swap files are made and removed as memory is needed, so their total moves on its own."},
				{Name: "ansible_processor_vcpus", Type: "int", Returned: "when /proc/cpuinfo lists processors", Description: "The number of logical processors, counted as the processor entries in /proc/cpuinfo."},
				{Name: "ansible_processor_cores", Type: "int", Returned: "when /proc/cpuinfo reports cpu cores", Description: "The cores per processor package, from the first cpu cores field in /proc/cpuinfo."},
				{Name: "package.<name>", Type: "map", Returned: "with the packages subset, for each installed package", Description: "One installed package: version, arch and source (dpkg, rpm, apk, pkg, brew, brew-cask, pkgutil or registry). A name a key cannot hold is percent-encoded; a name two packages share takes :arch, then @source, then #2 and on in the order the source lists them. A package its source gives no version has none."},
				{Name: "service.<name>", Type: "map", Returned: "with the services subset, for each service the service manager lists", Description: "One service by its manager's name for it: enabled, status and source; what it is doing now is service_state.<name>. From systemd (source systemd), status is the unit file's state (enabled, disabled, static, masked, generated, indirect and so on), enabled meaning enabled or enabled-runtime, and a unit that lasts only as long as a login, a connection or a process (a transient unit, a user@ or user-runtime-dir@ instance, one connection's instance of an Accept=yes socket) not listed, a lingering user's user@<uid>.service left out as well by choice though it lasts as long as the host, and a transient unit still listed on systemd before 230, which does not report it as transient; from OpenRC (openrc), status is the runlevels the script is in, or disabled, enabled meaning a runlevel other than shutdown; from FreeBSD's rc, status is enabled or disabled; from launchd, status is enabled or disabled, a daemon launchd has not loaded because it is disabled being listed all the same, and a job launchd starts once per connection or session (a label ending in a UUID, such as com.openssh.sshd.<UUID>) not listed, since a new one comes with every login; from the Windows service control manager (scm), status is the start mode (auto, delayed, manual, disabled, boot, system), enabled meaning it starts with Windows, and a per-user service is listed once by its template's name with per_user true, never by the instance Windows makes for each signed-in user (Template_<LUID>), whose name changes with every sign-in."},
				{Name: "service_state.<name>", Type: "string", Returned: "with the services subset, for each service its manager says the state of", Description: "What the service service.<name> names is doing, kept as its latest value only and never as drift, since services start and stop on their own (a timer's job, a service started when something asks for it). From systemd, the unit's sub-state (running, exited, dead, failed, inactive and the others systemd reports, such as activating or auto-restart); from OpenRC, running, stopped or OpenRC's own word (crashed, inactive), or unknown before OpenRC has booted; from FreeBSD's rc, running, stopped or unknown (an rc script that is not enabled, or whose status only root may read); from launchd, running or stopped; from Windows, the manager's own word lower-cased (running, stopped, start pending). A per-user service's template has none."},
				{Name: "file.<path>", Type: "map", Returned: "with the files subset, for each path in files", Description: "One named path: state (present, absent or unreadable); when present, type (file, directory, link, fifo, socket, char or block), size (not for a directory, nor a file not everyone may read), uid, gid, owner, group and mode as four octal digits on a POSIX device, attributes and owner on Windows, for a regular file world_readable and, when anyone may read it, sha256 and size, or when not, hmac_sha256 and hmac_key (a keyed digest the store can compare and nobody without the key can test guesses against; on a Runner the task's own result holds it sealed to the Controller, as sha256_sealed, and only the stored fact holds it keyed), readable, and link_target for a link. The path is percent-encoded in the key as any item name is."},
				{Name: "port.<protocol>:<address>:<port>", Type: "map", Returned: "with the ports subset, for each listening socket", Description: "One listening TCP socket, or UDP socket bound with no peer: protocol (tcp or udp), address (0.0.0.0 or :: for any; in brackets in the key when it is IPv6), port, uid on Linux, and process, the owning process's name, where the login may see it (on Linux its own processes, or every one as root; on FreeBSD every one unless security.bsd.see_other_uids is off; every one on macOS; on Windows every one an administrator sees). A socket taking both families is ::."},
				{Name: "user.<name>", Type: "map", Returned: "with the users subset, for each local account", Description: "One local account: uid, gid, home, shell and source (files from /etc/passwd, dslocal from a Mac's local directory), or on Windows sid, enabled and source sam. Accounts a directory service adds (LDAP, SSSD, a domain) are not local and not listed; the password field is never read."},
				{Name: "group.<name>", Type: "map", Returned: "with the users subset, for each local group", Description: "One local group: gid (sid on Windows), members as the group lists them (an account whose primary group it is is not repeated), and source. A Windows group whose members could not be listed has no members key."},
			},
			Examples: []collection.Example{
				{
					Name:        "Gather everything before deciding what to do",
					RunbookYAML: "- name: Learn what this device is\n  facts.gather:\n",
				},
				{
					Name:        "Gather the installed packages and services",
					RunbookYAML: "- name: Learn what is installed and running\n  facts.gather:\n    gather_subset:\n      - packages\n      - services\n",
				},
				{
					Name:        "Record a configuration file's digest and who listens where",
					RunbookYAML: "- name: Describe sshd's configuration and the listening sockets\n  facts.gather:\n    gather_subset:\n      - ports\n    files:\n      - /etc/ssh/sshd_config\n",
				},
				{
					Name:        "Gather only what a later condition reads",
					RunbookYAML: "- name: Learn which distribution this is\n  facts.gather:\n    filter:\n      - ansible_distribution*\n",
				},
			},
			SeeAlso: []string{"exec.command", "net.catalyst.device_facts"},
		},
	},
}
