// Package pcapsum reads a packet capture (pcap or pcapng) and counts what
// crossed the wire by protocol and by address pair: IPsec's ESP and IKE
// apart from cleartext, GRE, ICMP, the routing protocols, TCP and UDP. It
// proves from packets what a configuration claims, that traffic between two
// sites is encrypted, without trusting either router's counters.
//
// Each TCP flow is also read from its bytes, both directions in sequence
// order, never judged by its port: SSH (its banner, the key exchange, then
// everything after NEWKEYS), TLS (each record by its content type), HTTP/1.x
// in the clear (its request or status line, then its header lines), and
// HTTP/2 in the clear (its client preface), named grpc when a request says
// application/grpc, with each header block decoded (HPACK) so that a
// credential in a header is found though its value is Huffman-coded. A
// credential is reported by its header's name and its flow, never its value.
// Only HTTP/1.x and HTTP/2 headers are searched for one: Telnet, FTP, SNMP
// communities, URL query strings and bodies are not.
//
// Bytes framed as encrypted (a TLS application data record, SSH after
// NEWKEYS) that read as text are counted cleartext and the flow flagged, so
// a NULL cipher suite, SSH's none cipher or records forged around cleartext
// are not called encrypted. ESP is still counted encrypted whatever its
// cipher, ESP-NULL included: its payload is not read.
//
// It is the standard library and golang.org/x/net/http2/hpack only. A
// flow's bytes are read in place; what is copied is bounded: each
// direction's first openingLen bytes, a frame or record header, an HTTP/1.x
// line of at most maxH1Line bytes, one HTTP/2 header block of at most
// maxHeaderBlock bytes and the HPACK dynamic table of at most maxHeaderTable
// bytes for each direction of the one flow being read. Its memory is linear
// in the capture's packets: measured at about 1000 bytes a packet at its
// peak when every packet is a TCP segment of its own flow between its own
// pair (about half of it the pairs the result lists, which every capture of
// UDP alone costs too), far less when packets share flows. It reads every record
// defensively: a record that claims more bytes than remain, or a block that
// does not fit, ends the read with an error naming where, and what was
// counted before it is still returned.
package pcapsum

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sort"
)

// The protocol names a Summary counts packets under.
const (
	ESP      = "esp"      // IP protocol 50: IPsec's encrypted payload
	ESPInUDP = "esp-udp"  // ESP carried in UDP 4500 (NAT traversal)
	IKE      = "ike"      // IKE on UDP 500, or on 4500 behind the non-ESP marker
	AH       = "ah"       // IP protocol 51: authenticated, not encrypted
	GRE      = "gre"      // IP protocol 47 seen in the clear
	ICMP     = "icmp"     // IP protocol 1
	ICMPv6   = "icmpv6"   // IPv6 next header 58
	OSPF     = "ospf"     // IP protocol 89
	EIGRP    = "eigrp"    // IP protocol 88
	VRRP     = "vrrp"     // IP protocol 112
	PIM      = "pim"      // IP protocol 103
	TCP      = "tcp"      // IP protocol 6, other than the protocols named here
	BGP      = "bgp"      // TCP port 179
	UDP      = "udp"      // IP protocol 17, other than the protocols named here
	HSRP     = "hsrp"     // UDP port 1985
	ARP      = "arp"      // EtherType 0x0806
	OtherIP  = "other-ip" // any other IP protocol
	NonIP    = "non-ip"   // a frame that carries no IP or ARP (CDP, LOOP keepalives, STP)
	SSH      = "ssh"      // a TCP flow that opened with an SSH-2.0 banner
	TLS      = "tls"      // a TCP flow that opened with a TLS ClientHello or ServerHello
	HTTP     = "http"     // a TCP flow that opened with an HTTP/1.x request or status line, in the clear
	HTTP2    = "http2"    // a TCP flow that opened with HTTP/2's client preface, in the clear
	GRPC     = "grpc"     // an http2 flow whose headers say application/grpc
)

// encrypted are the protocols whose payload a reader of the wire cannot see.
var encrypted = map[string]bool{ESP: true, ESPInUDP: true}

// keyExchange are the protocols that set up encryption.
var keyExchange = map[string]bool{IKE: true}

// Counter is packets and bytes (as captured, each frame's original length).
type Counter struct {
	Packets int   `json:"packets"`
	Bytes   int64 `json:"bytes"`
}

// add counts one packet of n bytes.
func (c *Counter) add(n int) {
	c.Packets++
	c.Bytes += int64(n)
}

// Pair is the packets between two addresses, in both directions.
type Pair struct {
	// A and B are the two addresses, A the lower.
	A string `json:"a"`
	B string `json:"b"`
	// Protocols counts the pair's packets by protocol name.
	Protocols map[string]Counter `json:"protocols"`
	// Encrypted, KeyExchange and Cleartext sum the pair's packets by kind.
	Encrypted   int `json:"encrypted"`
	KeyExchange int `json:"key_exchange"`
	Cleartext   int `json:"cleartext"`
}

