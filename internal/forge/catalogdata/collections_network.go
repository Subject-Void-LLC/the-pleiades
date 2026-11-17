// This file holds the Network devices section of docs/hephaestus.md's
// catalog: ansible.netcommon.cli_command/cli_config/netconf_config, and
// the three vendor-specific config modules (cisco.ios, junipernetworks.junos,
// arista.eos). RequiresElevation does not map cleanly onto a network OS's
// own enable-mode privilege model, so every entry here leaves it false
// rather than asserting a Linux-specific semantic onto network gear.
package catalogdata

import (
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/clidialog"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosparse"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/tlsfacts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangfacts"
)

var networkCollections = []collectionscaffold.Config{
	{
		Name:          "net.cli.command",
		Capabilities:  []capability.Name{capability.NameNetworkCLI},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Runs one show/exec-mode command against a network device's CLI.",
			Description: netCLICommandDescription,
			Params:      netCLICommandParams(),
			Returns:     netCLICommandReturns(),
			Examples: []collection.Example{
				{
					Name:        "Read a device's software version",
					RunbookYAML: "- name: Check the running version\n  net.cli.command:\n    command: show version\n  register: version\n",
				},
				{
					Name:        "Answer the question a command asks",
					RunbookYAML: "- name: Copy the configuration to a file\n  net.cli.command:\n    command: copy running-config flash:before-change.cfg\n    prompts:\n      - match: 'Destination filename \\[[^\\]]*\\]\\?'\n        answer: \"\"\n      - match: 'over ?write\\? \\[confirm\\]'\n        refuse: true\n",
				},
			},
			SeeAlso: []string{"net.cli.config", "net.ios.command", "net.ios.config"},
		},
	},
	{
		Name:          "net.cli.config",
		Capabilities:  []capability.Name{capability.NameNetworkCLI},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Applies configuration lines to a network device over its CLI.",
			Description: "Splits config into non-blank lines and sends each one through the same generic session net.cli.command uses, one line at a time, matched against the device's own declared cli_prompt property. It carries no vendor-specific configuration-mode knowledge of its own: it never enters or leaves a configuration mode on the caller's behalf, so a device that needs one (Cisco IOS's \"configure terminal\"/\"end\", for instance) must have those lines included in config itself. net.ios.config is the Cisco-specific sibling that does drive configuration mode, and is what a runbook targeting Cisco IOS should use instead. Reports changed every time every line reaches the device without error, the same convention net.cli.command and exec.command both establish: a CLI line's effect cannot be inspected, so this platform does not guess at one. Each line is bounded to 30 seconds for the same reason net.cli.command's own line is: a generic device has no known paging convention, so an unexpectedly long response can pause on a pager prompt this method cannot answer, and a bounded, clear error is preferable to an indefinite hang.",
			Params: []collection.Param{
				{Name: "config", Type: "string", Required: true, Description: "The configuration lines to send, one per line. Blank lines are skipped. Include any vendor-specific mode commands (e.g. \"configure terminal\" and \"end\") this device needs, since this method sends none on its own.", Format: collection.ParamFormatCommand},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
			},
			Examples: []collection.Example{
				{
					Name:        "Apply configuration on a device with no vendor-specific method yet",
					RunbookYAML: "- name: Set a banner\n  net.cli.config:\n    config: |\n      configure terminal\n      banner motd ^Cauthorized access only^C\n      end\n",
				},
			},
			SeeAlso: []string{"net.cli.command", "net.ios.config"},
		},
	},
	// net.ssh.ping is Phase 16 (Native Go Execution Adapter)'s own real,
	// StatusImplemented method: a lightweight connectivity check that
	// dials a real SSH connection and echoes a value back, the artifact
	// that phase's Release Gate proves the full distributed execution
	// chain (dispatch, a real JIT-delivered secret, the Runner's DAG
	// executor, the per-task subprocess boundary, a real device) through.
	{
		Name:          "net.ssh.ping",
		Capabilities:  []capability.Name{capability.NameSSHTransport},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Opens a real SSH connection to the target and echoes a value back, to prove reachability.",
			Description: "Dials the device's SSHTransportCapable host and port, authenticates with the credential the Controller attached to this dispatch, and runs a trivial, read-only remote command that prints pong, which comes back as reply. It takes no parameters: unlike Ansible's ping it has no data to echo, and validation refuses one, on the command line and on a Runner alike. Never reports changed: a connectivity check does not alter device state.",
			Returns: []collection.ReturnField{
				{Name: "reply", Type: "string", Returned: "always", Description: "The trimmed value the remote command echoed back."},
			},
			Examples: []collection.Example{
				{Name: "Check a device is reachable over SSH", RunbookYAML: "- name: Ping the device\n  net.ssh.ping:\n  register: reachability\n"},
			},
		},
	},
	// net.netconf.facts is the read-only half of what every NETCONF session
	// discovers (owner decision 2026-11-14): a Day-N reading of a device's
	// capabilities, datastores and YANG modules without an edit.
	{
		Name:          "net.netconf.facts",
		Capabilities:  []capability.Name{capability.NameNetconf, capability.NameSSHTransport},
		Transports:    []string{"netconf"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Records what a NETCONF session discovers about a device: its capabilities, datastores and YANG modules.",
			Description: "Opens an RFC 6241 NETCONF session over the SSH \"netconf\" subsystem, exactly as net.netconf.config does, and records what the session discovered, changing nothing: every capability the hello advertised that names no YANG module, the datastores the session could use, and the YANG modules the device implements, read from its YANG library (RFC 8525, or RFC 7895's modules-state) when it advertises one, since a YANG 1.1 device need not list its modules in the hello. The same facts every net.netconf.config session records, so a device read on a schedule shows a change in what it offers (IOS XE drops :writable-running when its candidate datastore is turned on) as drift. A session whose host key was not verified records its findings as stats only, never as facts. Reports no change, and a check runs it for real.",
			Params: []collection.Param{
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it; nothing the session discovers is then recorded as a fact."},
			},
			Returns: []collection.ReturnField{
				{Name: "netconf.capabilities", Type: "list of string", Returned: "on every session, as a stat; as a fact only when its host key was verified", Description: "Every capability the device's hello advertised that names no YANG module (RFC 6241's own and a vendor's), with its parameters but the volatile content-id and module-set-id, which yang.content_id holds, sorted; the YANG module capabilities are in yang.modules instead. Left out, with a warning, when it does not fit in 60 KiB. Read on every session, so a device that changes what it offers (IOS XE drops :writable-running when its candidate datastore is turned on) reads as a change."},
				{Name: "netconf.datastores", Type: "list of string", Returned: "on every session, as a stat; as a fact only when its host key was verified", Description: "The datastores the session could use, sorted: running always, candidate and startup when the hello advertises them, and every datastore the device's YANG library lists (an NMDA device's operational and intended)."},
				{Name: "netconf.modules", Type: "list of string", Returned: "on every session, as a stat; as a fact only when its host key was verified", Description: yangfacts.OwnModulesDescription("NETCONF")},
				{Name: "yang.modules", Type: "list of string", Returned: "when the list fits in 60 KiB as JSON", Description: "Every YANG module the device implements, each as name@revision (the name alone for a module with no revision), sorted, read from the device's YANG library (RFC 8525, or RFC 7895's modules-state) when it advertises one and from the hello otherwise. The protocols' own modules, which each server lists for itself, are left out (they are in netconf.modules or restconf.modules), so the list is the same whichever protocol read it. An entry that is not a YANG identifier, a revision date or a printable namespace is left out whole, never cut short. Left out when the list does not fit; yang.module_count and yang.modules_digest are always recorded."},
				{Name: "yang.module_count", Type: "int", Returned: "on every session, as a stat; as a fact only when its host key was verified", Description: "How many YANG modules the device implements, as yang.modules counts them."},
				{Name: "yang.modules_digest", Type: "string", Returned: "on every session, as a stat; as a fact only when its host key was verified", Description: "The SHA-256 (hex) of the device's data models in their canonical form (every module's name, namespace, revision, features and deviations, sorted, as JSON), the protocols' own modules left out as yang.modules leaves them, so one device read over NETCONF and over RESTCONF gives one value, two devices implementing the same models read alike, and any change to the set reads as a change of this one value."},
				{Name: "yang.digest_rule", Type: "string", Returned: "on every session, as a stat; as a fact only when its host key was verified", Description: yangfacts.DigestRuleDescription},
				{Name: "yang.completeness", Type: "string", Returned: "on every session, as a stat; as a fact only when its host key was verified", Description: "How far yang.modules is every module the device implements: complete (read from the YANG library, or from the hello of a device with none), partial (a library that could not be read, or came back empty for an account the device's access control hides it from), or unknown (a device that named no modules). A document is refused for a namespace the device does not serve only when this is complete."},
				{Name: "yang.content_id", Type: "string", Returned: "when the device advertises yang-library:1.1", Description: "The YANG library's content-id from the hello, a hint the device may regenerate on restart; yang.modules_digest is the module set's identity."},
			},
			Examples: []collection.Example{
				{
					Name:        "Read what a device offers over NETCONF",
					RunbookYAML: "- name: Discover the device's YANG modules and datastores\n  net.netconf.facts:\n  register: discovered\n",
				},
			},
			SeeAlso: []string{"net.netconf.config", "net.ios.facts"},
		},
	},
	// net.restconf.facts is RESTCONF's read of what net.netconf.facts reads
	// over NETCONF (owner decision 2026-11-14: yang.* is the schema's family,
	// shared by every protocol): the root, the capabilities, the datastores
	// and the YANG module set, without an edit.
	{
		Name:          "net.restconf.facts",
		Capabilities:  []capability.Name{capability.NameRestconf},
		Transports:    []string{"restconf"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Records what a device's RESTCONF server says about it: its root, capabilities, datastores and YANG modules.",
			Description: restconfFactsDescription,
			Returns:     restconfFactsReturns(),
			Examples: []collection.Example{
				{
					Name:        "Read what a device offers over RESTCONF",
					RunbookYAML: "- name: Discover the device's YANG modules over RESTCONF\n  net.restconf.facts:\n  register: discovered\n",
				},
			},
			SeeAlso: []string{"net.netconf.facts"},
		},
	},
	// net.restconf.config is RESTCONF's write, after
	// ansible.netcommon.restconf_config: one data resource posted, put,
	// patched or deleted, its change read back rather than assumed.
	{
		Name:          "net.restconf.config",
		Capabilities:  []capability.Name{capability.NameRestconf},
		Transports:    []string{"restconf"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Writes one YANG data resource over RESTCONF (post, put, patch or delete), reporting whether it changed, with an optional pre-change backup.",
			Description: "Finds the device's RESTCONF root from its /.well-known/host-meta, read with no credential, or, when host-meta answers 404 or a redirect (never followed), as IOS XE answers it to anything but a valid login with a 302 to its WebUI's login page (404 there), takes the conventional /restconf for the first authenticated read to confirm; it reads its capabilities and YANG library as net.restconf.facts does (recording the same facts), and writes the resource path names with method: post creates content under path, put creates or replaces the resource at path, patch merges content into it, and delete removes it. A write to the conventional /restconf that nothing under it confirmed (neither the capabilities nor the YANG library answered) is refused before the document is sent. Parameter names are ansible.netcommon.restconf_config's own. The device is reached at its own origin with its own TLS (always verified, a pinned certificate included) and its own stored credential, following no redirect, and a refused credential is not sent again within the task. A request the device refuses with 409 Conflict, in-use, \"Too many sessions.\" (IOS XE counts every RESTCONF request as a session until it expires) is sent again after a wait that starts at 2 seconds and doubles to at most 30, for up to 11 minutes a request and 15 minutes in all per task, and the task warns how many times requests were refused and how long it waited. IOS XE's answer suggests it opened no session for the refused try and applied nothing, which was measured for reads, never for writes, so a write sent again whose answer a first try having applied it would also explain (a post answered 409 data-exists or resource-denied, a delete answered 404, a put of a resource the read before did not find answered other than 201 Created) fails saying whether the first try applied it is not known, the resource read after where its path is known and no undo recorded. path is parsed, never passed on: each node is held to the YANG identifier grammar and each key decoded once and encoded again, so a key holding / or , stays inside its key and never addresses another resource. A module the device does not implement is refused before anything is sent, when its YANG library was read whole, and so is a datastore its library does not list. The resource is read before and after the write, in format, and the two compared with key order and white space set aside, so a run that leaves the resource as it found it reports no change: a put or patch of what is already there changes nothing, a delete of a resource that is not there sends nothing, and a post answered 409 Conflict (RFC 8040's resource-denied, or data-exists: the resource is there already, and post never replaces one) changes nothing and says so. A patch of a resource that is not there is refused before anything is sent. A rejected write comes back as the device's own ietf-restconf:errors, structured and masked. Every answer recorded (the backup, the diff and every error) is masked by value, the device's credential and every named secret the content resolved in each form it could be echoed in, and then by shape, every secret-named leaf and crypt-shaped value; the content itself is never recorded.",
			Params: []collection.Param{
				{Name: "path", Type: "string", Required: true, Description: "The resource to write, as RFC 8040 writes a data resource path: module:node/list=key1,key2/..., such as ietf-netconf-acm:nacm/groups/group=ops, under the root's /data, or under the datastore named. A key holding /, , or = must be percent-encoded (%2F, %2C, %3D); every key is decoded once and encoded again, and a path that does not parse is refused before anything is sent, as is a query, a fragment, an empty node or a dot segment. The first node names its module. For post, path is the parent the new resource is created under."},
				{Name: "content", Type: "string", SecretRefs: true, Description: "The document to write, in format: for post the resource to create under path, for put the resource at path whole, for patch what to merge into it. Required for those, refused with delete. JSON must parse; XML must be well-formed, with no DOCTYPE or other directive, and nest no deeper than 256 elements. A JSON string value or an XML leaf may name a stored secret, such as a pre-shared key, with a secret reference, {{ secret.<name>.<field> }} (pleiades add-secret): it is resolved just before the write, escaped as JSON string content or as XML text, and masked in everything the run reports; the content is never recorded. A reference outside a JSON string is refused. A document holding one holds no other template expression, so a per-device value and a secret go in separate tasks, and a value with whitespace, a control character or ? is refused. Content holding a masked placeholder (pleiades-masked-) is refused, since it would set each secret to the placeholder."},
				{Name: "method", Type: "string", Default: "post", Choices: []string{"post", "put", "patch", "delete"}, ChoicesIgnoreCase: true, Description: "How to write: post (create under path; never replaces), put (create or replace the resource at path whole), patch (merge into the resource at path, which must exist), or delete (remove it)."},
				{Name: "format", Type: "string", Default: "json", Choices: []string{"json", "xml"}, ChoicesIgnoreCase: true, Description: "The encoding of content, and of the reads before and after: json (application/yang-data+json) or xml (application/yang-data+xml)."},
				{Name: "datastore", Type: "string", Choices: []string{"running", "startup"}, ChoicesIgnoreCase: true, Description: "Write the NMDA datastore RFC 8527 addresses under the root's /ds/, ietf-datastores:running or ietf-datastores:startup, instead of the conventional /data. Refused before anything is sent unless the device's YANG library lists that datastore. candidate is not offered: RESTCONF has no commit, so an edit there would stay staged for the next commit by any client."},
				{Name: "backup", Type: "bool", Default: "false", Description: "Record the resource as it was before the write under the backup stat, masked as every recorded answer is: empty when the resource was not there, and always for a post, which reads nothing before it writes (path is the parent, and the resource a post creates is not there yet). A document masking cannot vouch for fails the task before anything is written."},
			},
			Returns: append([]collection.ReturnField{
				{Name: "backup", Type: "string", Returned: "when backup is true", Description: "The resource as the device answered it before the write, in format, masked by value (the device's credential and every named secret the content resolved) and by shape (every secret-named leaf and crypt-shaped value); empty when the resource was not there, and always for a post. net.restconf.config refuses content holding a placeholder, so this text cannot be written back as it is."},
			}, restconfFactsReturns()...),
			Examples: []collection.Example{
				{Name: "Create or replace an access-control group", RunbookYAML: "- name: Make the ops group\n  net.restconf.config:\n    method: put\n    path: ietf-netconf-acm:nacm/groups/group=ops\n    content: '{\"ietf-netconf-acm:group\": [{\"name\": \"ops\", \"user-name\": [\"alice\"]}]}'\n"},
				{Name: "Remove a group whose name holds a slash", RunbookYAML: "- name: Remove the ops/lab group\n  net.restconf.config:\n    method: delete\n    path: ietf-netconf-acm:nacm/groups/group=ops%2Flab\n"},
			},
			SeeAlso: []string{"net.restconf.facts", "net.netconf.config"},
		},
	},
	{
		Name:          "net.netconf.config",
		Capabilities:  []capability.Name{capability.NameNetconf, capability.NameSSHTransport},
		Transports:    []string{"netconf"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Applies a configuration document to a device over NETCONF, reporting whether it changed, with its undo where exact, an optional pre-change backup, and a gated check through the candidate datastore.",
			Description: "Opens an RFC 6241 NETCONF session over the SSH \"netconf\" subsystem and applies content to the chosen datastore with edit-config. Parameter names are ansible.netcommon.netconf_config's own, with its target parameter under its own alias, datastore, since target names the device a task runs on. The session negotiates RFC 6242 chunked framing whenever the device offers base:1.1, and requests rollback-on-error whenever the device advertises it, so a rejected document leaves the device unchanged rather than half configured; that matters most on a device offering only writable-running, which is what Cisco IOS XE offers until its candidate datastore is configured, because such a device has no staging area and every element lands on the live configuration as it is applied. A datastore the device never advertised support for is refused when the session opens rather than at the first write, naming the missing capability. Unlike the net.cli.* and net.ios.config methods, a rejected element comes back as a structured error carrying the device's own error-tag and the XPath of the element it objected to. Whether the task changed anything is read, not assumed: the configuration the edit can touch is read with get-config before and after the edit (running, or startup for a write to startup), through a subtree filter built from the content that selects every instance of each element's name by name alone, never narrowed by a value, in RFC 6243 with-defaults explicit mode where the device offers it and otherwise in its own basic mode; the two reads are compared in document order with namespaces resolved and white space between elements set aside, so a re-run that leaves the configuration as it found it reports no change. A default_operation of replace reads the whole datastore. A candidate edit with commit false is validated and discarded and reports no change. A run that creates configuration records the removal of what it created, built from its own content; a change to what was there records no undo, and the run says why whenever it records none. A check runs only with candidate_check: true, through the candidate datastore.",
			Params: []collection.Param{
				{Name: "content", Type: "string", Required: true, SecretRefs: true, Description: "The configuration document to apply, as the XML that goes inside edit-config's <config> element: well-formed, holding no DOCTYPE, nesting no deeper than 256 elements, and holding no masked placeholder (pleiades-masked-), each refused before a session opens. Per-element operations are expressed the standard way, with an nc:operation attribute; pair that with default_operation: none so the device changes only what the document explicitly names. A leaf may name a stored secret, such as an IKE pre-shared key, with a secret reference, {{ secret.<name>.<field> }} (pleiades add-secret): it is resolved just before the session opens, escaped as XML text, and masked in everything the run reports; the content is never recorded, and a call whose content names a secret records no undo. A document holding one holds no other template expression, so a per-device value and a secret go in separate tasks, and a value with whitespace, a control character or ? is refused."},
				{Name: "datastore", Type: "string", Choices: []string{"running", "candidate", "startup"}, ChoicesIgnoreCase: true, Description: "The datastore to configure: running, candidate or startup. Left unset it is chosen when the session opens, from what the device's hello advertises that session: running when it offers :writable-running, otherwise candidate (locked, validated when the device offers :validate, then committed). That is chosen afresh on every task, since a device's offer changes with its configuration: Cisco IOS XE offers only running until netconf-yang feature candidate-datastore is configured, and then only candidate. A datastore the device does not advertise is refused before anything is applied."},
				{Name: "default_operation", Type: "string", Default: "merge", Choices: []string{"merge", "replace", "none"}, Description: "What the device does with elements carrying no explicit operation attribute: merge, replace or none. RFC 6241 defines no \"delete\" here; express a delete with an nc:operation attribute in content."},
				{Name: "error_option", Type: "string", Choices: []string{"stop-on-error", "continue-on-error", "rollback-on-error"}, Description: "How the device handles a rejected element: stop-on-error, continue-on-error or rollback-on-error. Left unset this method asks for rollback-on-error whenever the device advertises the capability, and otherwise leaves the device its own default, stop-on-error, because stop-on-error leaves a rejected document half applied."},
				{Name: "lock", Type: "string", Default: "never", Choices: []string{"never", "always", "if_supported"}, ChoicesIgnoreCase: true, Description: "Whether to lock the datastore for the duration: never, always, or if_supported. Locking prevents another client changing the datastore mid-edit; it also blocks every other client, which matters on a shared device. The candidate datastore is always locked, and never is refused there: the lock is refused while another client's changes sit in the candidate, so this task commits and discards only its own edit."},
				{Name: "commit", Type: "bool", Default: "true", Description: "On the candidate datastore, commit after the edit, which is validated first when the device offers :validate. false validates the edit and discards it, so it is checked by the device and applied nowhere, and reports no change, with validated and applied saying what ran; unlike ansible.netcommon.netconf_config it never leaves the edit staged, where the next commit by any client would apply it. false is refused when the session writes running, where an edit is live at once."},
				{Name: "candidate_check", Type: "bool", Default: "false", Description: "In a check (pleiades run --mode check, or check_mode: true), check this task through the device's candidate datastore rather than report it unchecked. The check takes the candidate's lock first (a lock the device refuses, as it does while the candidate holds changes not committed, reports the task unchecked with nothing sent), proves the candidate holds nothing uncommitted in what the content touches, stages the edit there, has the device validate it, reads it, and discards it, proving the candidate reads as it did before; it holds the lock for at most 30 seconds, after which the session is closed. Only a check's own RPCs can be sent (lock, unlock, edit-config and validate of the candidate, discard-changes, reads and close-session): a commit, or a write to running or startup, is refused before it is written. Its result says validated; commit not exercised, since a device may refuse at commit what it validated, and changed says whether the edit changed the candidate. It runs only from the command line, never on a simulate-locked device, never for a write to startup or content naming a secret, never after an earlier task of the same check predicted a change on the device (or could not be checked, or failed), since the device as it is now is then not what this task would meet, only when this session's own hello advertises :candidate and :validate, and only on a generic_netconf device whose server is Netopeer2 over sysrepo, the one server measured to refuse the lock on a candidate holding another session's changes, to leave the candidate as running when a session is killed after its edit, and to restore it on a discard; each of those is reported unchecked with its reason. Any failure between sending the edit and proving the discard fails the check as candidate state unverified, and then the check does not unlock (an unlock may leave the edit staged for the next commit by any client) but closes its session holding the lock. A real run ignores it."},
				{Name: "backup", Type: "bool", Default: "false", Description: "Capture the running datastore's full contents with get-config before applying anything, whichever datastore the task writes, recorded under the backup stat with every secret masked. A configuration masking cannot vouch for fails the task before anything is applied."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "validated", Type: "bool", Returned: "when commit is false", Description: "Whether the device validated the candidate edit before it was discarded: false on a device that does not offer :validate, where only the edit itself was accepted."},
				{Name: "applied", Type: "bool", Returned: "when commit is false", Description: "Always false: the edit was discarded, not applied."},
				{Name: "candidate_check", Type: "string", Returned: "in a check through the candidate datastore", Description: "What the check proved: validated; commit not exercised. The device validated the edit in its locked candidate datastore, the edit was discarded, and the candidate was proven to read as it did before; a device may still refuse at commit what it validated."},
				{Name: "backup", Type: "string", Returned: "when backup is true", Description: "The datastore's full contents as XML, captured immediately before this task's own document was applied, with every secret masked: the text of each leaf whose name holds a secret (password, secret, a key such as auth-key or pre-shared-key, encrypted-password, RFC 8177's key-string, keystring and hexadecimal-string, OpenConfig's key-clear-text, an SNMP community), of every other leaf inside such an element at any depth but its encryption type, id, algorithm or lifetime (and, two or more levels down outside SNMP, a name or address), and of each crypt-shaped value ($1$, $6$, $9$) is replaced by pleiades-masked- and sixteen hexadecimal digits, equal secrets alike within one run; the run's own credential is masked wherever it appears, other leaves are read by the configuration text masker net.ios.config's backup uses, and comments are withheld. Every element, name, address and encryption type stays where it was. A document that is not well-formed XML, holds a DOCTYPE, nests past 256 elements, has an attribute that may hold a secret, or has a line the text masker cannot vouch for fails the task before anything is applied, and none of it is recorded. net.netconf.config refuses content holding a placeholder, so this text cannot be applied back as it is."},
				{Name: "netconf.capabilities", Type: "list of string", Returned: "on every session, as a stat; as a fact only when its host key was verified", Description: "Every capability the device's hello advertised that names no YANG module (RFC 6241's own and a vendor's), with its parameters but the volatile content-id and module-set-id, which yang.content_id holds, sorted; the YANG module capabilities are in yang.modules instead. Left out, with a warning, when it does not fit in 60 KiB. Read on every session, so a device that changes what it offers (IOS XE drops :writable-running when its candidate datastore is turned on) reads as a change."},
				{Name: "netconf.datastores", Type: "list of string", Returned: "on every session, as a stat; as a fact only when its host key was verified", Description: "The datastores the session could use, sorted: running always, candidate and startup when the hello advertises them, and every datastore the device's YANG library lists (an NMDA device's operational and intended)."},
				{Name: "netconf.modules", Type: "list of string", Returned: "on every session, as a stat; as a fact only when its host key was verified", Description: yangfacts.OwnModulesDescription("NETCONF")},
				{Name: "yang.modules", Type: "list of string", Returned: "when the list fits in 60 KiB as JSON", Description: "Every YANG module the device implements, each as name@revision (the name alone for a module with no revision), sorted, read from the device's YANG library (RFC 8525, or RFC 7895's modules-state) when it advertises one and from the hello otherwise. The protocols' own modules, which each server lists for itself, are left out (they are in netconf.modules or restconf.modules), so the list is the same whichever protocol read it. An entry that is not a YANG identifier, a revision date or a printable namespace is left out whole, never cut short. Left out when the list does not fit; yang.module_count and yang.modules_digest are always recorded."},
				{Name: "yang.module_count", Type: "int", Returned: "on every session, as a stat; as a fact only when its host key was verified", Description: "How many YANG modules the device implements, as yang.modules counts them."},
				{Name: "yang.modules_digest", Type: "string", Returned: "on every session, as a stat; as a fact only when its host key was verified", Description: "The SHA-256 (hex) of the device's data models in their canonical form (every module's name, namespace, revision, features and deviations, sorted, as JSON), the protocols' own modules left out as yang.modules leaves them, so one device read over NETCONF and over RESTCONF gives one value, two devices implementing the same models read alike, and any change to the set reads as a change of this one value."},
				{Name: "yang.digest_rule", Type: "string", Returned: "on every session, as a stat; as a fact only when its host key was verified", Description: yangfacts.DigestRuleDescription},
				{Name: "yang.completeness", Type: "string", Returned: "on every session, as a stat; as a fact only when its host key was verified", Description: "How far yang.modules is every module the device implements: complete (read from the YANG library, or from the hello of a device with none), partial (a library that could not be read, or came back empty for an account the device's access control hides it from), or unknown (a device that named no modules). A document is refused for a namespace the device does not serve only when this is complete."},
				{Name: "yang.content_id", Type: "string", Returned: "when the device advertises yang-library:1.1", Description: "The YANG library's content-id from the hello, a hint the device may regenerate on restart; yang.modules_digest is the module set's identity."},
			},
			Examples: []collection.Example{
				{
					Name:        "Set a device's hostname over NETCONF",
					RunbookYAML: "- name: Set the hostname\n  net.netconf.config:\n    content: |\n      <native xmlns=\"http://cisco.com/ns/yang/Cisco-IOS-XE-native\">\n        <hostname>edge-01</hostname>\n      </native>\n    backup: true\n  register_mask: backup\n",
				},
				{
					Name:        "Remove an interface, changing nothing else",
					RunbookYAML: "- name: Remove the loopback\n  net.netconf.config:\n    default_operation: none\n    content: |\n      <native xmlns=\"http://cisco.com/ns/yang/Cisco-IOS-XE-native\">\n        <interface>\n          <Loopback xmlns:nc=\"urn:ietf:params:xml:ns:netconf:base:1.0\" nc:operation=\"delete\">\n            <name>8990</name>\n          </Loopback>\n        </interface>\n      </native>\n",
				},
			},
			SeeAlso: []string{"net.ios.config", "net.ios.save", "net.cli.config", "net.restconf.config"},
		},
	},
	{
		Name:          "net.ios.config",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Applies configuration lines to a Cisco IOS device, keeping a checkpoint of the configuration before them so the change can be undone exactly.",
			Description: "Opens an interactive session over SSH (or over Telnet, with insecure_telnet), using netcli.IOS's own paging, configuration-mode and error conventions, and applies lines as a batch: \"configure terminal\", each line in order, then \"end\". Aborts on the first line the device rejects (an IOS \"% ...\" error), still leaving configuration mode before returning that error. A \"% \" line IOS prints for a line it accepted is a notice, not a rejection, and is reported under notices: an access list entry, IKEv2 proposal or profile match already there, a key pair being made or replaced, and what removing one says first; accept adds the task's own. A line that changes key material (crypto key generate, zeroize, import, move, decrypt, encrypt) changes what the configuration does not hold, so its run reports changed whatever the configuration shows, and no checkpoint returns it: such a call's undo is never exact. " +
				"A line that asks a question (removing a key pair asks to confirm) is answered from prompts, each answer written in the task, or by a model with ask, which picks one of the task's own choices by id or refuses; a question nothing answers (a line left ending in ?, [confirm], [yes/no] or asking for a password) fails the line at once, naming it, rather than waiting; anything else a device waits on, such as a banner's text, still waits until the read limit. " +
				"Before the lines, it copies the running configuration to a checkpoint file on the device (its default writable disk or flash, such as unix: or flash:), and after them the device's own show archive config differences says what changed: no difference reports unchanged and deletes the checkpoint, so a second run of the same lines changes nothing. A change copies the configuration it left to a second checkpoint and records its undo as net.ios.replace naming both files, which returns the device exactly to the first (configure replace) and refuses if the device no longer matches the second. The files stay on the device until that undo runs. " +
				"A device with no writable file system, or no configure replace, records no undo, with a warning: the configuration lines that would undo the change, written from its configuration read before and after it (the negation of each added line, each removed line put back), are reported under undo_lines, masked, for the task's rollback: list, since lines read off a device never reach the journal. A change inside an access list, a banner or a certificate has no such lines. checkpoint: false does the same on any device. The differences this reports are masked as net.batfish.check masks a candidate. On a switch that keeps its VLANs outside the configuration (VTP server or client mode), lines that make or remove VLANs (vlan N, no vlan N, a list or a range) have the VLAN table read with show vlan brief before and after them: the VLANs they changed are reported under differences (+vlan 60, or a name changed under vlan 50), and a change to the VLAN database alone is undone by net.ios.config with lines that remove each VLAN made and put back each name changed. A VLAN removed, or whose state changed, cannot be made again exactly from that table, so such a change records no undo, saying why; beside a configuration change the return to the checkpoint is partial, since configure replace leaves the VLAN database as it is. A device that refuses show vlan brief is warned of. " +
				"A check reads the running configuration over the same session and sends no line: each line is looked for, as written, in the block the lines before it entered and only there (a line that opens a block, such as interface or address-family, also where IOS would open it), so a line found only in an enclosing block, such as a neighbor under the router and not in the address family entered, is predicted to change. IOS leaves a submode for a global command, and the check follows it: when the lines are indented as show running-config indents them (\" switchport mode trunk\" under its interface), a line at the margin is at the top level, and a line the configuration holds at its top level and not in the current block is taken there wherever it is written. A new line among lines indented nowhere stays in the block before it, so write exit before it or indent the block's lines. On a switch, whose VLANs IOS keeps out of the running configuration in VTP server or client mode, a vlan N line the configuration lacks is held to show vlan brief (its VLAN and name), and a device that refuses that command has it predicted to change, with a warning. The lines not found are reported under differences as IOS reports a change (+ added, - what a no line removes, each under the block it sits in), masked as the backup is, with changed when there is one. A line IOS prints differently (an abbreviation such as int, or a default it does not print, such as no shutdown) is predicted to change. A call with a secret reference, prompts or ask, a crypto key line, a banner, end or a do line is reported unchecked, since only applying it tells what it changes.",
			Params: append(append([]collection.Param{
				{Name: "lines", Type: "list of string", Required: true, Description: "The configuration lines to apply, in order, WITHOUT \"configure terminal\" or \"end\": this method supplies both itself. A line may name a stored secret, such as a pre-shared key, with a secret reference, {{ secret.<name>.<field> }} (pleiades add-secret): it is resolved just before the line is sent, and masked in everything the run reports. A line holding one holds no other template expression, and a value with whitespace, a control character or ? is refused.", Format: collection.ParamFormatCommand, SecretRefs: true},
				{Name: "checkpoint", Type: "bool", Default: "true", Description: "Keep a checkpoint of the running configuration on the device before the change, which its undo returns to exactly. false records no undo, and reports the lines that would undo the change for the task's rollback: list."},
				{Name: "backup", Type: "bool", Default: "false", Description: "Capture the device's running-config with \"show running-config\" before applying any line, recorded under the backup stat."},
				{Name: "accept", Type: "list of string", Description: "Further IOS \"% \" lines to take as notices rather than rejections, each RE2 matched from the start of the line and so beginning with %, e.g. \"% Frob is deprecated\". Every other \"% \" line still fails the task."},
			}, clidialog.ConfigParams()...),
				collection.Param{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				collection.Param{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
			),
			Returns: append([]collection.ReturnField{
				{Name: "checkpoint", Type: "string", Returned: "when a checkpoint was kept and the lines changed the configuration", Description: "The checkpoint file on the device holding the configuration before the change, which the undo returns to."},
				{Name: "differences", Type: "list of string", Returned: "when a checkpoint was kept, and in a check", Description: "The device's own contextual differences the change made (+ added, - removed), masked; in a check, the differences the lines are predicted to make."},
				{Name: "undo_lines", Type: "list of string", Returned: "when no checkpoint was kept and the lines changed the configuration", Description: "The configuration lines that would undo the change, masked; copy them into the task's rollback: list, putting each masked secret back."},
				{Name: "backup", Type: "string", Returned: "when backup is true", Description: "The device's full running-config, captured immediately before this task's own lines were applied, with every secret masked: each password, secret, key, community and pre-shared key, in whatever encoding the device applies (IOS's own \"type 7\" is trivially reversible, so it is masked too), is replaced by a placeholder, and a clear-text one is masked to the end of its line. A configuration the masker cannot vouch for fails the task before any line is applied. It is a record of the configuration, not a file to restore the secrets from."},
				{Name: "notices", Type: "list of string", Returned: "when the device printed one", Description: "The \"% \" lines the device printed that were taken as notices rather than rejections, masked."},
			}, clidialog.ConfigReturns()...),
			Examples: []collection.Example{
				{
					Name:        "Create a loopback interface, undoable by pleiades rollback",
					RunbookYAML: "- name: Add a loopback interface\n  net.ios.config:\n    lines:\n      - interface Loopback0\n      - description managed by pleiades\n",
				},
				{
					Name:        "Remove a key pair, answering the question IOS asks first",
					RunbookYAML: "- name: Remove the old key pair, which no rollback brings back\n  net.ios.config:\n    lines:\n      - crypto key zeroize rsa OLDKEY\n    prompts:\n      - match: 'Do you really want to remove these keys\\? \\[yes/no\\]'\n        answer: \"yes\"\n    accept:\n      - '% The specified keypair does not exist'\n",
				},
			},
			SeeAlso: []string{"net.ios.replace", "net.ios.parse", "net.cli.command", "net.cli.config"},
		},
	},
	{
		Name:          "net.ios.command",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Runs one exec-mode command on a Cisco IOS device, answering the questions it asks as the task says.",
			Description: netIOSCommandDescription,
			Params:      netIOSCommandParams(),
			Returns:     netCLICommandReturns(),
			Examples:    netIOSCommandExamples(),
			SeeAlso:     []string{"net.cli.command", "net.ios.config", "net.ios.save", "ai.chat"},
		},
	},
	{
		Name:          "net.ios.facts",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           netIOSFactsDoc(),
	},
	{
		Name:          "net.ios.ping",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Runs a ping from a Cisco IOS device and reports the result.",
			Description: "Answers a different question from net.ssh.ping, and the difference is the point: net.ssh.ping proves this platform can reach the device, while this method proves the DEVICE can reach somewhere else, which is the question that actually matters when a routing or ACL change is under review. Runs IOS's own ping from an interactive PTY session and parses its \"Success rate is N percent (rx/tx)\" line, including the trailing \"round-trip min/avg/max = a/b/c ms\" clause that IOS omits entirely when nothing came back. Nothing is changed on the device, so this always reports no change. Use state to turn the result into a gate: state present (the default) fails the task when every packet is lost, and state absent fails it when anything answers, so a runbook can assert reachability or its absence without a separate condition.",
			Params: []collection.Param{
				{Name: "dest", Type: "string", Required: true, Description: "The address or hostname to ping from the device.", Format: collection.ParamFormatCommand},
				{Name: "count", Type: "int", Default: "5", Description: "How many echoes to send, passed to IOS as \"repeat\"."},
				{Name: "source", Type: "string", Description: "Source address or interface for the ping, passed to IOS as \"source\".", Format: collection.ParamFormatCommand},
				{Name: "vrf", Type: "string", Description: "VRF to ping from, passed to IOS as \"vrf\".", Format: collection.ParamFormatCommand},
				{Name: "state", Type: "string", Default: "present", Choices: []string{"present", "absent"}, ChoicesIgnoreCase: true, Description: "\"present\" fails the task if the destination is unreachable (0 percent success); \"absent\" fails it if the destination answers at all. Set neither expectation by using a when condition on the returned facts instead."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
			},
			Returns: []collection.ReturnField{
				{Name: "packet_loss", Type: "string", Returned: "always", Description: "Percentage of packets lost, as a string with a trailing percent sign, e.g. \"0%\"."},
				{Name: "packets_tx", Type: "int", Returned: "always", Description: "How many echoes the device sent."},
				{Name: "packets_rx", Type: "int", Returned: "always", Description: "How many replies the device received."},
				{Name: "rtt", Type: "map", Returned: "when at least one packet returned", Description: "Round-trip times in milliseconds: min, avg, max. Absent entirely when every packet was lost, because IOS prints no round-trip clause in that case."},
			},
			Examples: []collection.Example{
				{
					Name:        "Assert the device can still reach its gateway",
					RunbookYAML: "- name: Confirm the upstream gateway answers\n  net.ios.ping:\n    dest: 192.0.2.1\n",
				},
				{
					Name:        "Record reachability without failing the run",
					RunbookYAML: "- name: Measure reachability to a peer\n  net.ios.ping:\n    dest: 198.51.100.10\n    count: 10\n    state: absent\n  register: peer\n",
				},
			},
			SeeAlso: []string{"net.ssh.ping", "net.ios.facts"},
		},
	},
	{
		Name:          "net.ios.save",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Saves a Cisco IOS device's running configuration to startup.",
			Description: "Runs IOS's \"write memory\", copying running-config over startup-config so the current configuration survives a reload. This is the step that makes every earlier net.ios.config task permanent, and it is deliberately a separate method rather than a parameter on net.ios.config: persisting configuration is a decision about blast radius, not a detail of applying a line, and a runbook that applies several changes should be able to decide once, at the end, whether any of them should outlive the next reload. Reports changed whenever the save completes, since IOS gives no way to know whether startup-config already matched. Aborts on a real IOS \"% ...\" error rather than reporting a save that did not happen. A check opens the session a real run opens and predicts the change, since a save always reports one, without sending write memory.",
			Params: []collection.Param{
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
			},
			Returns: []collection.ReturnField{
				{Name: "stdout", Type: "string", Returned: "always", Description: "Whatever the device printed in response to \"write memory\", typically a \"Building configuration...\" line followed by \"[OK]\"."},
			},
			Examples: []collection.Example{
				{
					Name:        "Persist a change after verifying it",
					RunbookYAML: "- name: Save the running configuration\n  net.ios.save:\n",
				},
			},
			SeeAlso: []string{"net.ios.config"},
		},
	},
	{
		Name:          "net.ios.replace",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh", "telnet", "console"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Replaces a Cisco IOS device's running configuration with a checkpoint file on the device.",
			Description: "Runs IOS's configure replace with a checkpoint file the device holds (net.ios.config writes one before each change and records this method as its undo), so the device itself puts back exactly the configuration the checkpoint holds: lines a change added are removed and lines it replaced or removed come back, with no inverse line written by anyone. When expect names a second checkpoint (the one net.ios.config writes after its change), the device must still match it, by the device's own show archive config differences, or this refuses before replacing anything, unless force is true: configure replace returns the whole configuration to the checkpoint, so a change someone made since would be undone with it. After the replace, the device must report no differences from the checkpoint, or the task fails; a line in IOS's \"% \" form the replace prints fails it only then, and when the device matches it is reported as a warning, masked. The checkpoint files net.ios.config made (named pleiades-<id>.cfg) are deleted afterwards unless keep is true; any other file, such as nvram:startup-config or a configuration kept on flash:, is never deleted. A check reports what the replace would change, from the same comparison, and changes nothing. The configuration lines this reports are masked as net.batfish.check masks a candidate: a password, secret or key is never shown.",
			Params: []collection.Param{
				{Name: "checkpoint", Type: "string", Required: true, Description: "The checkpoint to return to, a file on the device such as unix:pleiades-3f2a.cfg or flash:pleiades-3f2a.cfg."},
				{Name: "expect", Type: "string", Description: "A checkpoint the running configuration must still match before anything is replaced, refusing otherwise: the configuration as the change left it."},
				{Name: "force", Type: "bool", Default: "false", Description: "Replace even when the running configuration no longer matches expect, undoing whatever changed since along with the change."},
				{Name: "keep", Type: "bool", Default: "false", Description: "Keep the checkpoint files net.ios.config made on the device after a replace that leaves no differences, rather than deleting them. A file it did not make is never deleted."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
			},
			Returns: []collection.ReturnField{
				{Name: "differences", Type: "list of string", Returned: "always", Description: "The device's own contextual differences between the running configuration and the checkpoint before the replace, masked: what the replace changes, or would change in a check."},
			},
			Examples: []collection.Example{
				{
					Name:        "Return a device to a checkpoint a change left",
					RunbookYAML: "- name: Undo the loopback change\n  net.ios.replace:\n    checkpoint: unix:pleiades-3f2a.cfg\n    expect: unix:pleiades-3f2a-after.cfg\n",
				},
			},
			SeeAlso: []string{"net.ios.config", "net.ios.parse"},
		},
	},
	{
		Name:          "net.ios.checkpoints",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh", "telnet", "console"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Lists the checkpoints net.ios.config left on a Cisco IOS device, and with prune deletes the older ones.",
			Description: "Reads the file system net.ios.config keeps its checkpoints on (the device's default writable disk or flash, such as unix: or flash:) with dir, and reports each change's checkpoints, newest first: its id, the configuration before it and, for a change that completed, after it, with the time and size the device gives. Only files named as net.ios.config names them (pleiades-<12 hex digits>.cfg and -after.cfg) are ever listed or deleted, so a startup configuration or an image is never touched. " +
				"Each checkpoint holds the whole configuration, its secrets included, and stays on the device until its change's undo runs. With prune, the checkpoints of every change but the newest keep, or of every change older than older_than by the device's own clock (show clock, so a router whose clock was never set is measured against itself), or both (a change goes when either says so), are deleted with delete /force; a check reports what would go. A deleted checkpoint's undo (the net.ios.replace net.ios.config recorded) then refuses, so prune only changes no rollback will need, such as those of a lab about to be deleted.",
			Params: []collection.Param{
				{Name: "prune", Type: "bool", Default: "false", Description: "Delete the checkpoints keep and older_than select. Without it the method only lists."},
				{Name: "keep", Type: "int", Description: "With prune, keep the checkpoints of this many of the newest changes and delete the rest; 0 deletes every one."},
				{Name: "older_than", Type: "string", Description: "With prune, delete the checkpoints of every change older than this, a Go duration such as 72h, measured by the device's own clock against the times it gives its files."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
			},
			Returns: []collection.ReturnField{
				{Name: "checkpoints", Type: "list of dict", Returned: "always", Description: "Each change's checkpoints, newest first: id, before, after (empty when the change did not complete), modified (RFC 3339, empty when the device's time did not read) and bytes."},
				{Name: "deleted", Type: "list of string", Returned: "when prune is true", Description: "The files deleted, or in a check the files that would be."},
			},
			Examples: []collection.Example{
				{Name: "Keep the checkpoints of the last five changes", RunbookYAML: "- name: Prune old checkpoints\n  net.ios.checkpoints:\n    prune: true\n    keep: 5\n"},
			},
			SeeAlso: []string{"net.ios.config", "net.ios.replace"},
		},
	},
	{
		Name:          "net.pcap.summary",
		Capabilities:  []capability.Name{},
		Transports:    []string{},
		EngineVersion: engineVersion,
		Site:          collection.SiteController,
		Device:        collection.DeviceNone,
		Doc:           collection.Doc{Summary: "Counts a packet capture in the project by protocol and address pair, encrypted apart from cleartext.", Description: "Reads a pcap or pcapng file from the project (virt.cml.capture.stop writes one; up to 16 MiB) and counts every frame by protocol: IPsec's ESP, ESP inside UDP 4500 (NAT traversal) and IKE (UDP 500, or 4500 behind the non-ESP marker), and in the clear GRE, ICMP, OSPF, EIGRP, BGP, HSRP, VRRP, PIM, TCP, UDP, ARP and frames carrying no IP (CDP, keepalives). Each TCP connection is also read from its own bytes, both ways in sequence order, and never judged by its port: ssh (an SSH-2.0 banner, its key exchange, then everything after NEWKEYS), tls (a ClientHello or ServerHello record, then each record by its content type), http (an HTTP/1.x request or status line in the clear), http2 (HTTP/2's client preface in the clear) and grpc (http2 whose request says application/grpc). Each IP packet is one of three kinds: encrypted (ESP, ESP in UDP, SSH after NEWKEYS, TLS application data), the key exchange that set it up (IKE, SSH's banner and key exchange, TLS handshake records), or cleartext (everything else carried over IP, which anyone on the wire can read). A packet with no data in an encrypted connection (a SYN, a bare ACK) is counted encrypted. The same counts are given for each address pair, most packets first, so \"the sites' traffic crosses encrypted\" is a pair whose cleartext is 0 and whose encrypted count grew while traffic was sent. Whether it crossed inside IPsec is the pair's protocols.esp (and esp-udp), not encrypted, which counts SSH and TLS too; GRE in the clear between two sites is DMVPN without its IPsec. Every IP packet is also counted by its DSCP mark (dscp, over the capture and in each pair and connection), named by its codepoint (EF, AF41, CS6) and by the RFC 4594 service class that codepoint is recommended for (Telephony, Multimedia Conferencing, Network Control). A mark is what the sender or a router on the way wrote, not proof of how any queue treated the packet, and a network that uses a codepoint for another class (call signaling on CS3, where RFC 4594 gives CS3 to broadcast video) still reads RFC 4594's name. The first and last packet times and the seconds between them, over the capture and for each connection, come from the capture's own records; a pcapng simple packet block has no time. UDP is counted by pair and mark, never as connections. HTTP/1.x, HTTP/2 and gRPC in the clear to a management endpoint are cleartext management: listed first in flows, and a run warns about them. A server is a management endpoint when a request on the connection named a management API by its path (RESTCONF's /restconf or /.well-known/host-meta, gNMI's /gnmi.gNMI/, gNOI's /gnoi.), said in the connection's management, or when the management parameter lists it. A port never decides, so a lab's web server on 8080 is cleartext and not management, and a device's own web interface is management once management lists the device. Every HTTP/1.x and HTTP/2 connection's headers, management or not, are searched by name (HTTP/2's decoded from HPACK, which compresses header values, so a search of the bytes finds nothing), and a header that carries a credential (authorization, username, password, a token, a cookie, or one its sender marked never to be indexed) is reported by its name and its connection, never its value, in cleartext_credentials, flows and a warning. Credentials are searched in HTTP/1.x and HTTP/2 headers only: Telnet, FTP, SNMP communities, URL query strings and bodies are not searched, so cleartext_credentials 0 means no credential header was found, not that no credential crossed, and Telnet, FTP and SNMP are not counted in cleartext_management. A header that could not be searched (a block over 64 KiB or one that would not decode, a name over 256 bytes, headers after a gap or a protocol switch, a credential name past the 32 kept for a connection) is counted in headers_unread. Bytes framed as encrypted that read as text are not ciphertext: a TLS application data record, or a 256-byte block of what SSH sent after NEWKEYS, of at least 64 bytes of which three quarters are printable ASCII (random bytes are about 38% printable) makes that direction's packets cleartext and flags the connection cleartext_in_encrypted, which is how a TLS NULL cipher suite, the SSH none cipher and records forged around cleartext are told. Nothing is called encrypted that was not seen to be: a connection whose start the capture lacks, one whose bytes stop making sense as SSH or TLS, the bytes after a lost segment that hid a record's or packet's header (TLS is read again from the next segment that begins with a record), a packet whose data the capture holds none of (a short snapshot length), and a repeated segment whose bytes differ from the first copy are cleartext. UDP is read no further than its ports, so DTLS and QUIC are cleartext UDP. It reads Ethernet (VLAN tags included), raw IP and Linux cooked captures; a frame of another link type is counted as unread. A frame carrying no IP (CDP, a keepalive, but also anything inside MPLS or PPPoE) and an unread frame are not examined, so they are counted apart as unexamined, never as cleartext: \"nothing crossed in the clear\" is cleartext 0 and unexamined 0 (or every unexamined frame accounted for by its protocol). IKE and ESP in UDP are told by their ports, and ESP is counted encrypted whatever its cipher, ESP-NULL included, since its payload is not read. A file cut short is read up to the cut, and the task fails naming where. It changes nothing, and a check runs it as a run does. It runs on this machine with no device: write it without hosts.", Params: []collection.Param{{Name: "src", Type: "string", Required: true, Description: "The capture file, a path relative to the project directory, such as captures/core1-wan.pcap.", Format: collection.ParamFormatProjectPath}, {Name: "pairs", Type: "int", Default: "20", Description: "How many address pairs to list, most packets first; 0 lists none."}, {Name: "flows", Type: "int", Default: "20", Description: "How many TCP connections named from their bytes to list in flows, those in the clear first; 0 lists none."}, {Name: "management", Type: "list of string", Description: "The management endpoints the capture may reach, each an address (every port on it) or an address and port (192.0.2.1:80, [2001:db8::1]:443): an HTTP/1.x, HTTP/2 or gRPC connection to one in the clear is cleartext management whatever it asked. RESTCONF, gNMI and gNOI are found by their paths without it."}}, Returns: []collection.ReturnField{{Name: "format", Type: "string", Returned: "always", Description: "pcap or pcapng."}, {Name: "packets", Type: "int", Returned: "always", Description: "Every frame in the capture."}, {Name: "bytes", Type: "int", Returned: "always", Description: "Their lengths on the wire, summed."}, {Name: "protocols", Type: "dict", Returned: "always", Description: "Packets and bytes by protocol name (esp, esp-udp, ike, gre, icmp, ospf, bgp, ssh, tls, http, http2, grpc, tcp, udp, arp, non-ip and the rest). Whether traffic crossed inside IPsec is esp and esp-udp here."}, {Name: "encrypted", Type: "int", Returned: "always", Description: "IP packets that were ESP, ESP in UDP, SSH after NEWKEYS or TLS application data, and those carrying no data in an encrypted connection. It is not the IPsec count: that is protocols.esp."}, {Name: "key_exchange", Type: "int", Returned: "always", Description: "IP packets that were IKE, SSH's banner and key exchange, or TLS handshake records."}, {Name: "cleartext", Type: "int", Returned: "always", Description: "Every other IP packet: what a reader of the wire saw in the clear."}, {Name: "pairs", Type: "list of dict", Returned: "always", Description: "Each address pair (a, b), most packets first: its protocols, encrypted, key_exchange, cleartext and dscp."}, {Name: "flows", Type: "list of dict", Returned: "always", Description: "Each TCP connection named from its bytes (ssh, tls, http, http2, grpc), those that carried a credential in the clear first, then those whose encrypted-looking bytes read as text, then other cleartext management, then most packets first: client and server (address:port; the client sent the first SYN), protocol, packets, bytes (the frames' lengths on the wire, both ways), dscp (both ways), first and last (RFC 3339 times, left out when the capture holds none), duration_seconds, encrypted, key_exchange, cleartext, cleartext_management, management (restconf, gnmi, gnoi or listed, when the server is a management endpoint), cleartext_in_encrypted, cleartext_credentials (the names of the headers only, at most 32) and headers_unread."}, {Name: "cleartext_credentials", Type: "int", Returned: "always", Description: "HTTP/1.x and HTTP/2 headers that carried a credential in the clear, one for each header name on each connection; flows names them. Credentials are searched in HTTP/1.x and HTTP/2 headers only: Telnet, FTP, SNMP communities, URL query strings and bodies are not searched, so 0 is not proof that none crossed."}, {Name: "cleartext_management", Type: "int", Returned: "always", Description: "Connections named from their bytes as http, http2 or grpc that crossed in the clear to a management endpoint (see the description). Telnet, FTP and SNMP are not named, and are not counted here."}, {Name: "cleartext_in_encrypted", Type: "int", Returned: "always", Description: "Connections whose bytes framed as encrypted (TLS application data, SSH after NEWKEYS) read as text: a NULL cipher suite, the SSH none cipher, or records forged around cleartext. Their packets are counted cleartext."}, {Name: "headers_unread", Type: "int", Returned: "always", Description: "Headers or header blocks that could not be searched for a credential: a block over 64 KiB or one that would not decode (and every block after it), a name over 256 bytes, headers after a gap or a protocol switch, and a credential name past the 32 kept for a connection. When it is not 0, cleartext_credentials may be short."}, {Name: "warnings", Type: "list of string", Returned: "when a credential, text framed as encrypted, management traffic, or headers that could not all be searched crossed in the clear", Description: "One line for each connection that did, naming its headers but never a value and saying when some of its headers could not be searched, at most ten and then a count of the rest."}, {Name: "link_types", Type: "list of string", Returned: "always", Description: "The link types the capture declares."}, {Name: "unread", Type: "int", Returned: "always", Description: "Frames of a link type this method does not read."}, {Name: "unexamined", Type: "int", Returned: "always", Description: "Frames this method could not judge encrypted or not: unread ones, and every one carrying no IP (non-ip)."}, {Name: "dscp", Type: "list of dict", Returned: "always", Description: "Every IP packet by DSCP mark, most bytes first: dscp (0 to 63), name (its codepoint, such as EF or AF41, empty for a value no standard names), class (its RFC 4594 service class, empty when RFC 4594 gives it none), packets and bytes."}, {Name: "first", Type: "string", Returned: "when the capture holds a packet time", Description: "The first packet's time, RFC 3339 in UTC."}, {Name: "last", Type: "string", Returned: "when the capture holds a packet time", Description: "The last packet's time."}, {Name: "duration_seconds", Type: "float", Returned: "always", Description: "The seconds from first to last, 0 when the capture holds no time."}}, Examples: []collection.Example{{Name: "Count what crossed a WAN link", RunbookYAML: "- name: Count what crossed CORE1's WAN link\n  net.pcap.summary:\n    src: captures/core1-wan.pcap\n  register: wan\n"}}, SeeAlso: []string{"virt.cml.capture.start", "virt.cml.capture.stop"}},
	},
	{
		Name:          "net.ios.parse",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh", "telnet", "console"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Runs a show command on a Cisco IOS or IOS XE device and returns its output parsed into Genie's schema.",
			Description: "Runs one show command over the same session net.ios.command opens and parses its output with a parser ported from Cisco's Genie (genieparser, Apache License 2.0), returning the same nested structure Genie returns for that command, so Genie's own schema documentation describes the result and a later task can compare two reads field by field, such as the state before a change and after its rollback. Each parser is held to every one of genieparser's own recorded device outputs and expected results for the command forms it covers. Output in which the parser finds nothing (no OSPF neighbors yet) is an empty result with empty true, not a failure, so a before and after can compare it; a command the device refuses (% BGP not active) fails the task. A command no parser covers is refused before the device is reached, naming the commands that are covered. It only reads, so it reports unchanged, and a check runs it as a real run does.",
			Params: []collection.Param{
				// The forms come from the method's own parser table
				// (iosparse.Commands), as the registered doc's do.
				{Name: "command", Type: "string", Required: true, Description: "The show command to run and parse, in one of the forms a parser covers, each {argument} standing for a value of your own: " + strings.Join(iosparse.Commands(), ", ") + ". For example \"show ip interface brief\" or \"show ip route vrf MGMT\". IOS's abbreviations are not accepted: write the command in full."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
			},
			Returns: []collection.ReturnField{
				{Name: "parsed", Type: "dict", Returned: "always", Description: "The command's output in Genie's schema for it: for show ip interface brief, interface, keyed by interface name, each with ip_address, interface_is_ok, method, status and protocol."},
				{Name: "parser", Type: "string", Returned: "always", Description: "The Genie parser class whose port parsed it, such as ShowIpInterfaceBrief."},
				{Name: "empty", Type: "bool", Returned: "always", Description: "Whether the parser found nothing in the output, parsed then being empty."},
				{Name: "stdout", Type: "string", Returned: "always", Description: "The command's output as the device printed it, except that the secrets in show archive config differences (a password, secret, key or SNMP community) are masked, as net.ios.replace masks them, before it is parsed or recorded."},
			},
			Examples: []collection.Example{
				{
					Name:        "Read the interfaces before a change, to compare after its rollback",
					RunbookYAML: "- name: Read the interfaces\n  net.ios.parse:\n    command: show ip interface brief\n  register: interfaces_before\n",
				},
			},
			SeeAlso: []string{"net.ios.command", "net.ios.facts", "net.ios.replace"},
		},
	},
	{
		Name:          "net.junos.config",
		Capabilities:  []capability.Name{capability.NameJunos},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Applies configuration to a Juniper Junos device."},
	},
	{
		Name:          "net.eos.config",
		Capabilities:  []capability.Name{capability.NameAristaEOS},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Applies configuration to an Arista EOS device."},
	},
	// The net.catalyst.* namespace is controller-side and read-only: each
	// method addresses a Cisco Catalyst Center over its REST API and
	// gathers facts about the fleet it manages, changing nothing. They are
	// the first methods in this catalog to reach status implemented, having
	// been verified against Cisco's public DevNet sandbox.
	{
		Name:          "net.catalyst.device_facts",
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
		Capabilities:  []capability.Name{capability.NameCatalystAPI},
		Transports:    []string{"https"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Gathers every device a Cisco Catalyst Center manages, as facts.",
			Description: "Pages through the Catalyst Center's device inventory and emits one fact entry per device: identity, platform, software, role, and reachability/collection status. Never reports changed: reading an inventory does not alter it.",
			Fragments:   []string{"catalyst_connection", "catalyst_pagination"},
			Returns: []collection.ReturnField{
				{Name: "catalyst.devices", Type: "list of map", Returned: "always", Description: "One entry per device: id, hostname, management_ip, family, series, platform_id, software_type, software_version, role, serial_number, reachability_status, collection_status, mac_address, type, up_time (as the controller words it) and description, each empty when the controller leaves it unset."},
				{Name: "catalyst.device_count", Type: "int", Returned: "always", Description: "The number of devices in the catalyst.devices fact."},
			},
			Examples: []collection.Example{
				{Name: "Gather the managed fleet", RunbookYAML: "- name: Gather Catalyst Center device facts\n  net.catalyst.device_facts:\n  register: fleet\n"},
			},
			SeeAlso: []string{"net.catalyst.reachability", "net.catalyst.site_facts"},
		},
	},
	{
		Name:          "net.catalyst.site_facts",
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
		Capabilities:  []capability.Name{capability.NameCatalystAPI},
		Transports:    []string{"https"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Gathers a Cisco Catalyst Center's site hierarchy, as facts.",
			Description: "Reads every site the targeted Catalyst Center manages. Emits the full site_name_hierarchy for each site, not just its bare name, since two sites in different regions can share a bare name.",
			Fragments:   []string{"catalyst_connection"},
			Returns: []collection.ReturnField{
				{Name: "catalyst.sites", Type: "list of map", Returned: "always", Description: "One entry per site: id, name, site_name_hierarchy."},
				{Name: "catalyst.site_count", Type: "int", Returned: "always", Description: "The number of sites in the catalyst.sites fact."},
			},
			Examples: []collection.Example{
				{Name: "Gather the site hierarchy", RunbookYAML: "- name: Gather Catalyst Center site facts\n  net.catalyst.site_facts:\n  register: sites\n"},
			},
			SeeAlso: []string{"net.catalyst.device_facts"},
		},
	},
	{
		Name:          "net.catalyst.tag_facts",
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
		Capabilities:  []capability.Name{capability.NameCatalystAPI},
		Transports:    []string{"https"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Gathers the tags defined on a Cisco Catalyst Center, as facts.",
			Description: "Reads every tag the targeted Catalyst Center knows about, and separates the controller's own system tags (its internal bookkeeping) from operator-created ones, so a runbook grouping devices by operator intent does not have to filter system tags out itself.",
			Fragments:   []string{"catalyst_connection"},
			Returns: []collection.ReturnField{
				{Name: "catalyst.tags", Type: "list of map", Returned: "always", Description: "One entry per tag: id, name, system_tag."},
				{Name: "catalyst.operator_tags", Type: "list of string", Returned: "always", Description: "Names of every tag not created by the controller itself."},
			},
			Examples: []collection.Example{
				{Name: "Gather operator-created tags", RunbookYAML: "- name: Gather Catalyst Center tag facts\n  net.catalyst.tag_facts:\n  register: tags\n"},
			},
		},
	},
	{
		Name:          "net.catalyst.reachability",
		Site:          collection.SiteController,
		Device:        collection.DeviceRequired,
		Capabilities:  []capability.Name{capability.NameCatalystAPI},
		Transports:    []string{"https"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Reports which devices a Cisco Catalyst Center can currently reach and manage.",
			Description: "Answers a different question than device_facts: device_facts describes what the fleet is, gathered once; reachability describes what the fleet is doing right now, the check a gate task waits on before acting. Reports reachability_status (can the controller talk to the device at all) and collection_status (is it successfully collecting from it) separately, since a device can be reachable and still not managed.",
			Fragments:   []string{"catalyst_connection", "catalyst_pagination"},
			Returns: []collection.ReturnField{
				{Name: "catalyst.reachability", Type: "list of map", Returned: "always", Description: "One entry per device: hostname, management_ip, reachability_status, collection_status, reachable."},
				{Name: "catalyst.unreachable", Type: "list of string", Returned: "always", Description: "Hostnames (or management IPs, if hostname is unset) of every unreachable device."},
				{Name: "catalyst.reachable_count", Type: "int", Returned: "always", Description: "Count of devices with reachability_status Reachable."},
				{Name: "catalyst.managed_count", Type: "int", Returned: "always", Description: "Count of devices with collection_status Managed."},
				{Name: "catalyst.total_device_count", Type: "int", Returned: "always", Description: "How many devices the controller listed, reachable or not."},
			},
			Examples: []collection.Example{
				{Name: "Gate a change on full fleet reachability", RunbookYAML: "- name: Confirm the fleet is reachable before changing anything\n  net.catalyst.reachability:\n  register: health\n\n- name: Apply the change\n  net.ios.config:\n    lines:\n      - ntp server 192.0.2.10\n  when_cel: \"stat.health['']['catalyst.unreachable'].size() == 0\"\n"},
			},
			SeeAlso: []string{"net.catalyst.device_facts"},
		},
	},
}

