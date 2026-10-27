// Tests for pcapsum against captures built here, frame by frame: an IPsec
// site pair (IKE then ESP both ways, NAT traversal on 4500), cleartext ICMP,
// GRE, OSPF, ARP and a VLAN-tagged frame, written as pcap in both byte orders
// and as pcapng with enhanced and simple packet blocks.
package pcapsum_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/pcapsum"
)

// ipv4 is an IPv4 packet from src to dst of protocol proto carrying payload.
func ipv4(src, dst [4]byte, proto byte, payload []byte) []byte {
	h := make([]byte, 20)
	h[0] = 0x45
	binary.BigEndian.PutUint16(h[2:], uint16(20+len(payload)))
	h[8] = 64
	h[9] = proto
	copy(h[12:], src[:])
	copy(h[16:], dst[:])
	return append(h, payload...)
}

// udp is a UDP header from sport to dport and payload.
func udp(sport, dport uint16, payload []byte) []byte {
	h := make([]byte, 8)
	binary.BigEndian.PutUint16(h, sport)
	binary.BigEndian.PutUint16(h[2:], dport)
	binary.BigEndian.PutUint16(h[4:], uint16(8+len(payload)))
	return append(h, payload...)
}

// eth is an Ethernet frame of etherType carrying payload, with an 802.1Q
// tag when vlan is set.
func eth(etherType uint16, payload []byte, vlan bool) []byte {
	f := make([]byte, 12)
	if vlan {
		f = append(f, 0x81, 0x00, 0x00, 0x0a)
	}
	f = binary.BigEndian.AppendUint16(f, etherType)
	return append(f, payload...)
}

// The two sites and a third host.
var (
	siteA = [4]byte{172, 16, 1, 1}
	siteB = [4]byte{172, 16, 2, 1}
	host  = [4]byte{10, 1, 1, 10}
)

// frames are the capture every format is tested with.
func frames() [][]byte {
	espPayload := append([]byte{0, 0, 0x10, 0x01, 0, 0, 0, 1}, make([]byte, 40)...)
	var out [][]byte
	// IKE on 500, both ways.
	out = append(out, eth(0x0800, ipv4(siteA, siteB, 17, udp(500, 500, make([]byte, 60))), false))
	out = append(out, eth(0x0800, ipv4(siteB, siteA, 17, udp(500, 500, make([]byte, 60))), false))
	// ESP both ways, three each.
	for range 3 {
		out = append(out, eth(0x0800, ipv4(siteA, siteB, 50, espPayload), false))
		out = append(out, eth(0x0800, ipv4(siteB, siteA, 50, espPayload), false))
	}
	// NAT traversal: IKE behind the non-ESP marker, then ESP in UDP 4500.
	out = append(out, eth(0x0800, ipv4(siteA, siteB, 17, udp(4500, 4500, append([]byte{0, 0, 0, 0}, make([]byte, 30)...))), false))
	out = append(out, eth(0x0800, ipv4(siteA, siteB, 17, udp(4500, 4500, espPayload)), false))
	// Cleartext between the same sites: an ICMP echo, and GRE.
	out = append(out, eth(0x0800, ipv4(siteA, siteB, 1, []byte{8, 0, 0, 0, 0, 1, 0, 1}), false))
	out = append(out, eth(0x0800, ipv4(siteA, siteB, 47, []byte{0, 0, 0x08, 0x00}), false))
	// OSPF hello to its multicast group, on a tagged frame.
	out = append(out, eth(0x0800, ipv4(host, [4]byte{224, 0, 0, 5}, 89, make([]byte, 44)), true))
	// ARP, and a frame carrying no IP (an 802.3 length, as CDP's).
	out = append(out, eth(0x0806, make([]byte, 28), false))
	out = append(out, append(make([]byte, 12), 0x00, 0x40))
	// BGP between the sites, TCP 179.
	tcp := make([]byte, 20)
	binary.BigEndian.PutUint16(tcp, 40000)
	binary.BigEndian.PutUint16(tcp[2:], 179)
	out = append(out, eth(0x0800, ipv4(siteA, siteB, 6, tcp), false))
	return out
}

// pcapFile writes frames as a classic pcap file in order, link type 1.
func pcapFile(order binary.ByteOrder, frames [][]byte) []byte {
	var b bytes.Buffer
	h := make([]byte, 24)
	order.PutUint32(h, 0xa1b2c3d4)
	order.PutUint16(h[4:], 2)
	order.PutUint16(h[6:], 4)
	order.PutUint32(h[16:], 65535)
	order.PutUint32(h[20:], 1)
	b.Write(h)
	for _, f := range frames {
		r := make([]byte, 16)
		order.PutUint32(r[8:], uint32(len(f)))
		order.PutUint32(r[12:], uint32(len(f)))
		b.Write(r)
		b.Write(f)
	}
	return b.Bytes()
}

// block is one little-endian pcapng block of type t around body.
func block(t uint32, body []byte) []byte {
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	n := uint32(12 + len(body))
	out := binary.LittleEndian.AppendUint32(nil, t)
	out = binary.LittleEndian.AppendUint32(out, n)
	out = append(out, body...)
	return binary.LittleEndian.AppendUint32(out, n)
}