// Summary is what a capture holds.
type Summary struct {
	// Format is "pcap" or "pcapng".
	Format string `json:"format"`
	// LinkTypes are the link types the capture's interfaces declare, by
	// name ("ethernet", "raw", "linux_sll") or number.
	LinkTypes []string `json:"link_types"`
	// Packets and Bytes are the totals.
	Packets int   `json:"packets"`
	Bytes   int64 `json:"bytes"`
	// Protocols counts every packet by protocol name.
	Protocols map[string]Counter `json:"protocols"`
	// Encrypted, KeyExchange and Cleartext sum the IP packets by kind:
	// encrypted (ESP, ESP in UDP, SSH after NEWKEYS, TLS application data),
	// the key exchange that set it up (IKE, SSH's banner and key exchange,
	// TLS handshake records), and everything else carried over IP, which a
	// reader of the wire sees.
	Encrypted   int `json:"encrypted"`
	KeyExchange int `json:"key_exchange"`
	Cleartext   int `json:"cleartext"`
	// Pairs are the address pairs, most packets first.
	Pairs []Pair `json:"pairs"`
	// Flows are the TCP flows named from their bytes (ssh, tls, http,
	// http2, grpc): those carrying a credential in the clear first, then
	// those whose encrypted-looking bytes read as text, then the other
	// cleartext management flows, then most packets first.
	Flows []Flow `json:"flows"`
	// CleartextCredentials is the number of credential headers seen in the
	// clear, one for each header name on each flow, up to 32 names a flow.
	// Only HTTP/1.x and HTTP/2 headers are searched: Telnet, FTP, SNMP
	// communities, URL query strings and bodies are not, so 0 means no
	// credential header was found, not that no credential crossed.
	CleartextCredentials int `json:"cleartext_credentials"`
	// CleartextManagement is the number of flows of a management protocol
	// (http, http2, grpc) that crossed in the clear. Telnet, FTP and SNMP
	// are not named, and are not counted here.
	CleartextManagement int `json:"cleartext_management"`
	// CleartextInEncrypted is the number of flows where bytes framed as
	// encrypted (TLS application data, SSH after NEWKEYS) read as text.
	CleartextInEncrypted int `json:"cleartext_in_encrypted"`
	// HeadersUnread is the number of headers or header blocks, over every
	// flow, that could not be searched for a credential.
	HeadersUnread int `json:"headers_unread"`
	// Unread is the number of frames whose link type this package does not
	// read, counted under no protocol.
	Unread int `json:"unread"`
}

// Flow is one TCP connection named from what it carried.
type Flow struct {
	// Client and Server are address:port; the client is the side that sent
	// the first SYN, or else the side that opened the protocol (the TLS
	// ClientHello, the HTTP/2 preface), or else the higher port.
	Client string `json:"client"`
	Server string `json:"server"`
	// Protocol is ssh, tls, http (HTTP/1.x), http2 or grpc.
	Protocol string `json:"protocol"`
	// Packets, Encrypted, KeyExchange and Cleartext count the flow's
	// packets, both directions, by kind, as the pair's are counted.
	Packets     int `json:"packets"`
	Encrypted   int `json:"encrypted"`
	KeyExchange int `json:"key_exchange"`
	Cleartext   int `json:"cleartext"`
	// CleartextManagement is set for a management protocol (http, http2,
	// grpc) that crossed in the clear.
	CleartextManagement bool `json:"cleartext_management"`
	// CleartextInEncrypted is set when bytes framed as encrypted (a TLS
	// application data record, SSH after NEWKEYS) read as text: a NULL
	// cipher suite, SSH's none cipher, or records forged around cleartext.
	// Those packets are counted cleartext.
	CleartextInEncrypted bool `json:"cleartext_in_encrypted"`
	// CleartextCredentials are the names of the HTTP/1.x and HTTP/2 headers
	// that carried a credential in the clear (authorization, username,
	// password and their kind), sorted, at most 32. A value is never kept,
	// only its header's name.
	CleartextCredentials []string `json:"cleartext_credentials,omitempty"`
	// HeadersUnread counts the headers or header blocks of this flow that
	// could not be searched: a block over 64 KiB or that would not decode
	// (and every block after it, the table being lost), a header name over
	// 256 bytes, headers after the framing was lost to a gap or a protocol
	// switch, and a credential name past the 32 kept. When it is not 0,
	// CleartextCredentials may be short.
	HeadersUnread int `json:"headers_unread"`
}

// ErrNotACapture is returned for data that begins as neither pcap nor
// pcapng.
var ErrNotACapture = errors.New("pcapsum: not a pcap or pcapng file")

