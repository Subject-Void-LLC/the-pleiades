// Package restconf: "net.restconf.config", RESTCONF's write (RFC 8040
// section 4), after ansible.netcommon.restconf_config: one data resource
// posted, put, patched or deleted, its change read back rather than assumed,
// and its undo recorded where it is exact, for a create. The registration and
// documentation are here; the run is in config_run.go and config_write.go.
package restconf

import (
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangfacts"
)

// The parameters, ansible.netcommon.restconf_config's own names, and
// datastore and backup after net.netconf.config's.
const (
	paramPath      = "path"
	paramContent   = "content"
	paramMethod    = "method"
	paramFormat    = "format"
	paramDatastore = "datastore"
	paramBackup    = "backup"
)

func init() {
	doc := configDoc()
	collection.MustRegister(collection.Descriptor{
		Name: configFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportRestconf},
			RequiredCapabilities: []capability.Name{capability.NameRestconf},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			EngineVersion:  ">=0.4.0",
			Status:         collection.StatusImplemented,
			NoBecomeReason: "it speaks RESTCONF over HTTPS to the device's own API, which no sudo prefix reaches",
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "A run whose write the device answered as a create records net.restconf.config with method delete of the resource created, which is exact: a put answered 201 Created (RFC 8040 section 4.5), or a post answered 201 Created whose Location names one node directly under path (section 4.4.1). It is recorded as the 201 arrives, so it stands whatever happens after, the read after the write included. A put answered 204 No Content or 200 is a replace, whatever the read before found (a 404 can be access control hiding a resource that is there), and a post whose Location names anything else, or nothing, records no undo either; the run says why. A replace, a merge or a delete of a resource that was there records no undo: putting the earlier document back would keep it in the run's journal, which may hold secrets, so a rollback refuses such a task unless it is left (--leave) or the task's rollback: list says what to do; backup: true keeps the masked earlier document beside the run. A run that changed nothing records nothing.",
				Inverses: []sdk.InverseSpec{{
					FQCN:             configFQCN,
					Record:           []string{paramPath, paramMethod, paramFormat, paramDatastore},
					MayKeepOnFailure: true,
				}},
			},
			NoCheckReason: "RFC 8040 has no candidate and no validate, so only writing the document says what the device makes of it, and a check promises to write nothing.",
			Doc:           doc,
			Facts:         collection.DeclareFacts(doc.Returns, factKeys, yangfacts.FactContentID),
		},
		Invoke:   Config,
		UndoCall: configUndoCall,
	})
}

// configUndoCall is net.restconf.config's answer, from a call alone, to
// whether it records the undo its manifest declares: a delete never does,
// since putting back what it removed would keep it in the run's journal, and
// neither does a patch, which merges into a resource that must already be
// there. A post or a put records one only when the device answers it as a
// create, which only the run can tell, so those are left to the run. A method
// written as a template is not known before the run, and may be either.
func configUndoCall(params map[string]any) string {
	method, ok := params[paramMethod].(string)
	if !ok {
		// Absent is post; anything but text is the body's refusal.
		return ""
	}
	if collection.IsTemplate(method) {
		return "method is a template, so whether it is a delete or a patch, which record no undo, is not known before the run"
	}
	switch strings.ToLower(method) {
	case "delete":
		return "a delete records no undo: putting back what it removed would keep it in the run's journal"
	case "patch":
		return "a patch merges into a resource that was there, and records no undo: putting its earlier document back would keep it in the run's journal"
	}
	return ""
}

