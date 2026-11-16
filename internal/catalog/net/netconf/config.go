// Package netconf implements "net.netconf.config": vendor-neutral
// configuration over RFC 6241 NETCONF, built on pkg/netconf's client and
// reached over pkg/remoteexec's SSH subsystem channel.
//
// It is the structured-data sibling of internal/catalog/net/cli's
// "net.cli.*" and internal/catalog/net/ios's "net.ios.config". Those
// drive an interactive terminal and send lines a human would type; this
// one sends an XML document against a named datastore and gets a
// machine-readable answer, including which element the device rejected
// and why. Both reach the same Cisco device; which is appropriate is a
// question about the change, not about the platform.
//
// This file registers the method and documents it. The run is in
// config_run.go, its undo in config_undo.go and config_undo_record.go, and
// its check through the candidate datastore in config_check.go.
package netconf

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameter names come from ansible.netcommon.netconf_config rather than
// being coined here, matching this project's standing rule that a
// runbook feature reuses Ansible's vocabulary instead of inventing a new
// one. content, default_operation, error_option, lock, commit and backup
// are all that module's own names, with the same meanings. datastore is
// that module's own alias for its target parameter: target itself is the
// engine's device selector (collection.TargetParam), which no method may
// declare, so the alias is the name used here. candidate_check is this
// method's own: that module has no check through the candidate.
const (
	paramContent          = "content"
	paramDatastore        = "datastore"
	paramDefaultOperation = "default_operation"
	paramErrorOption      = "error_option"
	paramLock             = "lock"
	paramCommit           = "commit"
	paramBackup           = "backup"
	paramCandidateCheck   = "candidate_check"
)

// statBackup is the stat name backup: true records the pre-change
// configuration under.
const statBackup = "backup"

// The three values the lock parameter accepts, matching
// ansible.netcommon.netconf_config's own.
const (
	lockNever       = "never"
	lockAlways      = "always"
	lockIfSupported = "if_supported"
)

// reversibilityNotes is what net.netconf.config's undo is, and is not.
const reversibilityNotes = "A run that changed the configuration records net.netconf.config with default_operation none and, as content, the undo of what it did, where the reads before and after the edit prove one exact. " +
	"An element the read before holds no instance of was created, and its undo removes it (named by its own leaves, where a list entry's keys are). An element the read names for certain (a container, which carries no leaf; or the one instance agreeing with the content on every leading leaf it names, where RFC 7950 puts a list entry's keys) is recurred into, and where a value of it changes, or a part of it cannot be named, it is put back whole, nc:operation replace, as it was read. " +
	"That undo is decided from the read before, so it is recorded before the edit is sent: an edit whose answer is lost still leaves it, and it stands if the task fails, since running it where nothing changed changes nothing. " +
	"A list entry the read cannot name (one whose leading leaves are what the edit changes, directly under containers) is decided from the instances of its name before and after the edit: one new is removed, one changed into one other is put back; that undo can be recorded only once the read after answers, so a lost answer leaves none, and the run says so. " +
	"No undo is recorded, and the run says why, for content naming a secret (so no secret reaches the run's journal); for default_operation replace, which replaces the whole datastore; for a put-back from a device that does not read in with-defaults explicit mode; for an undo longer than the 256 bytes on one line the run's journal keeps of a value, which most put-backs of a real device's configuration are; for one holding anything masking would hide (a secret-named leaf, a crypt-shaped value, the run's own credential); for a value directly under containers, which no read tells a leaf from a leaf-list for; and for a change the reads cannot attribute. " +
	"A put-back holds configuration read from the device, which the run's journal keeps: masking decides it holds no secret, so a secret in a leaf whose name says nothing of one would be kept. A node the account's access control hides from its reads reads as absent: an undo recorded before an edit whose answer is lost would then remove it, and when both reads hide it they agree and the undo is withdrawn. A presence container the edit creates above what its undo removes is left in place. The reads count what another client changes at the same moment as this task's. A run that changed nothing records nothing."

