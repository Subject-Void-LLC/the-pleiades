---
status: beta
---

# net.snmp.get

Reads OIDs from a device's SNMP agent, in one GET or each walked, changing nothing.

Reads the OIDs oids names from the device's SNMP agent with the SNMP family of its own credential, through the client onboarding and wait.snmp use: SNMPv3 unless the device's record opts into SNMPv1 or SNMPv2c (snmp_allow_cleartext_community), every one of which is warned about. Without walk the OIDs are read in one GET, at most 60 of them, and an object the agent does not hold is answered as a value of type no-such-object or no-such-instance; an SNMPv1 agent answers that noSuchName instead, which fails the task naming the OID. With walk each OID is the root of a subtree read in order, with GETBULK (unless the device's record sets snmp_no_bulk, or it speaks SNMPv1) or GETNEXT, until the subtree ends or max_results bindings are read, and an agent answering an OID not after the one asked from stops the walk rather than loop. An OID is numeric (1.3.6.1.2.1.1.5.0) or a name from a small curated table with its instance (sysName.0, ifDescr.2, SNMPv2-MIB::sysName.0): the MIB-II system and interfaces groups, IF-MIB's ifXTable and the SNMP engine group; anything else is refused before anything is sent, and is written numerically. Every value is masked of the device's credential, and every octet string in the agent's community table (SNMP-COMMUNITY-MIB) or USM user table is masked whole, whatever it holds. Reports no change, and a check runs it for real.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `SNMPCapable` |
| Transports | `snmp` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Absence | No exists return: it reads OIDs and answers each one, an object the agent does not hold included, as a value of type no-such-object or no-such-instance |
| Engine version | `>=0.4.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `oids` | `list` | yes | - | The OIDs to read, or with walk the roots of the subtrees to walk: numeric, or a curated name with its instance (sysName.0). From 1 to 60 of them. |
| `walk` | `bool` | no | `false` | Walk each OID's subtree instead of reading the OID itself. |
| `max_results` | `int` | no | `1000` | With walk, the most bindings read for each OID, from 1 to 10000; a walk that stops there says so with truncated. Refused without walk. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `values` | `list of dict` | always | Each binding read, in order: oid (numeric), type (integer, octet-string, object-identifier, ip-address, counter32, gauge32, timeticks, counter64, opaque, null, no-such-object, no-such-instance or end-of-mib-view) and value as text (a number in decimal, an OID dotted, an octet string as itself, or as 0x and hex when it is not printable, with hex true). |
| `truncated` | `bool` | with walk | Whether a walk stopped at max_results before its subtree ended, so values may hold only its first part. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A GET or a walk changes nothing on the device, so there is nothing to undo.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.snmp.facts`
- `net.snmp.set`
- `wait.snmp`

## Examples

Read a device's name and uptime:

```yaml
- name: Read the agent's name and uptime
  net.snmp.get:
    oids:
      - sysName.0
      - sysUpTime.0
  register: system
```

Walk the interface descriptions:

```yaml
- name: Walk ifDescr
  net.snmp.get:
    oids:
      - ifDescr
    walk: true
    max_results: 500
```