// restconfFactsDescription is net.restconf.facts' description, which the
// method's own registration carries word for word.
const restconfFactsDescription = "Finds the device's RESTCONF root from its /.well-known/host-meta (RFC 8040 section 3.1), read with no credential; a host-meta answered 404 or a redirect, which is never followed (IOS XE answers it to anything but a valid login with a 302 to its WebUI's login page, which answers 404, measured on 17.15.1a, 2026-11-15), leaves RFC 8040's conventional /restconf, which the first authenticated read under it confirms (and restconf.root_source says which way the root was learned), while a 404 there too refuses the device. Then it reads the capabilities ietf-restconf-monitoring lists and the device's YANG library (RFC 8525's yang-library, or RFC 7895's modules-state from a device that does not serve it), and records them, changing nothing: the same yang.* facts net.netconf.facts records over NETCONF, and RESTCONF's own root, capabilities and datastores. The yang.* facts are protocol-neutral: each protocol's library also lists its own modules (measured on a Catalyst 8000V running IOS XE 17.15.1a, NETCONF's ietf-netconf, ietf-netconf-with-defaults and tailf-netconf-extensions, RESTCONF's tailf-rest-query), which yang.* leaves out and restconf.modules (or netconf.modules over NETCONF) records, so one device read over either protocol gives the same yang.modules, yang.module_count and yang.modules_digest. The device is reached at its own origin with its own TLS (always verified, a pinned certificate included) and its own stored credential, following no redirect, and a root its host-meta names on another scheme, host or port is refused. A read whose capabilities and library both failed fails the task rather than recording a device with nothing, as onboarding refuses it; a family whose own read failed is left out, with a warning. A device reached over http:// records its findings as stats only, never as facts, since nothing proves which server answered. A read the device refuses with 409 Conflict, in-use, \"Too many sessions.\" (IOS XE counts every RESTCONF request as a session until it expires) is sent again after a wait that starts at 2 seconds and doubles to at most 30, for up to 11 minutes a request and 15 minutes in all per task; a read given up on fails the task, never recorded as a capability or library the device lacks, and the task warns how many times requests were refused and how long it waited. Reports no change, and a check runs it for real."

