---
status: beta
---

# net.pcap.summary

Counts a packet capture in the project by protocol and address pair, encrypted apart from cleartext.

Reads a pcap or pcapng file from the project (virt.cml.capture.stop writes one; up to 16 MiB) and counts every frame by protocol: IPsec's ESP, ESP inside UDP 4500 (NAT traversal) and IKE (UDP 500, or 4500 behind the non-ESP marker), and in the clear GRE, ICMP, OSPF, EIGRP, BGP, HSRP, VRRP, PIM, TCP, UDP, ARP and frames carrying no IP (CDP, keepalives). Each TCP connection is also read from its own bytes, both ways in sequence order, and never judged by its port: ssh (an SSH-2.0 banner, its key exchange, then everything after NEWKEYS), tls (a ClientHello or ServerHello record, then each record by its content type), http2 (HTTP/2's client preface in the clear) and grpc (http2 whose request says application/grpc). Each IP packet is one of three kinds: encrypted (ESP, ESP in UDP, SSH after NEWKEYS, TLS application data), the key exchange that set it up (IKE, SSH's banner and key exchange, TLS handshake records), or cleartext (everything else carried over IP, which anyone on the wire can read). A packet with no data in an encrypted connection (a SYN, a bare ACK) is counted encrypted. The same counts are given for each address pair, most packets first, so "the sites' traffic crosses encrypted" is a pair whose cleartext is 0 and whose encrypted count grew while traffic was sent; GRE in the clear between two sites is DMVPN without its IPsec. HTTP/2 in the clear is cleartext management: it is listed first in flows and a run warns about it. Its header blocks are decoded (HPACK compresses header values, so a search of the bytes finds nothing), and a header that carries a credential (authorization, username, password, a token, a cookie, or one its sender marked never to be indexed) is reported by its name and its connection, never its value, in cleartext_credentials, flows and a warning. Nothing is called encrypted that was not seen to be: a connection whose start the capture lacks, one whose bytes stop making sense as SSH or TLS, and the bytes after a lost segment that hid a record's or packet's header are cleartext (TLS is read again from the next segment that begins with a record). SSH and TLS are known by their framing, not their cipher, so TLS with a NULL cipher suite is counted encrypted, as ESP-NULL is. UDP is read no further than its ports, so DTLS and QUIC are cleartext UDP. It reads Ethernet (VLAN tags included), raw IP and Linux cooked captures; a frame of another link type is counted as unread. A frame carrying no IP (CDP, a keepalive, but also anything inside MPLS or PPPoE) and an unread frame are not examined, so they are counted apart as unexamined, never as cleartext: "nothing crossed in the clear" is cleartext 0 and unexamined 0 (or every unexamined frame accounted for by its protocol). IKE and ESP in UDP are told by their ports, and ESP is counted encrypted whatever its cipher, ESP-NULL included. A file cut short is read up to the cut, and the task fails naming where. It changes nothing, and a check runs it as a run does. It runs on this machine with no device: write it without hosts.

## Attributes

|  |  |
| --- | --- |
| Capabilities | - |
| Transports | - |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on no device, so a task skips the runbook's hosts: and runs once |
| Check mode | Supported: reports what it would change and changes nothing |
| Absence | No exists return: it summarizes the capture file the task names; a file not there is refused input, not a lookup |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `src` | `string` | yes | - | The capture file, a path relative to the project directory, such as captures/core1-wan.pcap. |
| `pairs` | `int` | no | `20` | How many address pairs to list, most packets first; 0 lists none. |
| `flows` | `int` | no | `20` | How many TCP connections named from their bytes to list in flows, those in the clear first; 0 lists none. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `format` | `string` | always | pcap or pcapng. |
| `packets` | `int` | always | Every frame in the capture. |
| `bytes` | `int` | always | Their lengths on the wire, summed. |
| `protocols` | `dict` | always | Packets and bytes by protocol name (esp, esp-udp, ike, gre, icmp, ospf, bgp, ssh, tls, http2, grpc, tcp, udp, arp, non-ip and the rest). |
| `encrypted` | `int` | always | IP packets that were ESP, ESP in UDP, SSH after NEWKEYS or TLS application data, and those carrying no data in an encrypted connection. |
| `key_exchange` | `int` | always | IP packets that were IKE, SSH's banner and key exchange, or TLS handshake records. |
| `cleartext` | `int` | always | Every other IP packet: what a reader of the wire saw in the clear. |
| `pairs` | `list of dict` | always | Each address pair (a, b), most packets first: its protocols, encrypted, key_exchange and cleartext. |
| `flows` | `list of dict` | always | Each TCP connection named from its bytes (ssh, tls, http2, grpc), those that carried a credential in the clear first, then other cleartext management, then most packets first: client and server (address:port; the client sent the first SYN), protocol, packets, encrypted, key_exchange, cleartext, cleartext_management, and cleartext_credentials (the names of the headers only). |
| `cleartext_credentials` | `int` | always | Headers that carried a credential in the clear, one for each header name on each connection; flows names them. |
| `cleartext_management` | `int` | always | Connections of a management protocol (http2, grpc) that crossed in the clear. |
| `warnings` | `list of string` | when a credential or management traffic crossed in the clear | One line for each connection that did, naming its headers but never a value, at most ten and then a count of the rest. |
| `link_types` | `list of string` | always | The link types the capture declares. |
| `unread` | `int` | always | Frames of a link type this method does not read. |
| `unexamined` | `int` | always | Frames this method could not judge encrypted or not: unread ones, and every one carrying no IP (non-ip). |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads a capture file and changes nothing, so there is nothing to undo.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

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