// Read summarizes data, a whole capture file. On a malformed record it
// returns what it counted before the record and an error naming where the
// record was.
func Read(data []byte) (Summary, error) {
	s := Summary{Protocols: map[string]Counter{}}
	r := &reader{s: &s, pairs: map[[2]string]*Pair{}, flows: newTracker()}
	var err error
	switch {
	case len(data) >= 4 && binary.BigEndian.Uint32(data) == 0x0a0d0d0a:
		s.Format = "pcapng"
		err = readPcapng(data, r)
	case len(data) >= 24:
		s.Format = "pcap"
		err = readPcap(data, r)
	default:
		err = ErrNotACapture
	}
	// Every TCP segment waits for its flow to be read whole, in sequence
	// order, before it is counted, even when the read stopped early.
	r.flows.resolve(r.s)
	s.Pairs = sortedPairs(r.pairs)
	return s, err
}

// reader is one read's state: the summary it fills, its pairs, and the TCP
// flows whose segments are counted once each flow has been read.
type reader struct {
	s     *Summary
	pairs map[[2]string]*Pair
	flows *tracker
}

// readPcap reads a classic pcap file: a 24-byte header naming the byte order
// (the magic), the time precision and the link type, then records of a
// 16-byte header and the frame.
func readPcap(data []byte, r *reader) error {
	s := r.s
	var order binary.ByteOrder
	switch binary.LittleEndian.Uint32(data) {
	case 0xa1b2c3d4, 0xa1b23c4d:
		order = binary.LittleEndian
	default:
		switch binary.BigEndian.Uint32(data) {
		case 0xa1b2c3d4, 0xa1b23c4d:
			order = binary.BigEndian
		default:
			return ErrNotACapture
		}
	}
	link := order.Uint32(data[20:24]) & 0x0fffffff
	s.LinkTypes = []string{linkName(link)}
	off := 24
	for off < len(data) {
		if len(data)-off < 16 {
			return fmt.Errorf("pcapsum: a record header at byte %d is cut short", off)
		}
		captured := int(order.Uint32(data[off+8:]))
		original := int(order.Uint32(data[off+12:]))
		off += 16
		if captured < 0 || captured > len(data)-off {
			return fmt.Errorf("pcapsum: the record at byte %d claims %d bytes, more than remain", off-16, captured)
		}
		r.count(link, data[off:off+captured], original)
		off += captured
	}
	return nil
}

// readPcapng reads a pcapng file's blocks: each section header names the
// byte order; each interface description its link type; each enhanced or
// simple packet block a frame on an interface.
func readPcapng(data []byte, r *reader) error {
	s := r.s
	var order binary.ByteOrder = binary.LittleEndian
	var links []uint32
	off := 0
	for off < len(data) {
		if len(data)-off < 12 {
			return fmt.Errorf("pcapsum: a block at byte %d is cut short", off)
		}
		blockType := order.Uint32(data[off:])
		if blockType == 0x0a0d0d0a {
			// A section header: its byte-order magic says how to read the
			// rest, its own length included.
			switch binary.LittleEndian.Uint32(data[off+8:]) {
			case 0x1a2b3c4d:
				order = binary.LittleEndian
			case 0x4d3c2b1a:
				order = binary.BigEndian
			default:
				return fmt.Errorf("pcapsum: the section header at byte %d has no byte-order magic", off)
			}
			links = nil
		}
		length := int(order.Uint32(data[off+4:]))
		if length < 12 || length%4 != 0 || length > len(data)-off {
			return fmt.Errorf("pcapsum: the block at byte %d claims a length of %d, which does not fit", off, length)
		}
		body := data[off+8 : off+length-4]
		switch blockType {
		case 1: // interface description: link type, then reserved, then snap length
			if len(body) < 8 {
				return fmt.Errorf("pcapsum: the interface block at byte %d is cut short", off)
			}
			link := uint32(order.Uint16(body))
			links = append(links, link)
			s.LinkTypes = appendOnce(s.LinkTypes, linkName(link))
		case 6: // enhanced packet: interface, time, captured and original length, frame
			if len(body) < 20 {
				return fmt.Errorf("pcapsum: the packet block at byte %d is cut short", off)
			}
			iface := int(order.Uint32(body))
			captured := int(order.Uint32(body[12:]))
			original := int(order.Uint32(body[16:]))
			if captured < 0 || captured > len(body)-20 {
				return fmt.Errorf("pcapsum: the packet block at byte %d claims %d bytes, more than it holds", off, captured)
			}
			if iface < 0 || iface >= len(links) {
				return fmt.Errorf("pcapsum: the packet block at byte %d names interface %d, which no interface block declared", off, iface)
			}
			r.count(links[iface], body[20:20+captured], original)
		case 3: // simple packet: original length, then the frame, on interface 0
			if len(body) < 4 || len(links) == 0 {
				return fmt.Errorf("pcapsum: the simple packet block at byte %d is cut short or has no interface", off)
			}
			original := int(order.Uint32(body))
			frame := body[4:]
			if original < len(frame) {
				frame = frame[:original]
			}
			r.count(links[0], frame, original)
		}
		off += length
	}
	return nil
}