// configDescription is the method's description.
const configDescription = "Opens an RFC 6241 NETCONF session over the SSH \"netconf\" subsystem and applies content to the chosen datastore with edit-config. Parameter names are ansible.netcommon.netconf_config's own, with its target parameter under its own alias, datastore, since target names the device a task runs on. " +
	"The session negotiates RFC 6242 chunked framing whenever the device offers base:1.1, and requests rollback-on-error whenever the device advertises it, so a rejected document leaves the device unchanged rather than half configured; that matters most on a device offering only writable-running, which is what Cisco IOS XE offers until its candidate datastore is configured, because such a device has no staging area and every element lands on the live configuration as it is applied. " +
	"A datastore the device never advertised support for is refused when the session opens rather than at the first write, naming the missing capability. Unlike the net.cli.* and net.ios.config methods, a rejected element comes back as a structured error carrying the device's own error-tag and the XPath of the element it objected to. " +
	"Whether the task changed anything is read, not assumed: the configuration the edit can touch is read with get-config before and after the edit (running, or startup for a write to startup), through a subtree filter built from the content that selects every instance of each element's name by name alone, never narrowed by a value, in RFC 6243 with-defaults explicit mode where the device offers it and otherwise in its own basic mode; the two reads are compared in document order with namespaces resolved and white space between elements set aside, so a re-run that leaves the configuration as it found it reports no change. A default_operation of replace reads the whole datastore. " +
	"A candidate edit with commit false is validated and discarded and reports no change. A run that changes the configuration records its undo where the reads prove one exact, and says why when it records none. A check runs only with candidate_check: true, through the candidate datastore."

// contentDescription is the content parameter's description.
const contentDescription = "The configuration document to apply, as the XML that goes inside edit-config's <config> element: well-formed, holding no DOCTYPE, nesting no deeper than 256 elements, and holding no masked placeholder (pleiades-masked-), each refused before a session opens. " +
	"Per-element operations are expressed the standard way, with an nc:operation attribute; pair that with default_operation: none so the device changes only what the document explicitly names. " +
	"A leaf may name a stored secret, such as an IKE pre-shared key, with a secret reference, {{ secret.<name>.<field> }} (pleiades add-secret): it is resolved just before the session opens, escaped as XML text, and masked in everything the run reports; the content is never recorded, and a call whose content names a secret records no undo. " +
	"A document holding one holds no other template expression, so a per-device value and a secret go in separate tasks, and a value with whitespace, a control character or ? is refused."

// candidateCheckDescription is the candidate_check parameter's description.
const candidateCheckDescription = "In a check (pleiades run --mode check, or check_mode: true), check this task through the device's candidate datastore rather than report it unchecked. " +
	"The check takes the candidate's lock first (a lock the device refuses, as it does while the candidate holds changes not committed, reports the task unchecked with nothing sent), proves the candidate holds nothing uncommitted in what the content touches, stages the edit there, has the device validate it, reads it, and discards it, proving the candidate reads as it did before; it holds the lock for at most 30 seconds, after which the session is closed. " +
	"Only a check's own RPCs can be sent (lock, unlock, edit-config and validate of the candidate, discard-changes, reads and close-session): a commit, or a write to running or startup, is refused before it is written. " +
	"Its result says validated; commit not exercised, since a device may refuse at commit what it validated, and changed says whether the edit changed the candidate. " +
	"It runs only from the command line, never on a simulate-locked device, never for a write to startup or content naming a secret, only when this session's own hello advertises :candidate and :validate, and only on a generic_netconf device whose server is Netopeer2 over sysrepo, the one server measured to refuse the lock on a candidate holding another session's changes, to leave the candidate as running when a session is killed after its edit, and to restore it on a discard. " +
	"Any failure between sending the edit and proving the discard fails the check as candidate state unverified. A real run ignores it."

func init() {
	doc := configDoc()
	collection.MustRegister(collection.Descriptor{
		Name: configFQCN,
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"netconf",
			},
			RequiredCapabilities: []capability.Name{
				capability.NameNetconf,
				// SSHTransportCapable is declared alongside it, not
				// implied by it. NETCONF is carried over an SSH
				// subsystem channel, so sdk.Connect asserts this
				// interface at run time regardless; declaring it moves
				// that refusal to plan time, where "pleiades validate"
				// can report it before anything dials.
				capability.NameSSHTransport,
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			NoBecomeReason:  "it speaks NETCONF over an SSH subsystem, which no sudo prefix reaches, so it would run as the login",
			SupportsCheck:   true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      reversibilityNotes,
				Inverses: []sdk.InverseSpec{{
					FQCN:             configFQCN,
					Record:           []string{paramContent, paramDatastore, paramDefaultOperation},
					MayKeepOnFailure: true,
				}},
			},
			Doc:   doc,
			Facts: collection.DeclareFacts(doc.Returns, discoveryFactKeys, factContentID),
		},
		Invoke:    Config,
		Check:     ConfigCheck,
		CheckCall: configCheckCall,
		UndoCall:  configUndoCall,
	})
}

