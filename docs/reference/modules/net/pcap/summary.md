---
status: beta
---

# net.pcap.summary

Counts a packet capture in the project by protocol and address pair, encrypted apart from cleartext.

Reads a pcap or pcapng file from the project (virt.cml.capture.stop writes one; up to 16 MiB) and counts every frame by protocol: IPsec's ESP, ESP inside UDP 4500 (NAT traversal) and IKE (UDP 500, or 4500 behind the non-ESP marker), and in the clear GRE, ICMP, OSPF, EIGRP, BGP, HSRP, VRRP, PIM, TCP, UDP, ARP and frames carrying no IP (CDP, keepalives). Each IP packet is one of three kinds: encrypted (ESP, ESP in UDP), the key exchange that set it up (IKE), or cleartext (everything else carried over IP, which anyone on the wire can read). The same counts are given for each address pair, most packets first, so "the sites' traffic crosses encrypted" is a pair whose cleartext is 0 and whose encrypted count grew while traffic was sent; GRE in the clear between two sites is DMVPN without its IPsec. It reads Ethernet (VLAN tags included), raw IP and Linux cooked captures; a frame of another link type is counted as unread. A file cut short is read up to the cut, and the task fails naming where. It changes nothing, and a check runs it as a run does. It runs on this machine with no device: write it without hosts.

## Attributes

|  |  |
| --- | --- |
| Capabilities | - |
| Transports | - |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on no device, so a task skips the runbook's hosts: and runs once |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `src` | `string` | yes | - | The capture file, a path relative to the project directory, such as captures/core1-wan.pcap. |
| `pairs` | `int` | no | `20` | How many address pairs to list, most packets first; 0 lists none. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `format` | `string` | always | pcap or pcapng. |
| `packets` | `int` | always | Every frame in the capture. |
| `bytes` | `int` | always | Their lengths on the wire, summed. |
| `protocols` | `dict` | always | Packets and bytes by protocol name (esp, esp-udp, ike, gre, icmp, ospf, bgp, tcp, udp, arp, non-ip and the rest). |
| `encrypted` | `int` | always | IP packets that were ESP or ESP in UDP. |
| `key_exchange` | `int` | always | IP packets that were IKE. |
| `cleartext` | `int` | always | Every other IP packet: what a reader of the wire saw in the clear. |
| `pairs` | `list of dict` | always | Each address pair (a, b), most packets first: its protocols, encrypted, key_exchange and cleartext. |
| `link_types` | `list of string` | always | The link types the capture declares. |
| `unread` | `int` | always | Frames of a link type this method does not read. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads a capture file and changes nothing, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.cml.capture.start`
- `virt.cml.capture.stop`

## Examples

Count what crossed a WAN link:

```yaml
- name: Count what crossed CORE1's WAN link
  net.pcap.summary:
    src: captures/core1-wan.pcap
  register: wan
```

