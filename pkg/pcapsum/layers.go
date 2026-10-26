// Package pcapsum: the layers under a frame, read only as far as naming its
// protocol: Ethernet with its VLAN tags, IPv4 and IPv6 with IPv6's extension
// headers, and the UDP and TCP ports that say IKE, BGP or HSRP.
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

// ip reads an IP packet's addresses and names its protocol; ok is false for
// a header too short or malformed to read.
func ip(pkt []byte, v6 bool) (src, dst netip.Addr, proto string, ok bool) {
	var next int
	var payload []byte
	if v6 {
		if len(pkt) < 40 || pkt[0]>>4 != 6 {
			return src, dst, "", false
		}
		src, _ = netip.AddrFromSlice(pkt[8:24])
		dst, _ = netip.AddrFromSlice(pkt[24:40])
		next, payload = int(pkt[6]), pkt[40:]
		// Hop-by-hop (0), routing (43), destination options (60): length
		// in 8-byte units after the first 8. Fragment (44): 8 bytes.
		for range 8 {
			switch next {
			case 0, 43, 60:
				if len(payload) < 8 {
					return src, dst, OtherIP, true
				}
				n := 8 + int(payload[1])*8
				if n > len(payload) {
					return src, dst, OtherIP, true
				}
				next, payload = int(payload[0]), payload[n:]
				continue
			case 44:
				if len(payload) < 8 {
					return src, dst, OtherIP, true
				}
				next, payload = int(payload[0]), payload[8:]
				continue
			}
			break
		}
	} else {
		if len(pkt) < 20 || pkt[0]>>4 != 4 {
			return src, dst, "", false
		}
		ihl := int(pkt[0]&0x0f) * 4
		if ihl < 20 || ihl > len(pkt) {
			return src, dst, "", false
		}
		src = netip.AddrFrom4([4]byte(pkt[12:16]))
		dst = netip.AddrFrom4([4]byte(pkt[16:20]))
		next, payload = int(pkt[9]), pkt[ihl:]
		// A later fragment carries no transport header to read.
		if binary.BigEndian.Uint16(pkt[6:])&0x1fff != 0 {
			return src, dst, protoName(next, nil), true
		}
	}
	return src, dst, protoName(next, payload), true
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
