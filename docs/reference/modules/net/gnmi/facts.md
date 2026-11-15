---
status: beta
---

# net.gnmi.facts

Records what a device's gNMI server says about itself: its gNMI version, encodings and models.

Asks the device's gNMI server its Capabilities (gNMI specification section 3.2) and records what it says, changing nothing: the gNMI version, the encodings it answers in and the models it implements, in the gnmi.* family, beside the protocol-neutral yang.* facts net.netconf.facts and net.restconf.facts record from a YANG library. gNMI's Capabilities is not a YANG library (no namespace, and a model's own version in place of a revision when it is no date), so yang.completeness is always partial and no yang.modules_digest is recorded; gnmi.models_digest is the model list's own. The device is reached at its own target with its own TLS (always verified, a pinned certificate included, mutual TLS from the stored certificate) and its own stored credential, as the gNMI specification's username and password metadata, a bearer token, or none; a refused login is sent once and never again. A device reached over plaintext gRPC (grpc_plaintext) records its findings as stats only, never as facts, since nothing proves which server answered, and every run says so. Reports no change, and a check runs it for real.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `GNMICapable` |
| Transports | `gnmi` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Absence | No exists return: it describes the device itself; a device not there is unreachable, not absent |
| Engine version | `>=0.4.0` |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `gnmi.version` | `string` | on every read, as a stat; as a fact only when the device was reached over TLS | The gNMI version the server says it implements, such as 0.7.0; empty when it names none. |
| `gnmi.encodings` | `list of string` | on every read, as a stat; as a fact only when the device was reached over TLS | The encodings the server says it answers in, by gNMI's names lowercased (json, bytes, proto, ascii, json_ietf), in gNMI's own order. A server that answers PROTO alone (Stratum, measured 2026-11-15) refuses a Get in JSON. |
| `gnmi.models` | `list of string` | on every read, as a stat; as a fact only when the device was reached over TLS, when the list fits in 60 KiB | Every model the server lists, each name@version (name alone for a model with no version), as the server wrote them, sorted; an entry that is not printable or is too long is left out, with a warning. |
| `gnmi.model_count` | `int` | on every read, as a stat; as a fact only when the device was reached over TLS | How many models the server lists, as gnmi.models counts them. |
| `gnmi.models_digest` | `string` | on every read, as a stat; as a fact only when the device was reached over TLS | The SHA-256 (hex) of the model list, each model's name, organization and version, sorted, so any change to what the server says it implements reads as a change of this one value. It is not yang.modules_digest, which only a YANG library gives. |
| `yang.modules` | `list of string` | on every read, as a stat; as a fact only when the device was reached over TLS, when the list fits in 60 KiB | Every YANG module the device's gNMI Capabilities lists a model for, each name@revision, or name alone where the model's version is not a revision date (an OpenConfig model's semantic version, such as 2.4.1, is not one), sorted. A model named by a URN ending in :module is read as that module. gNMI's Capabilities is not a YANG library, so this list is never complete: yang.completeness is always partial. The same key net.netconf.facts and net.restconf.facts record from the device's YANG library. |
| `yang.module_count` | `int` | on every read, as a stat; as a fact only when the device was reached over TLS | How many modules yang.modules lists. |
| `yang.completeness` | `string` | on every read, as a stat; as a fact only when the device was reached over TLS | Always partial for a gNMI read: Capabilities lists the models a server chose to name, with no namespace and nothing in the protocol making it every module the device implements, so a module it does not list may still be served. A read over NETCONF or RESTCONF, from the YANG library, is what can say complete. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A read-only fact gatherer changes nothing on the device, so there is nothing to undo.

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

- `net.gnmi.config`
- `net.netconf.facts`
- `net.restconf.facts`

## Examples

Read what a device offers over gNMI:

```yaml
- name: Discover the device's gNMI models
  net.gnmi.facts:
  register: discovered
```

