---
status: beta
---

# net.snmp.set

Writes one object on a device's SNMP agent, reading it before and after, with an exact undo.

Writes value, of type, to the object oid names on the device's SNMP agent in one SET, with the SNMP family of the device's own credential: SNMPv3 unless the device's record opts into SNMPv1 or SNMPv2c (snmp_allow_cleartext_community), which is warned about, as a write in the clear with a community is. The object is read first. One the agent does not hold is refused, since creating a row is not this method's (its undo would not be exact), and so is one holding another type than type. One that already holds value is left alone: nothing is sent and the run reports no change, and a check reports what a run would change and sends no SET. After the SET the object is read again, and a value other than the one written fails the task: an agent can accept a write and drop it (IOS XE 17.15.1 accepted SNMPv3 users it never created, measured on 2026-11-15), so the answer to the SET is not taken as the change. A run that changed the object records its undo, net.snmp.set of the same OID with the type and value read before, as soon as the SET is answered. Every object under snmpModules (1.3.6.1.6.3), the agent's own users, keys, views and communities, is refused before anything is sent: their values would ride the undo and the run's journal. An OID is numeric or a curated name with its instance (sysContact.0), as net.snmp.get takes it. The values recorded are masked of the device's credential.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `SNMPCapable` |
| Transports | `snmp` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.4.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `oid` | `string` | yes | - | The object to write, one instance: numeric (1.3.6.1.2.1.1.4.0) or a curated name with its instance (sysContact.0). |
| `type` | `string` | yes | - | The value's SNMP type: integer (a signed 32-bit number), octet-string (text, written as its bytes), hex-string (an octet string given as hex, optionally after 0x, for one that is not text), object-identifier, ip-address (IPv4, dotted), gauge32 or timeticks (unsigned 32-bit numbers). Counters are read-only (RFC 2578) and are not offered. It must be the type the agent holds. **Choices:** `integer`, `octet-string`, `hex-string`, `object-identifier`, `ip-address`, `gauge32`, `timeticks`. |
| `value` | `string` | yes | - | The value, as text of its type: a number in decimal, an OID dotted or by curated name, an address dotted, text, or hex. An octet string is at most 65535 bytes. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `diff` | `dict` | always | The object before and after, each its oid, type and value (masked), so a run that changed nothing shows the same value twice. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that changed the object records net.snmp.set of the same OID with the type and value read before the write, which is exact: an SNMP object holds one typed value, and writing it back restores it. It is recorded as soon as the SET is answered, so it stands whatever happens after, the read back included. The value read before is kept in the run's journal, which is why net.snmp.set refuses every object under snmpModules (1.3.6.1.6.3), where an agent keeps its users, keys and communities; a value holding one of the run's secrets is withheld by the journal, and such an undo is not replayed. A run that changed nothing records nothing.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.snmp.get`
- `net.snmp.facts`

## Examples

Set a device's contact:

```yaml
- name: Name the device's contact
  net.snmp.set:
    oid: sysContact.0
    type: octet-string
    value: noc@example.net
```

Shut an interface down:

```yaml
- name: Set ifAdminStatus of interface 3 to down
  net.snmp.set:
    oid: ifAdminStatus.3
    type: integer
    value: "2"
```