// configDoc is net.restconf.config's documentation, word for word its
// catalog entry's.
func configDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Writes one YANG data resource over RESTCONF (post, put, patch or delete), reporting whether it changed, with an optional pre-change backup.",
		Description: "Finds the device's RESTCONF root from its /.well-known/host-meta, read with no credential, or, when host-meta answers 404 or a redirect (never followed), as IOS XE answers it to anything but a valid login with a 302 to its WebUI's login page (404 there), takes the conventional /restconf for the first authenticated read to confirm; it reads its capabilities and YANG library as net.restconf.facts does (recording the same facts), and writes the resource path names with method: post creates content under path, put creates or replaces the resource at path, patch merges content into it, and delete removes it. A write to the conventional /restconf that nothing under it confirmed (neither the capabilities nor the YANG library answered) is refused before the document is sent. Parameter names are ansible.netcommon.restconf_config's own. The device is reached at its own origin with its own TLS (always verified, a pinned certificate included) and its own stored credential, following no redirect, and a refused credential is not sent again within the task. path is parsed, never passed on: each node is held to the YANG identifier grammar and each key decoded once and encoded again, so a key holding / or , stays inside its key and never addresses another resource. A module the device does not implement is refused before anything is sent, when its YANG library was read whole, and so is a datastore its library does not list. The resource is read before and after the write, in format, and the two compared with key order and white space set aside, so a run that leaves the resource as it found it reports no change: a put or patch of what is already there changes nothing, a delete of a resource that is not there sends nothing, and a post answered 409 Conflict (RFC 8040's resource-denied, or data-exists: the resource is there already, and post never replaces one) changes nothing and says so. A patch of a resource that is not there is refused before anything is sent. A rejected write comes back as the device's own ietf-restconf:errors, structured and masked. Every answer recorded (the backup, the diff and every error) is masked by value, the device's credential and every named secret the content resolved in each form it could be echoed in, and then by shape, every secret-named leaf and crypt-shaped value; the content itself is never recorded.",
		Params: []collection.Param{
			{Name: paramPath, Type: "string", Required: true, Description: "The resource to write, as RFC 8040 writes a data resource path: module:node/list=key1,key2/..., such as ietf-netconf-acm:nacm/groups/group=ops, under the root's /data, or under the datastore named. A key holding /, , or = must be percent-encoded (%2F, %2C, %3D); every key is decoded once and encoded again, and a path that does not parse is refused before anything is sent, as is a query, a fragment, an empty node or a dot segment. The first node names its module. For post, path is the parent the new resource is created under."},
			{Name: paramContent, Type: "string", SecretRefs: true, Description: "The document to write, in format: for post the resource to create under path, for put the resource at path whole, for patch what to merge into it. Required for those, refused with delete. JSON must parse; XML must be well-formed, with no DOCTYPE or other directive, and nest no deeper than 256 elements. A JSON string value or an XML leaf may name a stored secret, such as a pre-shared key, with a secret reference, {{ secret.<name>.<field> }} (pleiades add-secret): it is resolved just before the write, escaped as JSON string content or as XML text, and masked in everything the run reports; the content is never recorded. A reference outside a JSON string is refused. A document holding one holds no other template expression, so a per-device value and a secret go in separate tasks, and a value with whitespace, a control character or ? is refused. Content holding a masked placeholder (pleiades-masked-) is refused, since it would set each secret to the placeholder."},
			{Name: paramMethod, Type: "string", Default: "post", Choices: []string{"post", "put", "patch", "delete"}, ChoicesIgnoreCase: true, Description: "How to write: post (create under path; never replaces), put (create or replace the resource at path whole), patch (merge into the resource at path, which must exist), or delete (remove it)."},
			{Name: paramFormat, Type: "string", Default: "json", Choices: []string{"json", "xml"}, ChoicesIgnoreCase: true, Description: "The encoding of content, and of the reads before and after: json (application/yang-data+json) or xml (application/yang-data+xml)."},
			{Name: paramDatastore, Type: "string", Choices: []string{"running", "startup"}, ChoicesIgnoreCase: true, Description: "Write the NMDA datastore RFC 8527 addresses under the root's /ds/, ietf-datastores:running or ietf-datastores:startup, instead of the conventional /data. Refused before anything is sent unless the device's YANG library lists that datastore. candidate is not offered: RESTCONF has no commit, so an edit there would stay staged for the next commit by any client."},
			{Name: paramBackup, Type: "bool", Default: "false", Description: "Record the resource as it was before the write under the backup stat, masked as every recorded answer is: empty when the resource was not there, and always for a post, which reads nothing before it writes (path is the parent, and the resource a post creates is not there yet). A document masking cannot vouch for fails the task before anything is written."},
		},
		Returns: append([]collection.ReturnField{
			{Name: statBackup, Type: "string", Returned: "when backup is true", Description: "The resource as the device answered it before the write, in format, masked by value (the device's credential and every named secret the content resolved) and by shape (every secret-named leaf and crypt-shaped value); empty when the resource was not there, and always for a post. net.restconf.config refuses content holding a placeholder, so this text cannot be written back as it is."},
		}, factsReturns()...),
		Examples: []collection.Example{
			{Name: "Create or replace an access-control group", RunbookYAML: "- name: Make the ops group\n  net.restconf.config:\n    method: put\n    path: ietf-netconf-acm:nacm/groups/group=ops\n    content: '{\"ietf-netconf-acm:group\": [{\"name\": \"ops\", \"user-name\": [\"alice\"]}]}'\n"},
			{Name: "Remove a group whose name holds a slash", RunbookYAML: "- name: Remove the ops/lab group\n  net.restconf.config:\n    method: delete\n    path: ietf-netconf-acm:nacm/groups/group=ops%2Flab\n"},
		},
		SeeAlso: []string{"net.restconf.facts", "net.netconf.config"},
	}
}
