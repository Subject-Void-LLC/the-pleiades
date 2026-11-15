---
status: beta
---

# net.restconf.facts

Records what a device's RESTCONF server says about it: its root, capabilities, datastores and YANG modules.

Reads the device's RESTCONF root from its /.well-known/host-meta (RFC 8040 section 3.1), never assuming /restconf, then the capabilities ietf-restconf-monitoring lists and the device's YANG library (RFC 8525's yang-library, or RFC 7895's modules-state from a device that does not serve it), and records them, changing nothing: the same yang.* facts net.netconf.facts records over NETCONF, so a device reads alike over either protocol, and RESTCONF's own root, capabilities and datastores. The device is reached at its own origin with its own TLS (always verified, a pinned certificate included) and its own stored credential, following no redirect, and a root its host-meta names on another scheme, host or port is refused. A device reached over http:// records its findings as stats only, never as facts, since nothing proves which server answered. Reports no change, and a check runs it for real.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `RestconfCapable` |
| Transports | `restconf` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Absence | No exists return: it describes the device itself; a device not there is unreachable, not absent |
| Engine version | `>=0.4.0` |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `restconf.root` | `string` | on every read, as a stat; as a fact only when the device was reached over https | The RESTCONF root the device's /.well-known/host-meta names, as an escaped path (such as /restconf), or / for a root at the device's origin itself. |
| `restconf.capabilities` | `list of string` | on every read, as a stat; as a fact only when the device was reached over https | Every capability URI the device's ietf-restconf-monitoring lists (RFC 8040 section 9.1), with its parameters, sorted. Empty, with a warning, when the list could not be read or does not fit in 60 KiB; an entry that is not printable is left out whole. |
| `restconf.datastores` | `list of string` | on every read, as a stat; as a fact only when the device was reached over https | The datastores the device's YANG library lists, sorted: an NMDA device's running, intended and operational, which RFC 8527 addresses under the root's /ds/; empty for a library that lists none, as RFC 7895's modules-state does. |
| `yang.modules` | `list of string` | when the list fits in 60 KiB as JSON | Every YANG module the device implements, each as name@revision (the name alone for a module with no revision), sorted, read from the device's YANG library over RESTCONF (RFC 8525's yang-library, or RFC 7895's modules-state from a device that does not serve it). An entry that is not a YANG identifier, a revision date or a printable namespace is left out whole, never cut short. Left out when the list does not fit; yang.module_count and yang.modules_digest are always recorded. |
| `yang.module_count` | `int` | on every read, as a stat; as a fact only when the device was reached over https | How many YANG modules the device implements, as yang.modules counts them. |
| `yang.modules_digest` | `string` | on every read, as a stat; as a fact only when the device was reached over https | The SHA-256 (hex) of the module set in its canonical form (every module's name, namespace, revision, features and deviations, sorted, as JSON), so two devices implementing the same modules read alike and any change to the set reads as a change of this one value. |
| `yang.completeness` | `string` | on every read, as a stat; as a fact only when the device was reached over https | How far yang.modules is every module the device implements: complete (the YANG library read whole), partial (a library that dropped a malformed or oversized entry), or unknown (a library that could not be read, or came back empty, as it does for an account the device's access control hides it from). |
| `yang.content_id` | `string` | when the device's YANG library names one | The YANG library's content-id (RFC 8525's content-id, or RFC 7895's module-set-id) from the library itself, a hint the device may regenerate on restart; yang.modules_digest is the module set's identity. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A read-only fact gatherer changes nothing on the device, so there is nothing to undo.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## Facts it keeps

A real run stores each of these as the device's fact. A **stable** fact keeps every value it has had, and a change is drift; a **volatile** one keeps only its latest value and never counts as drift. A check stores none.

| Fact | Kind | Type | Description |
| --- | --- | --- | --- |
| `restconf.root` | stable | string | The RESTCONF root the device's /.well-known/host-meta names, as an escaped path (such as /restconf), or / for a root at the device's origin itself. |
| `restconf.capabilities` | stable | list | Every capability URI the device's ietf-restconf-monitoring lists (RFC 8040 section 9.1), with its parameters, sorted. Empty, with a warning, when the list could not be read or does not fit in 60 KiB; an entry that is not printable is left out whole. |
| `restconf.datastores` | stable | list | The datastores the device's YANG library lists, sorted: an NMDA device's running, intended and operational, which RFC 8527 addresses under the root's /ds/; empty for a library that lists none, as RFC 7895's modules-state does. |
| `yang.modules` | stable | list | Every YANG module the device implements, each as name@revision (the name alone for a module with no revision), sorted, read from the device's YANG library over RESTCONF (RFC 8525's yang-library, or RFC 7895's modules-state from a device that does not serve it). An entry that is not a YANG identifier, a revision date or a printable namespace is left out whole, never cut short. Left out when the list does not fit; yang.module_count and yang.modules_digest are always recorded. |
| `yang.module_count` | stable | integer | How many YANG modules the device implements, as yang.modules counts them. |
| `yang.modules_digest` | stable | string | The SHA-256 (hex) of the module set in its canonical form (every module's name, namespace, revision, features and deviations, sorted, as JSON), so two devices implementing the same modules read alike and any change to the set reads as a change of this one value. |
| `yang.completeness` | stable | string | How far yang.modules is every module the device implements: complete (the YANG library read whole), partial (a library that dropped a malformed or oversized entry), or unknown (a library that could not be read, or came back empty, as it does for an account the device's access control hides it from). |
| `yang.content_id` | volatile | string | The YANG library's content-id (RFC 8525's content-id, or RFC 7895's module-set-id) from the library itself, a hint the device may regenerate on restart; yang.modules_digest is the module set's identity. |

## See also

- `net.netconf.facts`
- `http.request`

## Examples

Read what a device offers over RESTCONF:

```yaml
- name: Discover the device's YANG modules over RESTCONF
  net.restconf.facts:
  register: discovered
```

