// Package pcapsum: the layers under a frame, read only as far as naming its
// protocol: Ethernet with its VLAN tags, IPv4 and IPv6 with IPv6's extension
// headers, and the UDP and TCP ports that say IKE, BGP or HSRP. What a TCP
// flow carries (SSH, TLS, HTTP/2) is read from its bytes, never its ports,
// in flow.go.
package pcapsum

import (
	"encoding/binary"
	"net/netip"
)

// ethernet returns an Ethernet frame's EtherType and payload, past any
// 802.1Q or 802.1ad tags, or 0 for a frame too short to hold one.
func ethernet(frame []byte) (int, []byte) {
	if len(frame) < 14 {
		return 0, nil
	}
	etherType := int(binary.BigEndian.Uint16(frame[12:]))
	off := 14
	// Tags stack: 802.1ad (0x88a8), 802.1Q (0x8100) and the older 0x9100.
	for (etherType == 0x8100 || etherType == 0x88a8 || etherType == 0x9100) && len(frame) >= off+4 {
		etherType = int(binary.BigEndian.Uint16(frame[off+2:]))
		off += 4
	}
	if etherType < 0x0600 {
		// An 802.3 length field: LLC (CDP, STP), never IP here.
		return 0, nil
	}
	return etherType, frame[off:]
}

// IP protocol numbers named here.
const (
	ipICMP   = 1
	ipTCP    = 6
	ipUDP    = 17
	ipGRE    = 47
	ipESP    = 50
	ipAH     = 51
	ipICMPv6 = 58
	ipEIGRP  = 88
	ipOSPF   = 89
	ipPIM    = 103
	ipVRRP   = 112
)

// ipPacket is what ip reads from an IP header: the two addresses, the
// protocol it names, and for a TCP segment the bytes a flow is read from.
type ipPacket struct {
	src, dst netip.Addr
	// proto is the protocol's name, from the IP header and the transport
	// ports, exactly as this package has always named it.
	proto string
	// next is the IP protocol number of the payload.
	next int
	// payload is the transport header and data as captured, cut to the
	// length the IP header gives, so the zeros that pad a short Ethernet
	// frame are never read as transport bytes.
	payload []byte
	// wire is the payload's length on the wire by the IP header, which a
	// capture cut at its snapshot length holds fewer bytes of; -1 when the
	// header gives none (an IPv6 jumbogram).
	wire int
	// fragment is set for any fragment, first or later, whose transport
	// bytes are not all here, and for an extension chain not followed to
	// its end.
	fragment bool
	// dscp is the packet's DSCP: the top six bits of IPv4's DS field (the
	// old TOS byte) or of IPv6's traffic class (RFC 2474).
	dscp int
}

// ip reads an IP packet's addresses and names its protocol; ok is false for
// a header too short or malformed to read.
func ip(pkt []byte, v6 bool) (p ipPacket, ok bool) {
	if v6 {
		if len(pkt) < 40 || pkt[0]>>4 != 6 {
			return p, false
		}
		p.src, _ = netip.AddrFromSlice(pkt[8:24])
		p.dst, _ = netip.AddrFromSlice(pkt[24:40])
		// The traffic class spans the low four bits of the first byte and
		// the high four of the second.
		p.dscp = int((pkt[0]&0x0f)<<4|pkt[1]>>4) >> 2
		p.wire = int(binary.BigEndian.Uint16(pkt[4:]))
		if p.wire == 0 {
			// A jumbogram gives its length in an option, not read here.
			p.wire = -1
		}
		p.next, p.payload = int(pkt[6]), pkt[40:]
		// Hop-by-hop (0), routing (43), destination options (60): length
		// in 8-byte units after the first 8. Fragment (44): 8 bytes.
		for range 8 {
			n := 0
			switch p.next {
			case 0, 43, 60:
				if len(p.payload) < 8 {
					return chainCut(p), true
				}
				n = 8 + int(p.payload[1])*8
				if n > len(p.payload) {
					return chainCut(p), true
				}
			case 44:
				if len(p.payload) < 8 {
					return chainCut(p), true
				}
				n = 8
				p.fragment = true
			default:
				return named(p, p.payload), true
			}
			p.next, p.payload = int(p.payload[0]), p.payload[n:]
			if p.wire >= 0 {
				p.wire -= n
			}
		}
		return named(p, p.payload), true
	}
	if len(pkt) < 20 || pkt[0]>>4 != 4 {
		return p, false
	}
	ihl := int(pkt[0]&0x0f) * 4
	if ihl < 20 || ihl > len(pkt) {
		return p, false
	}
	p.src = netip.AddrFrom4([4]byte(pkt[12:16]))
	p.dst = netip.AddrFrom4([4]byte(pkt[16:20]))
	p.dscp = int(pkt[1] >> 2)
	p.next, p.payload = int(pkt[9]), pkt[ihl:]
	p.wire = int(binary.BigEndian.Uint16(pkt[2:])) - ihl
	flags := binary.BigEndian.Uint16(pkt[6:])
	// More fragments (0x2000) or an offset: not every transport byte is here.
	p.fragment = flags&0x3fff != 0
	if flags&0x1fff != 0 {
		// A later fragment carries no transport header to read.
		return named(p, nil), true
	}
	return named(p, p.payload), true
}

// chainCut is p when its IPv6 extension headers could not be followed to
// their end: named other-ip, as this package always has, and never read as a
// flow.
func chainCut(p ipPacket) ipPacket {
	p.proto, p.fragment = OtherIP, true
	return p
}

// named sets p's protocol name from ports, the transport bytes as captured
// (read exactly as before flows were read, Ethernet padding included), and
// then cuts p's payload to the length its IP header gives.
func named(p ipPacket, ports []byte) ipPacket {
	p.proto = protoName(p.next, ports)
	if p.wire < 0 {
		return p
	}
	if p.wire < len(p.payload) {
		p.payload = p.payload[:p.wire]
	}
	return p
}

// protoName names IP protocol next, reading the transport ports in payload
// when it has them.
func protoName(next int, payload []byte) string {
	switch next {
	case ipESP:
		return ESP
	case ipAH:
		return AH
	case ipGRE:
		return GRE
	case ipICMP:
		return ICMP
	case ipICMPv6:
		return ICMPv6
	case ipOSPF:
		return OSPF
	case ipEIGRP:
		return EIGRP
	case ipPIM:
		return PIM
	case ipVRRP:
		return VRRP
	case ipUDP:
		if len(payload) < 8 {
			return UDP
		}
		sp, dp := binary.BigEndian.Uint16(payload), binary.BigEndian.Uint16(payload[2:])
		switch {
		case sp == 500 || dp == 500:
			return IKE
		case sp == 4500 || dp == 4500:
			// RFC 3948: four zero bytes (the non-ESP marker) begin IKE;
			// anything else is an ESP packet's SPI. A one-byte 0xff is a
			// NAT-keepalive.
			body := payload[8:]
			if len(body) >= 4 && binary.BigEndian.Uint32(body) == 0 {
				return IKE
			}
			if len(body) == 1 && body[0] == 0xff {
				return IKE
			}
			return ESPInUDP
		case sp == 1985 || dp == 1985:
			return HSRP
		}
		return UDP
	case ipTCP:
		if len(payload) >= 4 {
			sp, dp := binary.BigEndian.Uint16(payload), binary.BigEndian.Uint16(payload[2:])
			if sp == 179 || dp == 179 {
				return BGP
			}
		}
		return TCP
	}
	return OtherIP
}