// configDoc is net.netconf.config's documentation, word for word its
// catalog entry's.
func configDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Applies a configuration document to a device over NETCONF, reporting whether it changed, with its undo where exact, an optional pre-change backup, and a gated check through the candidate datastore.",
		Description: configDescription,
		Params: []collection.Param{
			{Name: paramContent, Type: "string", Required: true, Description: contentDescription, SecretRefs: true},
			{Name: paramDatastore, Type: "string", Choices: []string{"running", "candidate", "startup"}, ChoicesIgnoreCase: true, Description: "The datastore to configure: running, candidate or startup. Left unset it is chosen when the session opens, from what the device's hello advertises that session: running when it offers :writable-running, otherwise candidate (locked, validated when the device offers :validate, then committed). That is chosen afresh on every task, since a device's offer changes with its configuration: Cisco IOS XE offers only running until netconf-yang feature candidate-datastore is configured, and then only candidate. A datastore the device does not advertise is refused before anything is applied."},
			{Name: paramDefaultOperation, Type: "string", Default: "merge", Choices: []string{"merge", "replace", "none"}, Description: "What the device does with elements carrying no explicit operation attribute: merge, replace or none. RFC 6241 defines no \"delete\" here; express a delete with an nc:operation attribute in content."},
			{Name: paramErrorOption, Type: "string", Choices: []string{"stop-on-error", "continue-on-error", "rollback-on-error"}, Description: "How the device handles a rejected element: stop-on-error, continue-on-error or rollback-on-error. Left unset this method asks for rollback-on-error whenever the device advertises the capability, and otherwise leaves the device its own default, stop-on-error, because stop-on-error leaves a rejected document half applied."},
			{Name: paramLock, Type: "string", Default: "never", Choices: []string{"never", "always", "if_supported"}, ChoicesIgnoreCase: true, Description: "Whether to lock the datastore for the duration: never, always, or if_supported. Locking prevents another client changing the datastore mid-edit; it also blocks every other client, which matters on a shared device. The candidate datastore is always locked, and never is refused there: the lock is refused while another client's changes sit in the candidate, so this task commits and discards only its own edit."},
			{Name: paramCommit, Type: "bool", Default: "true", Description: "On the candidate datastore, commit after the edit, which is validated first when the device offers :validate. false validates the edit and discards it, so it is checked by the device and applied nowhere, and reports no change, with validated and applied saying what ran; unlike ansible.netcommon.netconf_config it never leaves the edit staged, where the next commit by any client would apply it. false is refused when the session writes running, where an edit is live at once."},
			{Name: paramCandidateCheck, Type: "bool", Default: "false", Description: candidateCheckDescription},
			{Name: paramBackup, Type: "bool", Default: "false", Description: "Capture the running datastore's full contents with get-config before applying anything, whichever datastore the task writes, recorded under the backup stat with every secret masked. A configuration masking cannot vouch for fails the task before anything is applied."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: append([]collection.ReturnField{
			{Name: statValidated, Type: "bool", Returned: "when commit is false", Description: "Whether the device validated the candidate edit before it was discarded: false on a device that does not offer :validate, where only the edit itself was accepted."},
			{Name: statApplied, Type: "bool", Returned: "when commit is false", Description: "Always false: the edit was discarded, not applied."},
			{Name: statCandidateCheck, Type: "string", Returned: "in a check through the candidate datastore", Description: "What the check proved: validated; commit not exercised. The device validated the edit in its locked candidate datastore, the edit was discarded, and the candidate was proven to read as it did before; a device may still refuse at commit what it validated."},
			{Name: statBackup, Type: "string", Returned: "when backup is true", Description: "The datastore's full contents as XML, captured immediately before this task's own document was applied, with every secret masked: the text of each leaf whose name holds a secret (password, secret, a key such as auth-key or pre-shared-key, encrypted-password, RFC 8177's key-string, keystring and hexadecimal-string, OpenConfig's key-clear-text, an SNMP community), of every other leaf inside such an element at any depth but its encryption type, id, algorithm or lifetime (and, two or more levels down outside SNMP, a name or address), and of each crypt-shaped value ($1$, $6$, $9$) is replaced by pleiades-masked- and sixteen hexadecimal digits, equal secrets alike within one run; the run's own credential is masked wherever it appears, other leaves are read by the configuration text masker net.ios.config's backup uses, and comments are withheld. Every element, name, address and encryption type stays where it was. A document that is not well-formed XML, holds a DOCTYPE, nests past 256 elements, has an attribute that may hold a secret, or has a line the text masker cannot vouch for fails the task before anything is applied, and none of it is recorded. net.netconf.config refuses content holding a placeholder, so this text cannot be applied back as it is."},
		}, discoveryReturns()...),
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
	}
}
