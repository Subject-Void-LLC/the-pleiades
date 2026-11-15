// Package gnmi: "net.gnmi.config", gNMI's write (gNMI specification section
// 3.4), after nokia.grpc.gnmi_config: deletes, replaces and updates sent as
// one Set, which the device applies all or none of, each path read before and
// after so a change is read back rather than assumed, and the undo recorded
// where it is exact, for a create. The registration and documentation are
// here; the run is in config_run.go and config_write.go.
package gnmi

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// configFQCN is the method's name.
const configFQCN = "net.gnmi.config"

// The parameters, nokia.grpc.gnmi_config's own names, and backup after
// net.netconf.config's.
const (
	paramPrefix   = "prefix"
	paramUpdate   = "update"
	paramReplace  = "replace"
	paramDelete   = "delete"
	paramEncoding = "encoding"
	paramBackup   = "backup"
)

// The keys of one update or replace entry.
const (
	entryPath  = "path"
	entryValue = "val"
)

// statBackup is the stat backup: true records the earlier values under.
const statBackup = "backup"

func init() {
	doc := configDoc()
	collection.MustRegister(collection.Descriptor{
		Name: configFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportGNMI},
			RequiredCapabilities: []capability.Name{capability.NameGNMI},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			EngineVersion:  ">=0.4.0",
			Status:         collection.StatusImplemented,
			NoBecomeReason: noBecome,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "A run that created a node records net.gnmi.config deleting it, which is exact: for each update or replace path the read before found absent (the device answered NOT_FOUND, or nothing) and the read after found present, the undo deletes the highest node that was absent before, its absent ancestors read upward until one is present, so nothing the run did not make is removed. gNMI's Set answer does not say whether it created anything, so the reads are what decide. A replace, a merge or a delete of what was there records no undo: putting the earlier values back would keep them in the run's journal, which may hold secrets, so a rollback refuses such a task unless it is left (--leave) or the task's rollback: list says what to do; backup: true keeps the masked earlier values beside the run. A run that changed nothing records nothing.",
				Inverses: []sdk.InverseSpec{{
					FQCN:             configFQCN,
					Record:           []string{paramDelete, paramEncoding},
					MayKeepOnFailure: true,
				}},
			},
			NoCheckReason: "gNMI's Set has no dry run and no candidate, so only applying it says what the device makes of it, and a check promises to write nothing.",
			Doc:           doc,
			Facts:         collection.DeclareFacts(doc.Returns, factKeys),
		},
		Invoke: Config,
	})
}

// configDoc is net.gnmi.config's documentation, word for word its catalog
// entry's.
func configDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Writes YANG-modeled paths over gNMI (delete, replace, update) as one Set, reporting whether they changed, with an optional pre-change backup.",
		Description: "Asks the device's gNMI server its Capabilities, as net.gnmi.facts does (recording the same facts), then reads every path the task names, sends its deletes, replaces and updates as one gNMI Set, which the device applies all of or none of (gNMI specification section 3.4.3), and reads every path again. Parameter names are nokia.grpc.gnmi_config's own. The device is reached at its own target with its own TLS (always verified, a pinned certificate included, mutual TLS from the stored certificate) and its own stored credential, and a refused login is sent once and never again. Every path is parsed, never passed on: each element is held to the YANG identifier grammar with an optional module prefix, each key value to text with no control character, and a wildcard is refused, so a key holding / or ] stays inside its key and never names another node. An encoding the device's Capabilities does not list is refused before anything is sent. The reads before and after are compared with key order and white space set aside, so a run that leaves every path as it found it reports no change: an update or replace of what is already there changes nothing, and a task whose every path is a delete of one that is not there sends nothing. A refused Set changed nothing, and comes back as the device's own gRPC status, masked. A Set whose answer was lost (the deadline passed, or the connection broke, after it was sent) is read back to say what happened, and fails the task. Every value recorded (the backup, the diff and every error) is masked by value, the device's credential and every named secret a value resolved in each form it could be echoed in, and then by shape, every secret-named leaf and crypt-shaped value; the values written are never recorded.",
		Params: []collection.Param{
			{Name: paramPrefix, Type: "string", Description: "A path prepended to every path below, as gNMI's path conventions write one: /interface[name=ethernet-1/1], or with an origin, openconfig:/interfaces. Each path below is then relative to it."},
			{Name: paramUpdate, Type: "list of dict", SecretRefs: true, Description: "Nodes to merge a value into, each {path, val}: path as gNMI writes one (/interface[name=ethernet-1/1]/description), val any value (a string, a number, a list or a mapping), sent as one JSON document in encoding. A key value holding ] or \\ writes it as \\] or \\\\. A string inside val may name a stored secret, such as a pre-shared key, with a secret reference, {{ secret.<name>.<field> }} (pleiades add-secret): it is resolved just before the Set, and masked in everything the run reports; the values written are never recorded. A string holding one holds no other template expression, and a value with whitespace, a control character or ? is refused. A value holding a masked placeholder (pleiades-masked-) is refused, since it would set each secret to the placeholder."},
			{Name: paramReplace, Type: "list of dict", SecretRefs: true, Description: "Nodes to replace whole, each {path, val} as update's: whatever is at path and not in val is removed."},
			{Name: paramDelete, Type: "list of string", Description: "Paths to remove, as gNMI writes one. A path that is not there is accepted by the device, as the gNMI specification requires, and reported as no change."},
			{Name: paramEncoding, Type: "string", Default: "json_ietf", Choices: []string{"json_ietf", "json"}, ChoicesIgnoreCase: true, Description: "How values are sent and the reads before and after are made: json_ietf (RFC 7951, member names qualified by their module) or json."},
			{Name: paramBackup, Type: "bool", Default: "false", Description: "Record every path as it was before the Set under the backup stat, masked as every recorded value is: null for a path that was not there. A value masking cannot vouch for fails the task before anything is written."},
		},
		Returns: append([]collection.ReturnField{
			{Name: statBackup, Type: "dict", Returned: "when backup is true", Description: "Each path the task names, as gNMI writes it, mapped to its value as the device answered it before the Set (each node under it by its full path), masked by value (the device's credential and every named secret a value resolved) and by shape (every secret-named leaf and crypt-shaped value); null for a path that was not there. net.gnmi.config refuses a value holding a placeholder, so this cannot be written back as it is."},
		}, factsReturns()...),
		Examples: []collection.Example{
			{Name: "Describe and enable a loopback", RunbookYAML: "- name: Describe and enable lo0\n  net.gnmi.config:\n    update:\n      - path: /interface[name=lo0]\n        val:\n          admin-state: enable\n          description: managed by The Pleiades\n"},
			{Name: "Remove a network instance", RunbookYAML: "- name: Remove the lab VRF\n  net.gnmi.config:\n    delete:\n      - /network-instance[name=lab]\n"},
		},
		SeeAlso: []string{"net.gnmi.facts", "net.restconf.config", "net.netconf.config"},
	}
}