// appendOnce appends v to list unless it is there.
func appendOnce(list []string, v string) []string {
	for _, e := range list {
		if e == v {
			return list
		}
	}
	return append(list, v)
}

// The link types read, by their numbers in the pcap registry.
const (
	linkEthernet  = 1
	linkRaw       = 101
	linkRawAlt    = 12
	linkIPv4      = 228
	linkIPv6      = 229
	linkLinuxSLL  = 113
	linkLinuxSLL2 = 276
)

// linkName names a link type.
func linkName(link uint32) string {
	switch link {
	case linkEthernet:
		return "ethernet"
	case linkRaw, linkRawAlt, linkIPv4, linkIPv6:
		return "raw"
	case linkLinuxSLL:
		return "linux_sll"
	case linkLinuxSLL2:
		return "linux_sll2"
	}
	return fmt.Sprintf("linktype_%d", link)
}

// count counts one frame of link type link, whose wire length was original.
func (r *reader) count(link uint32, frame []byte, original int) {
	s := r.s
	s.Packets++
	s.Bytes += int64(original)
	var etherType int
	var payload []byte
	switch link {
	case linkEthernet:
		etherType, payload = ethernet(frame)
	case linkLinuxSLL:
		if len(frame) >= 16 {
			etherType, payload = int(binary.BigEndian.Uint16(frame[14:])), frame[16:]
		}
	case linkLinuxSLL2:
		if len(frame) >= 20 {
			etherType, payload = int(binary.BigEndian.Uint16(frame)), frame[20:]
		}
	case linkRaw, linkRawAlt, linkIPv4, linkIPv6:
		if len(frame) > 0 {
			switch frame[0] >> 4 {
			case 4:
				etherType = 0x0800
			case 6:
				etherType = 0x86dd
			}
			payload = frame
		}
	default:
		s.Unread++
		return
	}
	switch etherType {
	case 0x0800, 0x86dd:
		pkt, ok := ip(payload, etherType == 0x86dd)
		if !ok {
			add(s, nil, NonIP, original)
			return
		}
		pr := pair(r.pairs, pkt.src, pkt.dst)
		// A whole TCP segment waits for its flow to be read; anything else,
		// a fragment included, is counted now by its protocol's name.
		if pkt.next == ipTCP && !pkt.fragment && r.flows.segment(pr, pkt, original) {
			return
		}
		add(s, pr, pkt.proto, original)
	case 0x0806:
		add(s, nil, ARP, original)
	default:
		add(s, nil, NonIP, original)
	}
}

// add counts a packet of proto, and in its pair when it has one, as the
// kind its protocol's name says.
func add(s *Summary, p *Pair, proto string, n int) {
	k := kindNone
	switch {
	case encrypted[proto]:
		k = kindEncrypted
	case keyExchange[proto]:
		k = kindKeyExchange
	case p != nil:
		k = kindCleartext
	}
	addKind(s, p, proto, n, k)
}

// addKind counts a packet of proto as kind k, and in its pair when it has
// one. A packet with no pair carried no IP and is of no kind.
func addKind(s *Summary, p *Pair, proto string, n int, k kind) {
	c := s.Protocols[proto]
	c.add(n)
	s.Protocols[proto] = c
	if p == nil {
		return
	}
	pc := p.Protocols[proto]
	pc.add(n)
	p.Protocols[proto] = pc
	switch k {
	case kindEncrypted:
		s.Encrypted++
		p.Encrypted++
	case kindKeyExchange:
		s.KeyExchange++
		p.KeyExchange++
	default:
		s.Cleartext++
		p.Cleartext++
	}
}

// pair is the Pair for two addresses, either way round.
func pair(pairs map[[2]string]*Pair, a, b netip.Addr) *Pair {
	if b.Less(a) {
		a, b = b, a
	}
	key := [2]string{a.String(), b.String()}
	p, ok := pairs[key]
	if !ok {
		p = &Pair{A: key[0], B: key[1], Protocols: map[string]Counter{}}
		pairs[key] = p
	}
	return p
}

// sortedPairs is pairs, most packets first, then by address.
func sortedPairs(pairs map[[2]string]*Pair) []Pair {
	out := make([]Pair, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, *p)
	}
	total := func(p Pair) int { return p.Encrypted + p.KeyExchange + p.Cleartext }
	sort.Slice(out, func(i, j int) bool {
		if total(out[i]) != total(out[j]) {
			return total(out[i]) > total(out[j])
		}
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out
}
