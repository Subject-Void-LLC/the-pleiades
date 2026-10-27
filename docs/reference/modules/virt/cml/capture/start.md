---
status: beta
---

# virt.cml.capture.start

Starts a packet capture on a link of a CML lab.

Starts the controller capturing every packet that crosses the link named by link, or the one between a's a_interface and b's b_interface, until packets have been taken or seconds have passed (at least one is required; CML allows 1 to 1000000 packets and 1 to 86400 seconds), optionally only those a Berkeley packet filter (tcpdump's syntax) admits. The capture runs on the controller while the run goes on, so start it, send the traffic to prove (net.ios.ping), then stop it with virt.cml.capture.stop, which writes the file into the project for net.pcap.summary or another analyzer to read. A link that is already capturing is refused: stop that capture first. Its undo is virt.cml.capture.stop of the same link, which writes nothing. The link must be started (its lab started); the task's target is a cml_controller device. A check reads the link and its capture and changes nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CMLAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `lab` | `string` | yes | - | The lab, by id or by title. A title two labs share is refused, naming both ids; an undo always names the id. |
| `link` | `string` | no | - | The link's id. |
| `a` | `string` | no | - | One node, by label or id, with a_interface, b and b_interface, naming the link by its ends. |
| `a_interface` | `string` | no | - | Its interface, by label. |
| `b` | `string` | no | - | The other node, by label or id. |
| `b_interface` | `string` | no | - | Its interface, by label. |
| `packets` | `int` | no | - | Stop after this many packets. |
| `seconds` | `int` | no | - | Stop after this many seconds. |
| `filter` | `string` | no | - | A Berkeley packet filter, at most 128 characters, such as "esp or udp port 500 or udp port 4500 or icmp". |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `id` | `string` | always | The link's id. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that started a capture emits virt.cml.capture.stop of the same link, which ends it and writes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.cml.capture.stop`
- `net.pcap.summary`
- `virt.cml.link.stop`

## Examples

Prove a WAN link carries only encrypted traffic:

```yaml
- name: Capture CORE1's WAN link
  virt.cml.capture.start:
    lab: cvd
    a: CORE1
    a_interface: Ethernet0/1
    b: WAN1
    b_interface: Ethernet0/0
    packets: 2000
    seconds: 60
- name: Ping across the tunnel from a branch
  net.ios.ping:
    target: BR1
    dest: 10.0.0.1
    source: 10.1.1.1
    count: 50
- name: Stop it and keep the file
  virt.cml.capture.stop:
    lab: cvd
    a: CORE1
    a_interface: Ethernet0/1
    b: WAN1
    b_interface: Ethernet0/0
    dest: captures/core1-wan.pcap
- name: Count what crossed
  net.pcap.summary:
    src: captures/core1-wan.pcap
```

