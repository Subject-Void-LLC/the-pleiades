---
status: beta
---

# net.snmp.facts

Records what a device's SNMP agent says about it: its system group, engine and interfaces.

Reads the device's SNMP agent with the SNMP family of its own credential, through the client onboarding and wait.snmp use (SNMPv3 unless the device's record opts into SNMPv1 or SNMPv2c with snmp_allow_cleartext_community, which is warned about): the MIB-II system group in one GET, the agent's engine ID (the one SNMPv3 discovered, or snmpEngineID.0 for a community) and boots, and the interface table, ifTable's index, description, type, MTU, speed, address and statuses and ifXTable's name, alias and high speed, walked column by column up to max_interfaces rows. It records them, changing nothing, as stats always and as facts only when the session authenticated the agent's answers (SNMPv3 at authNoPriv or authPriv): a community, or a noAuthNoPriv user, proves nothing about which agent answered, so what it says is not kept, and the run says so. A group the agent does not answer is left out, never recorded empty, and an agent that answers no system object at all fails the task. snmp.interfaces is left out, with a warning, when it does not fit in 60 KiB; snmp.interface_count is recorded whenever the table was read. Every text value is masked of the device's credential. Reports no change, and a check runs it for real.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `SNMPCapable` |
| Transports | `snmp` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Absence | No exists return: it describes the device itself; a device not there is unreachable, not absent |
| Engine version | `>=0.4.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `max_interfaces` | `int` | no | `1000` | The most interfaces read, from 1 to 10000; a table with more is recorded as its first rows, with a warning. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `snmp.version` | `string` | always, as a stat; as a fact only over SNMPv3 with authentication | The SNMP version the agent was read in: 3, 2c or 1. |
| `snmp.security_level` | `string` | always, as a stat; as a fact only over SNMPv3 with authentication | The SNMPv3 security level the session ran at (authPriv, authNoPriv or noAuthNoPriv), or community for SNMPv1 and SNMPv2c. |
| `snmp.sys_descr` | `string` | when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication | sysDescr.0: the agent's description of the device, usually its hardware and software. |
| `snmp.sys_object_id` | `string` | when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication | sysObjectID.0, dotted: the vendor's identifier of the device's kind, under its enterprise number (1.3.6.1.4.1.9 is Cisco's, 1.3.6.1.4.1.8072 net-snmp's). |
| `snmp.sys_name` | `string` | when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication | sysName.0: the name the device's administrator gave it. |
| `snmp.sys_contact` | `string` | when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication | sysContact.0: who to contact about the device. |
| `snmp.sys_location` | `string` | when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication | sysLocation.0: where the device is. |
| `snmp.sys_services` | `int` | when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication | sysServices.0: the layers the device serves, as RFC 3418's sum of each layer's value (72 for a host offering applications, 78 for one that also bridges and routes). |
| `snmp.sys_uptime` | `int` | when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication | sysUpTime.0, in hundredths of a second since the agent's network management part started: it changes on every read, and goes back to 0 on a restart. |
| `snmp.engine_id` | `string` | when the agent has one it says, as a stat; as a fact only over SNMPv3 with authentication | The agent's SNMP engine ID as lowercase hex: the one SNMPv3 discovered for the session, or snmpEngineID.0 for a community. An engine ID names one agent, so a new one at the same address is another agent, or the same one reinstalled. |
| `snmp.engine_boots` | `int` | when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication | snmpEngineBoots.0: how many times the agent's engine has started since its engine ID was set. |
| `snmp.interface_count` | `int` | when the interface table was read, as a stat; as a fact only over SNMPv3 with authentication | How many interfaces the table holds, as read (at most max_interfaces). |
| `snmp.interfaces` | `list of dict` | when the interface table was read and fits in 60 KiB, as a stat; as a fact only over SNMPv3 with authentication | Each interface, by ifIndex order: index, descr, name, alias, type (IANAifType's number), mtu, speed (bits per second, from ifSpeed, or ifHighSpeed times a million when ifSpeed is at its 32-bit ceiling), phys_address (hex), admin_status and oper_status (up, down, testing and RFC 2863's others, or the number for one it does not name), each left out when the agent does not answer it. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A read-only fact gatherer changes nothing on the device, so there is nothing to undo.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## Facts it keeps

A real run stores each of these as the device's fact. A **stable** fact keeps every value it has had, and a change is drift; a **volatile** one keeps only its latest value and never counts as drift. A check stores none.

| Fact | Kind | Type | Description |
| --- | --- | --- | --- |
| `snmp.version` | stable | string | The SNMP version the agent was read in: 3, 2c or 1. |
| `snmp.security_level` | stable | string | The SNMPv3 security level the session ran at (authPriv, authNoPriv or noAuthNoPriv), or community for SNMPv1 and SNMPv2c. |
| `snmp.sys_descr` | stable | string | sysDescr.0: the agent's description of the device, usually its hardware and software. |
| `snmp.sys_object_id` | stable | string | sysObjectID.0, dotted: the vendor's identifier of the device's kind, under its enterprise number (1.3.6.1.4.1.9 is Cisco's, 1.3.6.1.4.1.8072 net-snmp's). |
| `snmp.sys_name` | stable | string | sysName.0: the name the device's administrator gave it. |
| `snmp.sys_contact` | stable | string | sysContact.0: who to contact about the device. |
| `snmp.sys_location` | stable | string | sysLocation.0: where the device is. |
| `snmp.sys_services` | stable | integer | sysServices.0: the layers the device serves, as RFC 3418's sum of each layer's value (72 for a host offering applications, 78 for one that also bridges and routes). |
| `snmp.sys_uptime` | volatile | integer | sysUpTime.0, in hundredths of a second since the agent's network management part started: it changes on every read, and goes back to 0 on a restart. |
| `snmp.engine_id` | stable | string | The agent's SNMP engine ID as lowercase hex: the one SNMPv3 discovered for the session, or snmpEngineID.0 for a community. An engine ID names one agent, so a new one at the same address is another agent, or the same one reinstalled. |
| `snmp.engine_boots` | volatile | integer | snmpEngineBoots.0: how many times the agent's engine has started since its engine ID was set. |
| `snmp.interface_count` | stable | integer | How many interfaces the table holds, as read (at most max_interfaces). |
| `snmp.interfaces` | volatile | list | Each interface, by ifIndex order: index, descr, name, alias, type (IANAifType's number), mtu, speed (bits per second, from ifSpeed, or ifHighSpeed times a million when ifSpeed is at its 32-bit ceiling), phys_address (hex), admin_status and oper_status (up, down, testing and RFC 2863's others, or the number for one it does not name), each left out when the agent does not answer it. |

## See also

- `net.snmp.get`
- `wait.snmp`

## Examples

Record what a device's agent says:

```yaml
- name: Read the agent's system group and interfaces
  net.snmp.facts:
  register: agent
```