// pcapngFile writes frames as pcapng: a section, one Ethernet interface,
// the first frame as a simple packet block and the rest as enhanced ones.
func pcapngFile(frames [][]byte) []byte {
	shb := binary.LittleEndian.AppendUint32(nil, 0x1a2b3c4d)
	shb = append(shb, 1, 0, 0, 0)
	shb = append(shb, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	out := block(0x0a0d0d0a, shb)
	idb := []byte{1, 0, 0, 0, 0, 0, 0, 0}
	out = append(out, block(1, idb)...)
	for i, f := range frames {
		if i == 0 {
			spb := binary.LittleEndian.AppendUint32(nil, uint32(len(f)))
			out = append(out, block(3, append(spb, f...))...)
			continue
		}
		epb := make([]byte, 20)
		binary.LittleEndian.PutUint32(epb[12:], uint32(len(f)))
		binary.LittleEndian.PutUint32(epb[16:], uint32(len(f)))
		out = append(out, block(6, append(epb, f...))...)
	}
	return out
}

// TestRead_CountsEveryFormatAlike: the same frames read the same in pcap of
// either byte order and in pcapng.
func TestRead_CountsEveryFormatAlike(t *testing.T) {
	for name, data := range map[string][]byte{
		"pcap little endian": pcapFile(binary.LittleEndian, frames()),
		"pcap big endian":    pcapFile(binary.BigEndian, frames()),
		"pcapng":             pcapngFile(frames()),
	} {
		s, err := pcapsum.Read(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := map[string]int{
			pcapsum.IKE: 3, pcapsum.ESP: 6, pcapsum.ESPInUDP: 1, pcapsum.ICMP: 1, pcapsum.GRE: 1,
			pcapsum.OSPF: 1, pcapsum.ARP: 1, pcapsum.NonIP: 1, pcapsum.BGP: 1,
		}
		for proto, n := range want {
			if s.Protocols[proto].Packets != n {
				t.Errorf("%s: %s = %d, want %d (all %v)", name, proto, s.Protocols[proto].Packets, n, s.Protocols)
			}
		}
		if s.Packets != 16 || s.Encrypted != 7 || s.KeyExchange != 3 || s.Cleartext != 4 {
			t.Errorf("%s: packets %d, encrypted %d, key exchange %d, cleartext %d", name, s.Packets, s.Encrypted, s.KeyExchange, s.Cleartext)
		}
		top := s.Pairs[0]
		if top.A != "172.16.1.1" || top.B != "172.16.2.1" || top.Encrypted != 7 || top.KeyExchange != 3 || top.Cleartext != 3 {
			t.Errorf("%s: the site pair = %+v", name, top)
		}
		if len(s.LinkTypes) != 1 || s.LinkTypes[0] != "ethernet" {
			t.Errorf("%s: link types %v", name, s.LinkTypes)
		}
	}
}

// TestRead_OnlyEncryptedIsCleartextFree: a capture of IKE and ESP alone has
// no cleartext, which is what "encrypted on the WAN" means.
func TestRead_OnlyEncryptedIsCleartextFree(t *testing.T) {
	s, err := pcapsum.Read(pcapFile(binary.LittleEndian, frames()[:8]))
	if err != nil || s.Cleartext != 0 || s.Encrypted != 6 || s.KeyExchange != 2 {
		t.Fatalf("%+v, %v", s, err)
	}
}

// TestRead_RefusesWhatIsNotACaptureAndKeepsWhatItRead: data that is no
// capture is refused; a record cut short ends the read with an error and
// what was counted before it.
func TestRead_RefusesWhatIsNotACaptureAndKeepsWhatItRead(t *testing.T) {
	if _, err := pcapsum.Read([]byte("<html>no</html>  padding to 24 bytes")); err == nil {
		t.Error("an HTML page was read as a capture")
	}
	data := pcapFile(binary.LittleEndian, frames())
	s, err := pcapsum.Read(data[:len(data)-5])
	if err == nil || s.Packets != 15 {
		t.Fatalf("a cut capture: %d packets, %v", s.Packets, err)
	}
	ng := pcapngFile(frames())
	if s, err := pcapsum.Read(ng[:len(ng)-3]); err == nil || s.Packets != 15 {
		t.Fatalf("a cut pcapng: %d packets, %v", s.Packets, err)
	}
}

// FuzzRead: no input makes Read panic, and what it counts adds up.
func FuzzRead(f *testing.F) {
	f.Add(pcapFile(binary.LittleEndian, frames()))
	f.Add(pcapFile(binary.BigEndian, frames()[:3]))
	f.Add(pcapngFile(frames()))
	f.Fuzz(func(t *testing.T, data []byte) {
		s, _ := pcapsum.Read(data)
		total := 0
		for _, c := range s.Protocols {
			total += c.Packets
		}
		if total+s.Unread != s.Packets {
			t.Fatalf("protocols count %d and unread %d, packets %d", total, s.Unread, s.Packets)
		}
	})
}
