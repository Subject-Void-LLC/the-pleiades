---
status: beta
---

# net.gnmi.config

Writes YANG-modeled paths over gNMI (delete, replace, update) as one Set, reporting whether they changed, with an optional pre-change backup.

Asks the device's gNMI server its Capabilities, as net.gnmi.facts does (recording the same facts), then reads every path the task names, sends its deletes, replaces and updates as one gNMI Set, which the device applies all of or none of (gNMI specification section 3.4.3), and reads every path again. Parameter names are nokia.grpc.gnmi_config's own. The device is reached at its own target with its own TLS (always verified, a pinned certificate included, mutual TLS from the stored certificate) and its own stored credential, and a refused login is sent once and never again. Every path is parsed, never passed on: each element is held to the YANG identifier grammar with an optional module prefix, each key value to text with no control character, and a wildcard is refused, so a key holding / or ] stays inside its key and never names another node. An encoding the device's Capabilities does not list is refused before anything is sent. The reads before and after are compared with key order and white space set aside, so a run that leaves every path as it found it reports no change: an update or replace of what is already there changes nothing, and a task whose every path is a delete of one that is not there sends nothing. A refused Set changed nothing, and comes back as the device's own gRPC status, masked. A Set whose answer was lost (the deadline passed, or the connection broke, after it was sent) is read back to say what happened, and fails the task. Every value recorded (the backup, the diff and every error) is masked by value, the device's credential and every named secret a value resolved in each form it could be echoed in, and then by shape, every secret-named leaf and crypt-shaped value; the values written are never recorded.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `GNMICapable` |
| Transports | `gnmi` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since gNMI's Set has no dry run and no candidate, so only applying it says what the device makes of it, and a check promises to write nothing. |
| Engine version | `>=0.4.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `prefix` | `string` | no | - | A path prepended to every path below, as gNMI's path conventions write one: /interface[name=ethernet-1/1], or with an origin, openconfig:/interfaces. Each path below is then relative to it. |
| `update` | `list of dict` | no | - | **Accepts `{{ secret.<name>.<field> }}`:** Nodes to merge a value into, each {path, val}: path as gNMI writes one (/interface[name=ethernet-1/1]/description), val any value (a string, a number, a list or a mapping), sent as one JSON document in encoding. A key value holding ] or \ writes it as \] or \\. A string inside val may name a stored secret, such as a pre-shared key, with a secret reference, {{ secret.<name>.<field> }} (pleiades add-secret): it is resolved just before the Set, and masked in everything the run reports; the values written are never recorded. A string holding one holds no other template expression, and a value with whitespace, a control character or ? is refused. A value holding a masked placeholder (pleiades-masked-) is refused, since it would set each secret to the placeholder. |
| `replace` | `list of dict` | no | - | **Accepts `{{ secret.<name>.<field> }}`:** Nodes to replace whole, each {path, val} as update's: whatever is at path and not in val is removed. |
| `delete` | `list` | no | - | Paths to remove, each a string as gNMI writes one. A path that is not there is accepted by the device, as the gNMI specification requires, and reported as no change. |
| `encoding` | `string` | no | `json_ietf` | How values are sent and the reads before and after are made: json_ietf (RFC 7951, member names qualified by their module) or json. **Choices:** `json_ietf`, `json` (case ignored). |
| `backup` | `bool` | no | `false` | Record every path as it was before the Set under the backup stat, masked as every recorded value is: null for a path that was not there. A value masking cannot vouch for fails the task before anything is written. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `backup` | `dict` | when backup is true | Each path the task names, as gNMI writes it, mapped to its value as the device answered it before the Set (each node under it by its full path), masked by value (the device's credential and every named secret a value resolved) and by shape (every secret-named leaf and crypt-shaped value); null for a path that was not there. net.gnmi.config refuses a value holding a placeholder, so this cannot be written back as it is. |
| `gnmi.version` | `string` | on every read, as a stat; as a fact only when the device was reached over TLS | The gNMI version the server says it implements, such as 0.7.0; empty when it names none. |
| `gnmi.encodings` | `list of string` | on every read, as a stat; as a fact only when the device was reached over TLS | The encodings the server says it answers in, by gNMI's names lowercased (json, bytes, proto, ascii, json_ietf), in gNMI's own order. A server that answers PROTO alone (Stratum, measured 2026-11-15) refuses a Get in JSON. |
| `gnmi.models` | `list of string` | on every read, as a stat; as a fact only when the device was reached over TLS, when the list fits in 60 KiB | Every model the server lists, each name@version (name alone for a model with no version), as the server wrote them, sorted; an entry that is not printable or is too long is left out, with a warning. |
| `gnmi.model_count` | `int` | on every read, as a stat; as a fact only when the device was reached over TLS | How many models the server lists, as gnmi.models counts them. |
| `gnmi.models_digest` | `string` | on every read, as a stat; as a fact only when the device was reached over TLS | The SHA-256 (hex) of the model list, each model's name, organization and version, sorted, so any change to what the server says it implements reads as a change of this one value. It is not yang.modules_digest, which only a YANG library gives. |
| `yang.modules` | `list of string` | on every read, as a stat; as a fact only when the device was reached over TLS, when the list fits in 60 KiB | Every YANG module the device's gNMI Capabilities lists a model for, each name@revision, or name alone where the model's version is not a revision date (an OpenConfig model's semantic version, such as 2.4.1, is not one), sorted. A model named by a URN ending in :module is read as that module. gNMI's Capabilities is not a YANG library, so this list is never complete: yang.completeness is always partial. The same key net.netconf.facts and net.restconf.facts record from the device's YANG library. |
| `yang.module_count` | `int` | on every read, as a stat; as a fact only when the device was reached over TLS | How many modules yang.modules lists. |
| `yang.completeness` | `string` | on every read, as a stat; as a fact only when the device was reached over TLS | Always partial for a gNMI read: Capabilities lists the models a server chose to name, with no namespace and nothing in the protocol making it every module the device implements, so a module it does not list may still be served. A read over NETCONF or RESTCONF, from the YANG library, is what can say complete. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that created a node records net.gnmi.config deleting it, which is exact: for each update or replace path the read before found absent (the device answered NOT_FOUND, or nothing) and the read after found present, the undo deletes the highest node that was absent before, its absent ancestors read upward until one is present, so nothing the run did not make is removed. gNMI's Set answer does not say whether it created anything, so the reads are what decide. A replace, a merge or a delete of what was there records no undo: putting the earlier values back would keep them in the run's journal, which may hold secrets, so a rollback refuses such a task unless it is left (--leave) or the task's rollback: list says what to do; backup: true keeps the masked earlier values beside the run. A run that changed nothing records nothing.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## Facts it keeps

A real run stores each of these as the device's fact. A **stable** fact keeps every value it has had, and a change is drift; a **volatile** one keeps only its latest value and never counts as drift. A check stores none.

| Fact | Kind | Type | Description |
| --- | --- | --- | --- |
| `gnmi.version` | stable | string | The gNMI version the server says it implements, such as 0.7.0; empty when it names none. |
| `gnmi.encodings` | stable | list | The encodings the server says it answers in, by gNMI's names lowercased (json, bytes, proto, ascii, json_ietf), in gNMI's own order. A server that answers PROTO alone (Stratum, measured 2026-11-15) refuses a Get in JSON. |
| `gnmi.models` | stable | list | Every model the server lists, each name@version (name alone for a model with no version), as the server wrote them, sorted; an entry that is not printable or is too long is left out, with a warning. |
| `gnmi.model_count` | stable | integer | How many models the server lists, as gnmi.models counts them. |
| `gnmi.models_digest` | stable | string | The SHA-256 (hex) of the model list, each model's name, organization and version, sorted, so any change to what the server says it implements reads as a change of this one value. It is not yang.modules_digest, which only a YANG library gives. |
| `yang.modules` | stable | list | Every YANG module the device's gNMI Capabilities lists a model for, each name@revision, or name alone where the model's version is not a revision date (an OpenConfig model's semantic version, such as 2.4.1, is not one), sorted. A model named by a URN ending in :module is read as that module. gNMI's Capabilities is not a YANG library, so this list is never complete: yang.completeness is always partial. The same key net.netconf.facts and net.restconf.facts record from the device's YANG library. |
| `yang.module_count` | stable | integer | How many modules yang.modules lists. |
| `yang.completeness` | stable | string | Always partial for a gNMI read: Capabilities lists the models a server chose to name, with no namespace and nothing in the protocol making it every module the device implements, so a module it does not list may still be served. A read over NETCONF or RESTCONF, from the YANG library, is what can say complete. |

## See also

- `net.gnmi.facts`
- `net.restconf.config`
- `net.netconf.config`

## Examples

Describe and enable a loopback:

```yaml
- name: Describe and enable lo0
  net.gnmi.config:
    update:
      - path: /interface[name=lo0]
        val:
          admin-state: enable
          description: managed by The Pleiades
```

Remove a network instance:

```yaml
- name: Remove the lab VRF
  net.gnmi.config:
    delete:
      - /network-instance[name=lab]
```

