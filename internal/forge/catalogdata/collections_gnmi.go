// This file holds the gNMI methods' catalog entries: net.gnmi.facts,
// net.gnmi.config and wait.gnmi, each word for word its registered
// documentation (internal/archtest's TestCatalogDataDocsMatchTheRegistry).
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// gnmiCollections are the gNMI methods.
var gnmiCollections = []collectionscaffold.Config{
	// net.gnmi.facts is gNMI's read of what a device's server says it supports:
	// its version, encodings and models, beside the protocol-neutral yang.*
	// facts, never complete (gNMI's Capabilities is not a YANG library).
	{
		Name:          "net.gnmi.facts",
		Capabilities:  []capability.Name{capability.NameGNMI},
		Transports:    []string{"gnmi"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Records what a device's gNMI server says about itself: its gNMI version, encodings and models.",
			Description: "Asks the device's gNMI server its Capabilities (gNMI specification section 3.2) and records what it says, changing nothing: the gNMI version, the encodings it answers in and the models it implements, in the gnmi.* family, beside the protocol-neutral yang.* facts net.netconf.facts and net.restconf.facts record from a YANG library. gNMI's Capabilities is not a YANG library (no namespace, and a model's own version in place of a revision when it is no date), so yang.completeness is always partial and no yang.modules_digest is recorded; gnmi.models_digest is the model list's own. The device is reached at its own target with its own TLS (always verified, a pinned certificate included, mutual TLS from the stored certificate) and its own stored credential, as the gNMI specification's username and password metadata, a bearer token, or none; a refused login is not sent again within the task. A device reached over plaintext gRPC (grpc_plaintext) records its findings as stats only, never as facts, since nothing proves which server answered, and every run says so. Reports no change, and a check runs it for real.",
			Returns: []collection.ReturnField{
				{Name: "gnmi.version", Type: "string", Returned: "on every read, as a stat; as a fact only when the device was reached over TLS", Description: "The gNMI version the server says it implements, such as 0.7.0; empty when it names none."},
				{Name: "gnmi.encodings", Type: "list of string", Returned: "on every read, as a stat; as a fact only when the device was reached over TLS", Description: "The encodings the server says it answers in, by gNMI's names lowercased (json, bytes, proto, ascii, json_ietf), in gNMI's own order. A server that answers PROTO alone (Stratum, measured 2026-11-15) refuses a Get in JSON."},
				{Name: "gnmi.models", Type: "list of string", Returned: "on every read, as a stat; as a fact only when the device was reached over TLS, when the list fits in 60 KiB", Description: "Every model the server lists, each name@version (name alone for a model with no version), as the server wrote them, sorted; an entry that is not printable or is too long is left out, with a warning."},
				{Name: "gnmi.model_count", Type: "int", Returned: "on every read, as a stat; as a fact only when the device was reached over TLS", Description: "How many models the server lists, as gnmi.models counts them."},
				{Name: "gnmi.models_digest", Type: "string", Returned: "on every read, as a stat; as a fact only when the device was reached over TLS", Description: "The SHA-256 (hex) of the model list, each model's name, organization and version, sorted, so any change to what the server says it implements reads as a change of this one value. It is not yang.modules_digest, which only a YANG library gives."},
				{Name: "yang.modules", Type: "list of string", Returned: "on every read, as a stat; as a fact only when the device was reached over TLS, when the list fits in 60 KiB", Description: "Every YANG module the device's gNMI Capabilities lists a model for, each name@revision, or name alone where the model's version is not a revision date (an OpenConfig model's semantic version, such as 2.4.1, is not one), sorted. A model named by a URN ending in :module is read as that module. gNMI's Capabilities is not a YANG library, so this list is never complete: yang.completeness is always partial. The same key net.netconf.facts and net.restconf.facts record from the device's YANG library."},
				{Name: "yang.module_count", Type: "int", Returned: "on every read, as a stat; as a fact only when the device was reached over TLS", Description: "How many modules yang.modules lists."},
				{Name: "yang.completeness", Type: "string", Returned: "on every read, as a stat; as a fact only when the device was reached over TLS", Description: "Always partial for a gNMI read: Capabilities lists the models a server chose to name, with no namespace and nothing in the protocol making it every module the device implements, so a module it does not list may still be served. A read over NETCONF or RESTCONF, from the YANG library, is what can say complete."},
			},
			Examples: []collection.Example{
				{Name: "Read what a device offers over gNMI", RunbookYAML: "- name: Discover the device's gNMI models\n  net.gnmi.facts:\n  register: discovered\n"},
			},
			SeeAlso: []string{"net.gnmi.config", "net.netconf.facts", "net.restconf.facts"},
		},
	},
	// net.gnmi.config is gNMI's write, after nokia.grpc.gnmi_config: deletes,
	// replaces and updates as one Set, each path read before and after.
	{
		Name:          "net.gnmi.config",
		Capabilities:  []capability.Name{capability.NameGNMI},
		Transports:    []string{"gnmi"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Writes YANG-modeled paths over gNMI (delete, replace, update) as one Set, reporting whether they changed, with an optional pre-change backup.",
			Description: "Asks the device's gNMI server its Capabilities, as net.gnmi.facts does (recording the same facts), then reads every path the task names, sends its deletes, replaces and updates as one gNMI Set, which the device applies all of or none of (gNMI specification section 3.4.3), and reads every path again. Parameter names are nokia.grpc.gnmi_config's own. The device is reached at its own target with its own TLS (always verified, a pinned certificate included, mutual TLS from the stored certificate) and its own stored credential, and a refused login is not sent again within the task. Every path is parsed, never passed on: each element is held to the YANG identifier grammar with an optional module prefix, each key value to text with no control character, and a wildcard is refused, so a key holding / or ] stays inside its key and never names another node. An encoding the device's Capabilities does not list is refused before anything is sent. The reads before and after are compared with key order and white space set aside, so a run that leaves every path as it found it reports no change: an update or replace of what is already there changes nothing, and a task whose every path is a delete of one that is not there sends nothing. A refused Set changed nothing, and comes back as the device's own gRPC status, masked. A Set whose answer was lost (the deadline passed, or the connection broke, after it was sent) is read back to say what happened, and fails the task. Every value recorded (the backup, the diff and every error) is masked by value, the device's credential and every named secret a value resolved in each form it could be echoed in, and then by shape, every secret-named leaf and crypt-shaped value; the values written are never recorded.",
			Params: []collection.Param{
				{Name: "prefix", Type: "string", Description: "A path prepended to every path below, as gNMI's path conventions write one: /interface[name=ethernet-1/1], or with an origin, openconfig:/interfaces. Each path below is then relative to it."},
				{Name: "update", Type: "list of dict", Description: "Nodes to merge a value into, each {path, val}: path as gNMI writes one (/interface[name=ethernet-1/1]/description), val any value (a string, a number, a list or a mapping), sent as one JSON document in encoding. A key value holding ] or \\ writes it as \\] or \\\\. A string inside val may name a stored secret, such as a pre-shared key, with a secret reference, {{ secret.<name>.<field> }} (pleiades add-secret): it is resolved just before the Set, and masked in everything the run reports; the values written are never recorded. A string holding one holds no other template expression, and a value with whitespace, a control character or ? is refused. A value holding a masked placeholder (pleiades-masked-) is refused, since it would set each secret to the placeholder.", SecretRefs: true},
				{Name: "replace", Type: "list of dict", Description: "Nodes to replace whole, each {path, val} as update's: whatever is at path and not in val is removed.", SecretRefs: true},
				{Name: "delete", Type: "list", Description: "Paths to remove, each a string as gNMI writes one. A path that is not there is accepted by the device, as the gNMI specification requires, and reported as no change."},
				{Name: "encoding", Type: "string", Default: "json_ietf", Choices: []string{"json_ietf", "json"}, ChoicesIgnoreCase: true, Description: "How values are sent and the reads before and after are made: json_ietf (RFC 7951, member names qualified by their module) or json."},
				{Name: "backup", Type: "bool", Default: "false", Description: "Record every path as it was before the Set under the backup stat, masked as every recorded value is: null for a path that was not there. A value masking cannot vouch for fails the task before anything is written."},
			},
			Returns: []collection.ReturnField{
				{Name: "backup", Type: "dict", Returned: "when backup is true", Description: "Each path the task names, as gNMI writes it, mapped to its value as the device answered it before the Set (each node under it by its full path), masked by value (the device's credential and every named secret a value resolved) and by shape (every secret-named leaf and crypt-shaped value); null for a path that was not there. net.gnmi.config refuses a value holding a placeholder, so this cannot be written back as it is."},
				{Name: "gnmi.version", Type: "string", Returned: "on every read, as a stat; as a fact only when the device was reached over TLS", Description: "The gNMI version the server says it implements, such as 0.7.0; empty when it names none."},
				{Name: "gnmi.encodings", Type: "list of string", Returned: "on every read, as a stat; as a fact only when the device was reached over TLS", Description: "The encodings the server says it answers in, by gNMI's names lowercased (json, bytes, proto, ascii, json_ietf), in gNMI's own order. A server that answers PROTO alone (Stratum, measured 2026-11-15) refuses a Get in JSON."},
				{Name: "gnmi.models", Type: "list of string", Returned: "on every read, as a stat; as a fact only when the device was reached over TLS, when the list fits in 60 KiB", Description: "Every model the server lists, each name@version (name alone for a model with no version), as the server wrote them, sorted; an entry that is not printable or is too long is left out, with a warning."},
				{Name: "gnmi.model_count", Type: "int", Returned: "on every read, as a stat; as a fact only when the device was reached over TLS", Description: "How many models the server lists, as gnmi.models counts them."},
				{Name: "gnmi.models_digest", Type: "string", Returned: "on every read, as a stat; as a fact only when the device was reached over TLS", Description: "The SHA-256 (hex) of the model list, each model's name, organization and version, sorted, so any change to what the server says it implements reads as a change of this one value. It is not yang.modules_digest, which only a YANG library gives."},
				{Name: "yang.modules", Type: "list of string", Returned: "on every read, as a stat; as a fact only when the device was reached over TLS, when the list fits in 60 KiB", Description: "Every YANG module the device's gNMI Capabilities lists a model for, each name@revision, or name alone where the model's version is not a revision date (an OpenConfig model's semantic version, such as 2.4.1, is not one), sorted. A model named by a URN ending in :module is read as that module. gNMI's Capabilities is not a YANG library, so this list is never complete: yang.completeness is always partial. The same key net.netconf.facts and net.restconf.facts record from the device's YANG library."},
				{Name: "yang.module_count", Type: "int", Returned: "on every read, as a stat; as a fact only when the device was reached over TLS", Description: "How many modules yang.modules lists."},
				{Name: "yang.completeness", Type: "string", Returned: "on every read, as a stat; as a fact only when the device was reached over TLS", Description: "Always partial for a gNMI read: Capabilities lists the models a server chose to name, with no namespace and nothing in the protocol making it every module the device implements, so a module it does not list may still be served. A read over NETCONF or RESTCONF, from the YANG library, is what can say complete."},
			},
			Examples: []collection.Example{
				{Name: "Describe and enable a loopback", RunbookYAML: "- name: Describe and enable lo0\n  net.gnmi.config:\n    update:\n      - path: /interface[name=lo0]\n        val:\n          admin-state: enable\n          description: managed by The Pleiades\n"},
				{Name: "Remove a network instance", RunbookYAML: "- name: Remove the lab VRF\n  net.gnmi.config:\n    delete:\n      - /network-instance[name=lab]\n"},
			},
			SeeAlso: []string{"net.gnmi.facts", "net.restconf.config", "net.netconf.config"},
		},
	},
	// wait.gnmi is wait.restconf's gNMI sibling: a device's gRPC port can
	// accept connections before its gNMI service answers.
	{
		Name:          "wait.gnmi",
		Capabilities:  []capability.Name{capability.NameGNMI},
		Transports:    []string{"gnmi"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Waits until the target's gNMI server answers its account with its Capabilities.",
			Description: "Tries the device's gNMI server until its Capabilities RPC answers with the device's own credential, and fails when the timeout runs out first, through the same client the net.gnmi.* methods use. It is the task to put between one that starts or restarts a network device and the first net.gnmi.* task: a device's gRPC port can accept connections before its gNMI service answers, and wait.connection, which logs in over SSH, says nothing about it. Each try is a new client on a new connection, so a connection kept open from an earlier try is never taken for an answer. The device's own TLS settings decide which certificate is trusted, and nothing here can weaken them: a certificate they do not trust ends the wait at once, before any call is sent, naming its subject and fingerprint and the tls_fingerprint or tls_ca_pem property that would trust it once checked; so does a server that requires a client certificate and was presented none, or one it does not accept (a TLS alert such as certificate required, which SONiC's server sends unless configured otherwise), naming the tls_client_certificate property; and so does a login the device refuses (UNAUTHENTICATED), since none of these changes by being asked again and a password sent at every try can lock the account. A server that refuses or resets the connection, sends no answer within connect_timeout, or answers with any other gRPC status (UNAVAILABLE while it starts, among them) is tried again until the timeout. A failed try is not an error, since it is what the task waits to see stop; the last one is quoted when the timeout ends the wait. The delay, backoff and steady parameters work as wait.connection's do. It changes nothing.",
			Params: []collection.Param{
				{Name: "timeout", Type: "int", Default: "600", Description: "How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0."},
				{Name: "delay", Type: "int", Default: "0", Description: "How many seconds to wait before the first try. Must be shorter than the timeout, which it is spent out of."},
				{Name: "sleep", Type: "int", Default: "5", Description: "How many seconds to wait between tries, or with backoff: exponential before the second try, the gap doubling after each failed one. Must be more than 0."},
				{Name: "backoff", Type: "string", Default: "fixed", Choices: []string{"fixed", "exponential"}, Description: "How the gap between failed tries grows: fixed keeps it at sleep; exponential doubles it after each failed try, up to max_sleep, each gap a random point in its upper half."},
				{Name: "max_sleep", Type: "int", Default: "60", Description: "With backoff: exponential, the longest gap between tries, in seconds. Not less than sleep; refused with backoff: fixed, where it would do nothing."},
				{Name: "steady", Type: "int", Default: "1", Description: "How many tries in a row must open a session before the listener counts as ready. Each is its own login. Must be at least 1, and the streak must fit in the timeout after the delay."},
				{Name: "steady_interval", Type: "int", Default: "10", Description: "With steady above 1, the seconds between the answering tries. Must be more than 0; refused with steady 1, where it would do nothing."},
				{Name: "connect_timeout", Type: "int", Default: "20", Description: "How many seconds one try may take, from the connection to the Capabilities answer, before it counts as failed. Must be more than 0."},
			},
			Returns: []collection.ReturnField{
				{Name: "elapsed", Type: "int", Returned: "always", Description: "How many whole seconds the task waited, including the delay."},
				{Name: "version", Type: "string", Returned: "always", Description: "The gNMI version the server named on the try that answered, such as 0.7.0; empty when it names none."},
				{Name: "tries", Type: "int", Returned: "always", Description: "How many tries the task made, answered or not."},
				{Name: "failures", Type: "int", Returned: "always", Description: "How many of those tries did not answer."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The version that answered. Both halves are identical, since a wait changes nothing."},
			},
			Examples: []collection.Example{
				{Name: "Wait for a switch's gNMI server after it boots", RunbookYAML: "- name: Wait for the switch's gNMI server to answer its account\n  wait.gnmi:\n    timeout: 600\n    sleep: 5\n"},
			},
			SeeAlso: []string{"wait.restconf", "wait.netconf", "net.gnmi.facts", "net.gnmi.config"},
		},
	},
}