// restconfFactsReturns documents net.restconf.facts' facts.
func restconfFactsReturns() []collection.ReturnField {
	const when = "on every read, as a stat; as a fact only when the device was reached over https"
	const whenLibrary = "when the YANG library was read, as a stat; as a fact only when the device was reached over https"
	return append([]collection.ReturnField{
		{Name: "restconf.root", Type: "string", Returned: when, Description: "The RESTCONF root, as an escaped path (such as /restconf), or / for a root at the device's origin itself: the one the device's /.well-known/host-meta names, or the conventional /restconf when host-meta answered 404 or a redirect and a read under it answered (restconf.root_source says which)."},
		{Name: "restconf.root_source", Type: "string", Returned: when, Description: "How restconf.root was learned: host-meta (its restconf link) or default (host-meta answered 404 or a redirect, as IOS XE answers it to anything but a valid login with a 302 to its WebUI's login page (404 there), and a read under the conventional /restconf answered)."},
		{Name: "restconf.capabilities", Type: "list of string", Returned: "when the capability list was read and fits, as a stat; as a fact only when the device was reached over https", Description: "Every capability URI the device's ietf-restconf-monitoring lists (RFC 8040 section 9.1), with its parameters, sorted. Left out, with a warning, when the list could not be read or does not fit in 60 KiB, so an unread list never reads as an empty one; an entry that is not printable is left out whole."},
		{Name: "restconf.datastores", Type: "list of string", Returned: whenLibrary, Description: "The datastores the device's YANG library lists, sorted: an NMDA device's running, intended and operational, which RFC 8527 addresses under the root's /ds/; empty for a library that lists none, as RFC 7895's modules-state does."},
		{Name: "restconf.modules", Type: "list of string", Returned: whenLibrary, Description: yangfacts.OwnModulesDescription("RESTCONF")},
	}, append(yangfacts.Returns(whenLibrary, yangfacts.RestconfSource), tlsfacts.Returns()...)...)
}
