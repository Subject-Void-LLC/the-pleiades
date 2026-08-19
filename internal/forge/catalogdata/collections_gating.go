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
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes an HTTP request and reports its status code and body.",
			Description: "Calls a URL from wherever the task runs, not from the target device, and records the status, body and response headers. A response whose status is not one of the expected ones fails the task, after recording what came back, since the body is usually the only thing that explains the failure. Certificates are verified unless a task says otherwise in its own text. Reporting changed follows the verb: GET, HEAD, OPTIONS and TRACE are read-only by HTTP's own definition and report no change, while any other verb reports a change, because what it did to the far side cannot be inspected from here. That is a deliberate difference from ansible.builtin.uri, which never reports changed at all. Four of that module's parameters are absent rather than accepted and ignored: body_format (the body is sent exactly as written, so set Content-Type in headers), return_content (the body is always recorded), follow_redirects (redirects are always followed), and the url_username and url_password pair (a credential belongs in the credential store, not in a runbook file).",
			Params: []collection.Param{
				{Name: "url", Type: "string", Required: true, Description: "The URL to call. It must be http or https: any other scheme is refused rather than attempted, since this method speaks one protocol and a file or ftp URL is a mistake in the runbook rather than a request this could make."},
				{Name: "method", Type: "string", Default: "GET", Description: "The HTTP method. It is upper-cased before being sent, because HTTP method names are case sensitive and a server given get will answer 501 rather than doing what the author meant."},
				{Name: "body", Type: "string", Description: "The request body, sent exactly as written. Set its Content-Type through headers: nothing here inspects the body or guesses a type for it."},
				{Name: "headers", Type: "dict", Description: "Request headers as a mapping of name to value. Every value must be text, so a numeric one is quoted in the runbook. A Host header is honored as the request's real Host rather than added as an ordinary header, which is what makes name-based routing testable against an address."},
				{Name: "status_code", Type: "int or list of int", Default: "200", Description: "The status code, or codes, that count as success. Anything else fails the task. Written as one number or a list of them, so an endpoint answering 200 or 201 depending on whether it created something can be accepted without a follow-up condition."},
				{Name: "timeout", Type: "float", Default: "30", Description: "How long to wait, in seconds, for the whole request including reading the body. Fractions are allowed, unlike Ansible's whole-second version, since a health check with a half-second budget is a real thing to want. Zero and negative values are refused: neither is a wait."},
				{Name: "validate_certs", Type: "bool", Default: "true", Description: "Verify the server's TLS certificate. Setting it false accepts any certificate, including one an attacker in the middle presents, so it belongs only on a target you have decided does not need it. There is no setting anywhere that changes the default: turning verification off is written in the task it applies to."},
			},
			Returns: []collection.ReturnField{
				{Name: "status", Type: "int", Returned: "always", Description: "The status code the server answered with, recorded even when it is not one of the expected ones."},
				{Name: "content", Type: "string", Returned: "always", Description: "The whole response body as text. Held in memory, so this method is for calling an API rather than for fetching a large file."},
				{Name: "headers", Type: "dict", Returned: "always", Description: "The response headers, names lower-cased, with a header sent more than once joined by a comma and a space."},
				{Name: "url", Type: "string", Returned: "always", Description: "The URL the response actually came from, which differs from the one asked for when redirects were followed."},
				{Name: "elapsed", Type: "float", Returned: "always", Description: "How long the request took, in seconds, with its fraction kept. Ansible reports whole seconds here, which is zero for every call that went well."},
				{Name: "msg", Type: "string", Returned: "always", Description: "The status line, for example \"404 Not Found\", for a person reading a run log."},
			},
			Examples: []collection.Example{
				{
					Name:        "Check that a service answers",
					RunbookYAML: "- name: Wait for the health endpoint\n  fqcn: http.request\n  params:\n    url: https://api.example.com/healthz\n    timeout: 5\n",
				},
				{
					Name:        "Post JSON to an API",
					RunbookYAML: "- name: Register the release\n  fqcn: http.request\n  params:\n    url: https://api.example.com/releases\n    method: POST\n    body: '{\"version\": \"1.4.0\"}'\n    headers:\n      Content-Type: application/json\n    status_code:\n      - 200\n      - 201\n",
				},
			},
			SeeAlso: []string{"exec.command", "pleiades.builtin.wait.port"},
		},
	},
	{
		// Namespaced under pleiades.builtin, not the bare "wait.port" its
		// wait.path/wait.search siblings still use: this is the first
		// (and, as of this entry, only) native-only method in the catalog
		// deliberately marked as belonging to Pleiades' own reserved
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
					RunbookYAML: "- name: Wait for postgres to accept connections\n  fqcn: pleiades.builtin.wait.port\n  params:\n    port: 5432\n    timeout: 120\n",
				},
				{
					Name:        "Wait for a port to be released before rebinding it",
					RunbookYAML: "- name: Wait for the old listener to go away\n  fqcn: pleiades.builtin.wait.port\n  params:\n    port: 8080\n    state: stopped\n    timeout: 60\n",
				},
				{
					Name:        "Give a service a head start, then poll slowly",
					RunbookYAML: "- name: Wait for the API on its private address\n  fqcn: pleiades.builtin.wait.port\n  params:\n    host: 10.0.0.7\n    port: 443\n    delay: 10\n    sleep: 5\n",
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
					RunbookYAML: "- name: Wait for the socket to appear\n  fqcn: wait.path\n  params:\n    path: /run/pleiades/api.sock\n    timeout: 60\n",
				},
				{
					Name:        "Wait for a lock file to be released",
					RunbookYAML: "- name: Wait for the package manager to finish\n  fqcn: wait.path\n  params:\n    path: /var/lib/dpkg/lock-frontend\n    state: absent\n    timeout: 300\n    sleep: 5\n",
				},
				{
					Name:        "Give a slow starter a head start",
					RunbookYAML: "- name: Wait for the pid file, but not immediately\n  fqcn: wait.path\n  params:\n    path: /run/app.pid\n    delay: 10\n    timeout: 120\n",
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
			Description: "Reads a file on the device on an interval until search_regex matches it, or until it stops matching when state is absent, and fails when the timeout runs out first. It changes nothing. The whole file is read on every look, so this suits a service's startup log rather than a file that grows without limit. The pattern is matched in multiline mode, as wait_for matches it, so ^ and $ mean the start and end of a line; it is a Go RE2 pattern, so a backreference or a lookahead is refused when the task is read rather than silently never matching. Only a regular file is read: a path that is missing, or that is a directory, counts as not matching, which satisfies state: absent at once and never satisfies state: present.",
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
					RunbookYAML: "- name: Wait for the API to finish starting\n  fqcn: wait.search\n  params:\n    path: /var/log/pleiades/api.log\n    search_regex: \"^Listening on \"\n    timeout: 120\n",
				},
				{
					Name:        "Capture the port a service chose",
					RunbookYAML: "- name: Read the port out of the log\n  fqcn: wait.search\n  params:\n    path: /var/log/app.log\n    search_regex: \"listening on port (?P<port>[0-9]+)\"\n  register: startup\n",
				},
				{
					Name:        "Wait for an error line to be rotated away",
					RunbookYAML: "- name: Wait for the log to stop showing the failure\n  fqcn: wait.search\n  params:\n    path: /var/log/app.log\n    search_regex: \"FATAL\"\n    state: absent\n    timeout: 60\n    sleep: 5\n",
				},
			},
			SeeAlso: []string{"wait.path", "pleiades.builtin.wait.port"},
		},
	},
	{
		Name:          "facts.gather",
		Capabilities:  []capability.Name{capability.NameFactGatherer},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Gathers baseline system facts from the target (OS, kernel, distribution).",
			Description: "Reads a small, fixed set of system facts over SSH and emits each one as a fact, so it is kept as drift data rather than as a value that expires at the end of the run. A fact the device cannot answer for is left out entirely: there is no guessed default and no empty string, so a condition reading ansible_distribution can tell \"this device does not say\" from \"this device says nothing\". Nothing is changed and no command is elevated, so this always reports no change. Three of ansible.builtin.setup's parameters are absent rather than accepted and ignored: gather_subset has nothing to select between at this size, gather_timeout is the task's own timeout here, and fact_path's local fact files are a separate feature this does not implement.",
			Params: []collection.Param{
				{Name: "filter", Type: "list of string", Description: "Shell-style patterns, as ansible.builtin.setup takes them, narrowing which facts are gathered. A fact is gathered when its name matches any pattern, so [\"ansible_distribution*\"] gathers the distribution and its version and nothing else. Leaving this out gathers everything, and so does an empty list. A pattern set matching none of the facts this method knows about is refused rather than gathering nothing, since that is a typo far more often than an intention. Narrowing also skips the commands behind the facts it excludes, so it is a real saving rather than a filter on the way out."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "ansible_hostname", Type: "string", Returned: "when the device reports one", Description: "The short host name, which is everything before the first dot of what uname reports."},
				{Name: "ansible_kernel", Type: "string", Returned: "when the device reports one", Description: "The kernel release, as uname -r reports it."},
				{Name: "ansible_architecture", Type: "string", Returned: "when the device reports one", Description: "The machine hardware name, as uname -m reports it, for example x86_64."},
				{Name: "ansible_distribution", Type: "string", Returned: "when /etc/os-release names one", Description: "The distribution name, read from NAME in /etc/os-release, for example Ubuntu."},
				{Name: "ansible_distribution_version", Type: "string", Returned: "when /etc/os-release names one", Description: "The distribution version, read from VERSION_ID in /etc/os-release. A rolling release that publishes no VERSION_ID reports no such fact."},
				{Name: "ansible_memtotal_mb", Type: "int", Returned: "when /proc/meminfo reports it", Description: "Total usable memory in megabytes, from MemTotal in /proc/meminfo, truncated the way Ansible truncates it."},
				{Name: "ansible_processor_count", Type: "int", Returned: "when /proc/cpuinfo reports it", Description: "The number of physical processor packages, counted as the distinct physical id values in /proc/cpuinfo. An architecture whose /proc/cpuinfo carries no physical id field reports no such fact rather than a guessed 1."},
				{Name: "ansible_uptime_seconds", Type: "int", Returned: "when /proc/uptime reports it", Description: "How long the device has been up, in whole seconds, from the first field of /proc/uptime."},
			},
			Examples: []collection.Example{
				{
					Name:        "Gather everything before deciding what to do",
					RunbookYAML: "- name: Learn what this device is\n  fqcn: facts.gather\n",
				},
				{
					Name:        "Gather only what a later condition reads",
					RunbookYAML: "- name: Learn which distribution this is\n  fqcn: facts.gather\n  params:\n    filter:\n      - ansible_distribution*\n",
				},
			},
			SeeAlso: []string{"exec.command", "net.catalyst.device_facts"},
		},
	},
}
