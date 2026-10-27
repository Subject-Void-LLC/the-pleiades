// Package pcapsum reads a packet capture (pcap or pcapng) and counts what
// crossed the wire by protocol and by address pair: IPsec's ESP and IKE
// apart from cleartext, GRE, ICMP, the routing protocols, TCP and UDP. It
// proves from packets what a configuration claims, that traffic between two
// sites is encrypted, without trusting either router's counters.
//
// It is the standard library only, bounded by the bytes it is given, and
// reads every record defensively: a record that claims more bytes than
// remain, or a block that does not fit, ends the read with an error naming
// where, and what was counted before it is still returned.
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
	// encrypted (ESP, ESP in UDP), the key exchange that set it up (IKE),
	// and everything else carried over IP, which a reader of the wire sees.
	Encrypted   int `json:"encrypted"`
	KeyExchange int `json:"key_exchange"`
	Cleartext   int `json:"cleartext"`
	// Pairs are the address pairs, most packets first.
	Pairs []Pair `json:"pairs"`
	// Unread is the number of frames whose link type this package does not
	// read, counted under no protocol.
	Unread int `json:"unread"`
}

// ErrNotACapture is returned for data that begins as neither pcap nor
// pcapng.
var ErrNotACapture = errors.New("pcapsum: not a pcap or pcapng file")

// Read summarizes data, a whole capture file. On a malformed record it
// returns what it counted before the record and an error naming where the
// record was.
func Read(data []byte) (Summary, error) {
	s := Summary{Protocols: map[string]Counter{}}
	pairs := map[[2]string]*Pair{}
	var err error
	switch {
	case len(data) >= 4 && binary.BigEndian.Uint32(data) == 0x0a0d0d0a:
		s.Format = "pcapng"
		err = readPcapng(data, &s, pairs)
	case len(data) >= 24:
		s.Format = "pcap"
		err = readPcap(data, &s, pairs)
	default:
		err = ErrNotACapture
	}
	s.Pairs = sortedPairs(pairs)
	return s, err
}

// readPcap reads a classic pcap file: a 24-byte header naming the byte order
// (the magic), the time precision and the link type, then records of a
// 16-byte header and the frame.
func readPcap(data []byte, s *Summary, pairs map[[2]string]*Pair) error {
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
		count(s, pairs, link, data[off:off+captured], original)
		off += captured
	}
	return nil
}

// readPcapng reads a pcapng file's blocks: each section header names the
// byte order; each interface description its link type; each enhanced or
// simple packet block a frame on an interface.
func readPcapng(data []byte, s *Summary, pairs map[[2]string]*Pair) error {
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
			count(s, pairs, links[iface], body[20:20+captured], original)
		case 3: // simple packet: original length, then the frame, on interface 0
			if len(body) < 4 || len(links) == 0 {
				return fmt.Errorf("pcapsum: the simple packet block at byte %d is cut short or has no interface", off)
			}
			original := int(order.Uint32(body))
			frame := body[4:]
			if original < len(frame) {
				frame = frame[:original]
			}
			count(s, pairs, links[0], frame, original)
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
func count(s *Summary, pairs map[[2]string]*Pair, link uint32, frame []byte, original int) {
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
		src, dst, proto, ok := ip(payload, etherType == 0x86dd)
		if !ok {
			add(s, nil, NonIP, original)
			return
		}
		add(s, pair(pairs, src, dst), proto, original)
	case 0x0806:
		add(s, nil, ARP, original)
	default:
		add(s, nil, NonIP, original)
	}
}

// add counts a packet of proto, and in its pair when it has one.
func add(s *Summary, p *Pair, proto string, n int) {
	c := s.Protocols[proto]
	c.add(n)
	s.Protocols[proto] = c
	isIP := p != nil
	switch {
	case encrypted[proto]:
		s.Encrypted++
	case keyExchange[proto]:
		s.KeyExchange++
	case isIP:
		s.Cleartext++
	}
	if p == nil {
		return
	}
	pc := p.Protocols[proto]
	pc.add(n)
	p.Protocols[proto] = pc
	switch {
	case encrypted[proto]:
		p.Encrypted++
	case keyExchange[proto]:
		p.KeyExchange++
	default:
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
