---
status: beta
---

# net.pcap.summary

Counts a packet capture in the project by protocol and address pair, encrypted apart from cleartext.

Reads a pcap or pcapng file from the project (virt.cml.capture.stop writes one; up to 16 MiB) and counts every frame by protocol: IPsec's ESP, ESP inside UDP 4500 (NAT traversal) and IKE (UDP 500, or 4500 behind the non-ESP marker), and in the clear GRE, ICMP, OSPF, EIGRP, BGP, HSRP, VRRP, PIM, TCP, UDP, ARP and frames carrying no IP (CDP, keepalives). Each TCP connection is also read from its own bytes, both ways in sequence order, and never judged by its port: ssh (an SSH-2.0 banner, its key exchange, then everything after NEWKEYS), tls (a ClientHello or ServerHello record, then each record by its content type), http (an HTTP/1.x request or status line in the clear), http2 (HTTP/2's client preface in the clear) and grpc (http2 whose request says application/grpc). Each IP packet is one of three kinds: encrypted (ESP, ESP in UDP, SSH after NEWKEYS, TLS application data), the key exchange that set it up (IKE, SSH's banner and key exchange, TLS handshake records), or cleartext (everything else carried over IP, which anyone on the wire can read). A packet with no data in an encrypted connection (a SYN, a bare ACK) is counted encrypted. The same counts are given for each address pair, most packets first, so "the sites' traffic crosses encrypted" is a pair whose cleartext is 0 and whose encrypted count grew while traffic was sent. Whether it crossed inside IPsec is the pair's protocols.esp (and esp-udp), not encrypted, which counts SSH and TLS too; GRE in the clear between two sites is DMVPN without its IPsec. Every IP packet is also counted by its DSCP mark (dscp, over the capture and in each pair and connection), named by its codepoint (EF, AF41, CS6) and by the RFC 4594 service class that codepoint is recommended for (Telephony, Multimedia Conferencing, Network Control). A mark is what the sender or a router on the way wrote, not proof of how any queue treated the packet, and a network that uses a codepoint for another class (call signaling on CS3, where RFC 4594 gives CS3 to broadcast video) still reads RFC 4594's name. The first and last packet times and the seconds between them, over the capture and for each connection, come from the capture's own records; a pcapng simple packet block has no time. UDP is counted by pair and mark, never as connections. HTTP/1.x, HTTP/2 and gRPC in the clear to a management endpoint are cleartext management: listed first in flows, and a run warns about them. A server is a management endpoint when a request on the connection named a management API by its path (RESTCONF's /restconf or /.well-known/host-meta, gNMI's /gnmi.gNMI/, gNOI's /gnoi.), said in the connection's management, or when the management parameter lists it. A port never decides, so a lab's web server on 8080 is cleartext and not management, and a device's own web interface is management once management lists the device. Every HTTP/1.x and HTTP/2 connection's headers, management or not, are searched by name (HTTP/2's decoded from HPACK, which compresses header values, so a search of the bytes finds nothing), and a header that carries a credential (authorization, username, password, a token, a cookie, or one its sender marked never to be indexed) is reported by its name and its connection, never its value, in cleartext_credentials, flows and a warning. Credentials are searched in HTTP/1.x and HTTP/2 headers only: Telnet, FTP, SNMP communities, URL query strings and bodies are not searched, so cleartext_credentials 0 means no credential header was found, not that no credential crossed, and Telnet, FTP and SNMP are not counted in cleartext_management. A header that could not be searched (a block over 64 KiB or one that would not decode, a name over 256 bytes, headers after a gap or a protocol switch, a credential name past the 32 kept for a connection) is counted in headers_unread. Bytes framed as encrypted that read as text are not ciphertext: a TLS application data record, or a 256-byte block of what SSH sent after NEWKEYS, of at least 64 bytes of which three quarters are printable ASCII (random bytes are about 38% printable) makes that direction's packets cleartext and flags the connection cleartext_in_encrypted, which is how a TLS NULL cipher suite, the SSH none cipher and records forged around cleartext are told. Nothing is called encrypted that was not seen to be: a connection whose start the capture lacks, one whose bytes stop making sense as SSH or TLS, the bytes after a lost segment that hid a record's or packet's header (TLS is read again from the next segment that begins with a record), a packet whose data the capture holds none of (a short snapshot length), and a repeated segment whose bytes differ from the first copy are cleartext. UDP is read no further than its ports, so DTLS and QUIC are cleartext UDP. It reads Ethernet (VLAN tags included), raw IP and Linux cooked captures; a frame of another link type is counted as unread. A frame carrying no IP (CDP, a keepalive, but also anything inside MPLS or PPPoE) and an unread frame are not examined, so they are counted apart as unexamined, never as cleartext: "nothing crossed in the clear" is cleartext 0 and unexamined 0 (or every unexamined frame accounted for by its protocol). IKE and ESP in UDP are told by their ports, and ESP is counted encrypted whatever its cipher, ESP-NULL included, since its payload is not read. A file cut short is read up to the cut, and the task fails naming where. It changes nothing, and a check runs it as a run does. It runs on this machine with no device: write it without hosts.

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
| `management` | `list of string` | no | - | The management endpoints the capture may reach, each an address (every port on it) or an address and port (192.0.2.1:80, [2001:db8::1]:443): an HTTP/1.x, HTTP/2 or gRPC connection to one in the clear is cleartext management whatever it asked. RESTCONF, gNMI and gNOI are found by their paths without it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `format` | `string` | always | pcap or pcapng. |
| `packets` | `int` | always | Every frame in the capture. |
| `bytes` | `int` | always | Their lengths on the wire, summed. |
| `protocols` | `dict` | always | Packets and bytes by protocol name (esp, esp-udp, ike, gre, icmp, ospf, bgp, ssh, tls, http, http2, grpc, tcp, udp, arp, non-ip and the rest). Whether traffic crossed inside IPsec is esp and esp-udp here. |
| `encrypted` | `int` | always | IP packets that were ESP, ESP in UDP, SSH after NEWKEYS or TLS application data, and those carrying no data in an encrypted connection. It is not the IPsec count: that is protocols.esp. |
| `key_exchange` | `int` | always | IP packets that were IKE, SSH's banner and key exchange, or TLS handshake records. |
| `cleartext` | `int` | always | Every other IP packet: what a reader of the wire saw in the clear. |
| `pairs` | `list of dict` | always | Each address pair (a, b), most packets first: its protocols, encrypted, key_exchange, cleartext and dscp. |
| `flows` | `list of dict` | always | Each TCP connection named from its bytes (ssh, tls, http, http2, grpc), those that carried a credential in the clear first, then those whose encrypted-looking bytes read as text, then other cleartext management, then most packets first: client and server (address:port; the client sent the first SYN), protocol, packets, bytes (the frames' lengths on the wire, both ways), dscp (both ways), first and last (RFC 3339 times, left out when the capture holds none), duration_seconds, encrypted, key_exchange, cleartext, cleartext_management, management (restconf, gnmi, gnoi or listed, when the server is a management endpoint), cleartext_in_encrypted, cleartext_credentials (the names of the headers only, at most 32) and headers_unread. |
| `cleartext_credentials` | `int` | always | HTTP/1.x and HTTP/2 headers that carried a credential in the clear, one for each header name on each connection; flows names them. Credentials are searched in HTTP/1.x and HTTP/2 headers only: Telnet, FTP, SNMP communities, URL query strings and bodies are not searched, so 0 is not proof that none crossed. |
| `cleartext_management` | `int` | always | Connections named from their bytes as http, http2 or grpc that crossed in the clear to a management endpoint (see the description). Telnet, FTP and SNMP are not named, and are not counted here. |
| `cleartext_in_encrypted` | `int` | always | Connections whose bytes framed as encrypted (TLS application data, SSH after NEWKEYS) read as text: a NULL cipher suite, the SSH none cipher, or records forged around cleartext. Their packets are counted cleartext. |
| `headers_unread` | `int` | always | Headers or header blocks that could not be searched for a credential: a block over 64 KiB or one that would not decode (and every block after it), a name over 256 bytes, headers after a gap or a protocol switch, and a credential name past the 32 kept for a connection. When it is not 0, cleartext_credentials may be short. |
| `warnings` | `list of string` | when a credential, text framed as encrypted, management traffic, or headers that could not all be searched crossed in the clear | One line for each connection that did, naming its headers but never a value and saying when some of its headers could not be searched, at most ten and then a count of the rest. |
| `link_types` | `list of string` | always | The link types the capture declares. |
| `unread` | `int` | always | Frames of a link type this method does not read. |
| `unexamined` | `int` | always | Frames this method could not judge encrypted or not: unread ones, and every one carrying no IP (non-ip). |
| `dscp` | `list of dict` | always | Every IP packet by DSCP mark, most bytes first: dscp (0 to 63), name (its codepoint, such as EF or AF41, empty for a value no standard names), class (its RFC 4594 service class, empty when RFC 4594 gives it none), packets and bytes. |
| `first` | `string` | when the capture holds a packet time | The first packet's time, RFC 3339 in UTC. |
| `last` | `string` | when the capture holds a packet time | The last packet's time. |
| `duration_seconds` | `float` | always | The seconds from first to last, 0 when the capture holds no time. |

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

