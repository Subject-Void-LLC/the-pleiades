---
status: beta
---

# net.netconf.facts

Records what a NETCONF session discovers about a device: its capabilities, datastores and YANG modules.

Opens an RFC 6241 NETCONF session over the SSH "netconf" subsystem, exactly as net.netconf.config does, and records what the session discovered, changing nothing: every capability the hello advertised that names no YANG module, the datastores the session could use, and the YANG modules the device implements, read from its YANG library (RFC 8525, or RFC 7895's modules-state) when it advertises one, since a YANG 1.1 device need not list its modules in the hello. The same facts every net.netconf.config session records, so a device read on a schedule shows a change in what it offers (IOS XE drops :writable-running when its candidate datastore is turned on) as drift. A session whose host key was not verified records its findings as stats only, never as facts. Reports no change, and a check runs it for real.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `NetconfCapable`, `SSHTransportCapable` |
| Transports | `netconf` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Absence | No exists return: it describes the device itself; a device not there is unreachable, not absent |
| Engine version | `>=0.4.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it; nothing the session discovers is then recorded as a fact. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `netconf.capabilities` | `list of string` | on every session, as a stat; as a fact only when its host key was verified | Every capability the device's hello advertised that names no YANG module (RFC 6241's own and a vendor's), with its parameters but the volatile content-id and module-set-id, which yang.content_id holds, sorted; the YANG module capabilities are in yang.modules instead. Left out, with a warning, when it does not fit in 60 KiB. Read on every session, so a device that changes what it offers (IOS XE drops :writable-running when its candidate datastore is turned on) reads as a change. |
| `netconf.datastores` | `list of string` | on every session, as a stat; as a fact only when its host key was verified | The datastores the session could use, sorted: running always, candidate and startup when the hello advertises them, and every datastore the device's YANG library lists (an NMDA device's operational and intended). |
| `yang.modules` | `list of string` | when the list fits in 60 KiB as JSON | Every YANG module the device implements, each as name@revision (the name alone for a module with no revision), sorted, read from the device's YANG library (RFC 8525, or RFC 7895's modules-state) when it advertises one and from the hello otherwise. An entry that is not a YANG identifier, a revision date or a printable namespace is left out whole, never cut short. Left out when the list does not fit; yang.module_count and yang.modules_digest are always recorded. |
| `yang.module_count` | `int` | on every session, as a stat; as a fact only when its host key was verified | How many YANG modules the device implements, as yang.modules counts them. |
| `yang.modules_digest` | `string` | on every session, as a stat; as a fact only when its host key was verified | The SHA-256 (hex) of the module set in its canonical form (every module's name, namespace, revision, features and deviations, sorted, as JSON), so two devices implementing the same modules read alike and any change to the set reads as a change of this one value. |
| `yang.completeness` | `string` | on every session, as a stat; as a fact only when its host key was verified | How far yang.modules is every module the device implements: complete (read from the YANG library, or from the hello of a device with none), partial (a library that could not be read, or came back empty for an account the device's access control hides it from), or unknown (a device that named no modules). A document is refused for a namespace the device does not serve only when this is complete. |
| `yang.content_id` | `string` | when the device advertises yang-library:1.1 | The YANG library's content-id from the hello, a hint the device may regenerate on restart; yang.modules_digest is the module set's identity. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A read-only fact gatherer changes nothing on the device, so there is nothing to undo.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## Facts it keeps

A real run stores each of these as the device's fact. A **stable** fact keeps every value it has had, and a change is drift; a **volatile** one keeps only its latest value and never counts as drift. A check stores none.

| Fact | Kind | Type | Description |
| --- | --- | --- | --- |
| `netconf.capabilities` | stable | list | Every capability the device's hello advertised that names no YANG module (RFC 6241's own and a vendor's), with its parameters but the volatile content-id and module-set-id, which yang.content_id holds, sorted; the YANG module capabilities are in yang.modules instead. Left out, with a warning, when it does not fit in 60 KiB. Read on every session, so a device that changes what it offers (IOS XE drops :writable-running when its candidate datastore is turned on) reads as a change. |
| `netconf.datastores` | stable | list | The datastores the session could use, sorted: running always, candidate and startup when the hello advertises them, and every datastore the device's YANG library lists (an NMDA device's operational and intended). |
| `yang.modules` | stable | list | Every YANG module the device implements, each as name@revision (the name alone for a module with no revision), sorted, read from the device's YANG library (RFC 8525, or RFC 7895's modules-state) when it advertises one and from the hello otherwise. An entry that is not a YANG identifier, a revision date or a printable namespace is left out whole, never cut short. Left out when the list does not fit; yang.module_count and yang.modules_digest are always recorded. |
| `yang.module_count` | stable | integer | How many YANG modules the device implements, as yang.modules counts them. |
| `yang.modules_digest` | stable | string | The SHA-256 (hex) of the module set in its canonical form (every module's name, namespace, revision, features and deviations, sorted, as JSON), so two devices implementing the same modules read alike and any change to the set reads as a change of this one value. |
| `yang.completeness` | stable | string | How far yang.modules is every module the device implements: complete (read from the YANG library, or from the hello of a device with none), partial (a library that could not be read, or came back empty for an account the device's access control hides it from), or unknown (a device that named no modules). A document is refused for a namespace the device does not serve only when this is complete. |
| `yang.content_id` | volatile | string | The YANG library's content-id from the hello, a hint the device may regenerate on restart; yang.modules_digest is the module set's identity. |

## See also

- `net.netconf.config`
- `net.ios.facts`

## Examples

Read what a device offers over NETCONF:

```yaml
- name: Discover the device's YANG modules and datastores
  net.netconf.facts:
  register: discovered
```

