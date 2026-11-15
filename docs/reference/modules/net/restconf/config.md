---
status: beta
---

# net.restconf.config

Writes one YANG data resource over RESTCONF (post, put, patch or delete), reporting whether it changed, with an optional pre-change backup.

Finds the device's RESTCONF root from its /.well-known/host-meta, reads its capabilities and YANG library as net.restconf.facts does (recording the same facts), and writes the resource path names with method: post creates content under path, put creates or replaces the resource at path, patch merges content into it, and delete removes it. Parameter names are ansible.netcommon.restconf_config's own. The device is reached at its own origin with its own TLS (always verified, a pinned certificate included) and its own stored credential, following no redirect, and a refused credential is sent once and never again. path is parsed, never passed on: each node is held to the YANG identifier grammar and each key decoded once and encoded again, so a key holding / or , stays inside its key and never addresses another resource. A module the device does not implement is refused before anything is sent, when its YANG library was read whole, and so is a datastore its library does not list. The resource is read before and after the write, in format, and the two compared with key order and white space set aside, so a run that leaves the resource as it found it reports no change: a put or patch of what is already there changes nothing, a delete of a resource that is not there sends nothing, and a post answered 409 Conflict (RFC 8040's resource-denied, or data-exists: the resource is there already, and post never replaces one) changes nothing and says so. A patch of a resource that is not there is refused before anything is sent. A rejected write comes back as the device's own ietf-restconf:errors, structured and masked. Every answer recorded (the backup, the diff and every error) is masked by value, the device's credential and every named secret the content resolved in each form it could be echoed in, and then by shape, every secret-named leaf and crypt-shaped value; the content itself is never recorded.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `RestconfCapable` |
| Transports | `restconf` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since RFC 8040 has no candidate and no validate, so only writing the document says what the device makes of it, and a check promises to write nothing. |
| Engine version | `>=0.4.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `path` | `string` | yes | - | The resource to write, as RFC 8040 writes a data resource path: module:node/list=key1,key2/..., such as ietf-netconf-acm:nacm/groups/group=ops, under the root's /data, or under the datastore named. A key holding /, , or = must be percent-encoded (%2F, %2C, %3D); every key is decoded once and encoded again, and a path that does not parse is refused before anything is sent, as is a query, a fragment, an empty node or a dot segment. The first node names its module. For post, path is the parent the new resource is created under. |
| `content` | `string` | no | - | **Accepts `{{ secret.<name>.<field> }}`:** The document to write, in format: for post the resource to create under path, for put the resource at path whole, for patch what to merge into it. Required for those, refused with delete. JSON must parse; XML must be well-formed, with no DOCTYPE or other directive, and nest no deeper than 256 elements. A JSON string value or an XML leaf may name a stored secret, such as a pre-shared key, with a secret reference, {{ secret.<name>.<field> }} (pleiades add-secret): it is resolved just before the write, escaped as JSON string content or as XML text, and masked in everything the run reports; the content is never recorded. A reference outside a JSON string is refused. A document holding one holds no other template expression, so a per-device value and a secret go in separate tasks, and a value with whitespace, a control character or ? is refused. Content holding a masked placeholder (pleiades-masked-) is refused, since it would set each secret to the placeholder. |
| `method` | `string` | no | `post` | How to write: post (create under path; never replaces), put (create or replace the resource at path whole), patch (merge into the resource at path, which must exist), or delete (remove it). **Choices:** `post`, `put`, `patch`, `delete` (case ignored). |
| `format` | `string` | no | `json` | The encoding of content, and of the reads before and after: json (application/yang-data+json) or xml (application/yang-data+xml). **Choices:** `json`, `xml` (case ignored). |
| `datastore` | `string` | no | - | Write the NMDA datastore RFC 8527 addresses under the root's /ds/, ietf-datastores:running or ietf-datastores:startup, instead of the conventional /data. Refused before anything is sent unless the device's YANG library lists that datastore. candidate is not offered: RESTCONF has no commit, so an edit there would stay staged for the next commit by any client. **Choices:** `running`, `startup` (case ignored). |
| `backup` | `bool` | no | `false` | Record the resource as it was before the write under the backup stat, masked as every recorded answer is (empty when the resource was not there). A document masking cannot vouch for fails the task before anything is written. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `backup` | `string` | when backup is true | The resource as the device answered it before the write, in format, masked by value (the device's credential and every named secret the content resolved) and by shape (every secret-named leaf and crypt-shaped value); empty when the resource was not there. net.restconf.config refuses content holding a placeholder, so this text cannot be written back as it is. |
| `restconf.root` | `string` | on every read, as a stat; as a fact only when the device was reached over https | The RESTCONF root the device's /.well-known/host-meta names, as an escaped path (such as /restconf), or / for a root at the device's origin itself. |
| `restconf.capabilities` | `list of string` | when the capability list was read and fits, as a stat; as a fact only when the device was reached over https | Every capability URI the device's ietf-restconf-monitoring lists (RFC 8040 section 9.1), with its parameters, sorted. Left out, with a warning, when the list could not be read or does not fit in 60 KiB, so an unread list never reads as an empty one; an entry that is not printable is left out whole. |
| `restconf.datastores` | `list of string` | when the YANG library was read, as a stat; as a fact only when the device was reached over https | The datastores the device's YANG library lists, sorted: an NMDA device's running, intended and operational, which RFC 8527 addresses under the root's /ds/; empty for a library that lists none, as RFC 7895's modules-state does. |
| `yang.modules` | `list of string` | when the list fits in 60 KiB as JSON | Every YANG module the device implements, each as name@revision (the name alone for a module with no revision), sorted, read from the device's YANG library over RESTCONF (RFC 8525's yang-library, or RFC 7895's modules-state from a device that does not serve it). An entry that is not a YANG identifier, a revision date or a printable namespace is left out whole, never cut short. Left out when the list does not fit; yang.module_count and yang.modules_digest are always recorded. |
| `yang.module_count` | `int` | when the YANG library was read, as a stat; as a fact only when the device was reached over https | How many YANG modules the device implements, as yang.modules counts them. |
| `yang.modules_digest` | `string` | when the YANG library was read, as a stat; as a fact only when the device was reached over https | The SHA-256 (hex) of the module set in its canonical form (every module's name, namespace, revision, features and deviations, sorted, as JSON), so two devices implementing the same modules read alike and any change to the set reads as a change of this one value. |
| `yang.completeness` | `string` | when the YANG library was read, as a stat; as a fact only when the device was reached over https | How far yang.modules is every module the device implements: complete (the YANG library read whole) or partial (a library that dropped a malformed or oversized module entry; a refused capability does not count). A library that could not be read, or came back empty as it does for an account the device's access control hides it from, records no yang.* fact at all, so it never reads as a device that implements no module. |
| `yang.content_id` | `string` | when the device's YANG library names one | The YANG library's content-id (RFC 8525's content-id, or RFC 7895's module-set-id) from the library itself, a hint the device may regenerate on restart; yang.modules_digest is the module set's identity. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that created the resource (a put to a path that was not there, or a post whose answer named the created resource in Location) records net.restconf.config with method delete of that path, which is exact, and stands even if the read after the write fails. A replace, a merge or a delete of a resource that was there records no undo: putting the earlier document back would keep it in the run's journal, which may hold secrets, so a rollback refuses such a task unless it is left (--leave) or the task's rollback: list says what to do; backup: true keeps the masked earlier document beside the run. A run that changed nothing records nothing.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## Facts it keeps

A real run stores each of these as the device's fact. A **stable** fact keeps every value it has had, and a change is drift; a **volatile** one keeps only its latest value and never counts as drift. A check stores none.

| Fact | Kind | Type | Description |
| --- | --- | --- | --- |
| `restconf.root` | stable | string | The RESTCONF root the device's /.well-known/host-meta names, as an escaped path (such as /restconf), or / for a root at the device's origin itself. |
| `restconf.capabilities` | stable | list | Every capability URI the device's ietf-restconf-monitoring lists (RFC 8040 section 9.1), with its parameters, sorted. Left out, with a warning, when the list could not be read or does not fit in 60 KiB, so an unread list never reads as an empty one; an entry that is not printable is left out whole. |
| `restconf.datastores` | stable | list | The datastores the device's YANG library lists, sorted: an NMDA device's running, intended and operational, which RFC 8527 addresses under the root's /ds/; empty for a library that lists none, as RFC 7895's modules-state does. |
| `yang.modules` | stable | list | Every YANG module the device implements, each as name@revision (the name alone for a module with no revision), sorted, read from the device's YANG library over RESTCONF (RFC 8525's yang-library, or RFC 7895's modules-state from a device that does not serve it). An entry that is not a YANG identifier, a revision date or a printable namespace is left out whole, never cut short. Left out when the list does not fit; yang.module_count and yang.modules_digest are always recorded. |
| `yang.module_count` | stable | integer | How many YANG modules the device implements, as yang.modules counts them. |
| `yang.modules_digest` | stable | string | The SHA-256 (hex) of the module set in its canonical form (every module's name, namespace, revision, features and deviations, sorted, as JSON), so two devices implementing the same modules read alike and any change to the set reads as a change of this one value. |
| `yang.completeness` | stable | string | How far yang.modules is every module the device implements: complete (the YANG library read whole) or partial (a library that dropped a malformed or oversized module entry; a refused capability does not count). A library that could not be read, or came back empty as it does for an account the device's access control hides it from, records no yang.* fact at all, so it never reads as a device that implements no module. |
| `yang.content_id` | volatile | string | The YANG library's content-id (RFC 8525's content-id, or RFC 7895's module-set-id) from the library itself, a hint the device may regenerate on restart; yang.modules_digest is the module set's identity. |

## See also

- `net.restconf.facts`
- `net.netconf.config`

## Examples

Create or replace an access-control group:

```yaml
- name: Make the ops group
  net.restconf.config:
    method: put
    path: ietf-netconf-acm:nacm/groups/group=ops
    content: '{"ietf-netconf-acm:group": [{"name": "ops", "user-name": ["alice"]}]}'
```

Remove a group whose name holds a slash:

```yaml
- name: Remove the ops/lab group
  net.restconf.config:
    method: delete
    path: ietf-netconf-acm:nacm/groups/group=ops%2Flab
```

