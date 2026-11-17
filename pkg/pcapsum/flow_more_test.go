// More flow tests: IPv6 (with extension headers and fragments) on raw and
// Linux cooked link types, a capture that missed the handshake's SYNs, one
// direction alone, the order flows are listed in, and segments lost inside
// SSH and inside a cleartext flow.
package pcapsum_test

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/pcapsum"
)

// The two IPv6 hosts.
var (
	client6 = [16]byte{0x20, 0x01, 0x0d, 0xb8, 15: 0x10}
	server6 = [16]byte{0x20, 0x01, 0x0d, 0xb8, 15: 0x20}
)

// ipv6 is an IPv6 packet from src to dst whose first header is next,
// carrying payload (any extension headers included).
func ipv6(src, dst [16]byte, next byte, payload []byte) []byte {
	h := make([]byte, 40)
	h[0] = 0x60
	binary.BigEndian.PutUint16(h[4:], uint16(len(payload)))
	h[6], h[7] = next, 64
	copy(h[8:], src[:])
	copy(h[24:], dst[:])
	return append(h, payload...)
}

// pcapLink writes frames as a little-endian pcap of link type link.
func pcapLink(link uint32, frames [][]byte) []byte {
	data := pcapFile(binary.LittleEndian, frames)
	binary.LittleEndian.PutUint32(data[20:], link)
	return data
}

// v6Frames are segs as IPv6 packets, each TCP header behind a hop-by-hop
// options header, as raw IP (sll false) or Linux cooked frames.
func v6Frames(segs []pkt, sll bool) [][]byte {
	var out [][]byte
	for _, f := range tcpFrames(segs, 40000, 22) {
		tcp := f[14+20:]
		src, dst := client6, server6
		if binary.BigEndian.Uint16(tcp) != 40000 {
			src, dst = server6, client6
		}
		// A hop-by-hop header of 8 bytes, its next header TCP.
		hbh := append([]byte{6, 0}, make([]byte, 6)...)
		// Cut Ethernet's padding: the IP header gives the TCP length.
		ipLen := int(binary.BigEndian.Uint16(f[14+2:]))
		pkt6 := ipv6(src, dst, 0, append(hbh, f[14+20:14+ipLen]...))
		if sll {
			hdr := make([]byte, 16)
			binary.BigEndian.PutUint16(hdr[14:], 0x86dd)
			pkt6 = append(hdr, pkt6...)
		}
		out = append(out, pkt6)
	}
	return out
}

// TestRead_NamesEveryProtocolAsBefore: each IP protocol and port this
// package names, as raw IP, on a Linux cooked v2 capture, and frames of a
// link type it does not read counted unread.
func TestRead_NamesEveryProtocolAsBefore(t *testing.T) {
	a, b := siteA, siteB
	cases := []struct {
		pkt  []byte
		want string
	}{
		{ipv4(a, b, 51, make([]byte, 24)), pcapsum.AH},
		{ipv4(a, b, 88, make([]byte, 20)), pcapsum.EIGRP},
		{ipv4(a, b, 103, make([]byte, 20)), pcapsum.PIM},
		{ipv4(a, b, 112, make([]byte, 20)), pcapsum.VRRP},
		{ipv4(a, b, 200, make([]byte, 20)), pcapsum.OtherIP},
		{ipv4(a, b, 17, udp(1985, 1985, make([]byte, 20))), pcapsum.HSRP},
		{ipv4(a, b, 17, udp(53, 40000, make([]byte, 20))), pcapsum.UDP},
		{ipv4(a, b, 17, []byte{0, 53}), pcapsum.UDP},
		{ipv4(a, b, 17, udp(4500, 4500, []byte{0xff})), pcapsum.IKE},
		{ipv6(client6, server6, 58, make([]byte, 8)), pcapsum.ICMPv6},
	}
	var frames [][]byte
	for _, c := range cases {
		// Linux cooked v2: the protocol type first, then 18 more bytes.
		et := uint16(0x0800)
		if c.pkt[0]>>4 == 6 {
			et = 0x86dd
		}
		frames = append(frames, append(binary.BigEndian.AppendUint16(nil, et), append(make([]byte, 18), c.pkt...)...))
	}
	s := read(t, pcapLink(276, frames))
	for _, c := range cases {
		if s.Protocols[c.want].Packets == 0 {
			t.Errorf("%s not counted: %v", c.want, s.Protocols)
		}
	}
	if s.LinkTypes[0] != "linux_sll2" || s.KeyExchange != 1 || s.Packets != len(cases) {
		t.Errorf("%+v", s)
	}
	if s := read(t, pcapLink(147, frames[:2])); s.Unread != 2 || s.LinkTypes[0] != "linktype_147" {
		t.Errorf("an unread link type: %+v", s)
	}
}

// TestRead_SSHOverIPv6IsReadOnEveryLinkType: an SSH session over IPv6,
// behind an extension header, as raw IP and as Linux cooked frames.
func TestRead_SSHOverIPv6IsReadOnEveryLinkType(t *testing.T) {
	segs := flowPackets(sshSession(t, "aes128-gcm@openssh.com"))
	for name, data := range map[string][]byte{
		"raw":       pcapLink(101, v6Frames(segs, false)),
		"linux_sll": pcapLink(113, v6Frames(segs, true)),
	} {
		s := read(t, data)
		f := onlyFlow(t, s)
		if f.Protocol != pcapsum.SSH || s.Cleartext != 0 || f.Client != "[2001:db8::10]:40000" || f.Server != "[2001:db8::20]:22" {
			t.Errorf("%s: %+v, cleartext %d", name, f, s.Cleartext)
		}
	}
	// A fragment, and a chain cut short, are counted by name and read as no
	// flow.
	frag := ipv6(client6, server6, 44, append([]byte{6, 0, 0, 1, 0, 0, 0, 1}, make([]byte, 24)...))
	cut := ipv6(client6, server6, 0, []byte{6, 9, 0, 0})
	s := read(t, pcapLink(101, [][]byte{frag, cut}))
	if s.Protocols[pcapsum.TCP].Packets != 1 || s.Protocols[pcapsum.OtherIP].Packets != 1 || len(s.Flows) != 0 || s.Cleartext != 2 {
		t.Errorf("a fragment and a cut chain: %+v", s)
	}
}

// TestRead_AFlowWithoutItsSYNsIsReadFromItsFirstBytes: the client is the
// side that sent the ClientHello, or not the ServerHello, or else the
// higher port; the bytes read the same.
func TestRead_AFlowWithoutItsSYNsIsReadFromItsFirstBytes(t *testing.T) {
	segs := flowPackets(tlsSession(t, 0x0304, []byte("show version")))[3:]
	assertSealed(t, read(t, pcapFile(binary.LittleEndian, tcpFrames(segs, 40000, 443))), pcapsum.TLS, 443)
	var down []pkt
	for _, s := range segs {
		if !s.up {
			down = append(down, s)
		}
	}
	if f := onlyFlow(t, read(t, pcapFile(binary.LittleEndian, tcpFrames(down, 40000, 443)))); f.Client != "192.0.2.10:40000" || f.Protocol != pcapsum.TLS {
		t.Errorf("the server's side alone: %+v", f)
	}
	ssh := flowPackets(sshSession(t, "aes128-ctr"))[3:]
	// The client's port is the lower one here, so the port rule names the
	// other side: the bytes cannot say which side is which.
	if f := onlyFlow(t, read(t, pcapFile(binary.LittleEndian, tcpFrames(ssh, 2200, 40022)))); f.Client != "198.51.100.20:40022" || f.Cleartext != 0 {
		t.Errorf("SSH without SYNs: %+v", f)
	}
}

// TestRead_FlowsListCredentialsFirstThenCleartextManagement: a flow that
// carried a credential is listed before one only in the clear, and both
// before an encrypted one with more packets. The HTTP/2 server on 8080 names
// no management API, so it is management only once Options lists it.
func TestRead_FlowsListCredentialsFirstThenCleartextManagement(t *testing.T) {
	c := newH2Writer()
	c.buf.WriteString("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n")
	if err := c.fr.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	plainH2 := append([]byte(nil), c.buf.Bytes()...)
	user, pass := throwaway(t), throwaway(t)
	var frames [][]byte
	frames = append(frames, tcpFrames(flowPackets(sshSession(t, "aes128-ctr")), 40000, 22)...)
	frames = append(frames, tcpFrames(flowPackets([]chunk{{up: true, data: plainH2}}), 40001, 8080)...)
	frames = append(frames, tcpFrames(flowPackets(grpcSession(t, user, pass)), 40002, 50001)...)
	capture := pcapFile(binary.LittleEndian, frames)
	for _, listed := range []netip.AddrPort{netip.MustParseAddrPort("198.51.100.20:8080"), netip.AddrPortFrom(netip.MustParseAddr("198.51.100.20"), 0)} {
		s, err := pcapsum.ReadWith(capture, pcapsum.Options{Management: []netip.AddrPort{listed}})
		if err != nil {
			t.Fatal(err)
		}
		var order []string
		for _, f := range s.Flows {
			order = append(order, f.Protocol)
		}
		if len(order) != 3 || order[0] != pcapsum.GRPC || order[1] != pcapsum.HTTP2 || order[2] != pcapsum.SSH {
			t.Errorf("order = %v", order)
		}
		if s.CleartextManagement != 2 || s.CleartextCredentials != 2 || s.Flows[1].Management != pcapsum.ManagementListed {
			t.Errorf("management %d, credentials %d, %+v", s.CleartextManagement, s.CleartextCredentials, s.Flows[1])
		}
		assertNoValue(t, s, user, pass)
	}
	// Unlisted, the HTTP/2 server is cleartext and not management, and the
	// gRPC flow still is, by the gNMI service it called.
	s := read(t, capture)
	if s.CleartextManagement != 1 || s.Flows[0].Management != pcapsum.ManagementGNMI || s.Flows[2].Protocol != pcapsum.HTTP2 || s.Flows[2].CleartextManagement || s.Flows[2].Cleartext == 0 {
		t.Errorf("unlisted: management %d, flows %+v", s.CleartextManagement, s.Flows)
	}
	// A list naming another port on the server, or another address, does
	// not make it one.
	for _, other := range []string{"198.51.100.20:80", "198.51.100.21:8080"} {
		s, err := pcapsum.ReadWith(capture, pcapsum.Options{Management: []netip.AddrPort{netip.MustParseAddrPort(other)}})
		if err != nil || s.CleartextManagement != 1 {
			t.Errorf("listing %s: management %d, %v", other, s.CleartextManagement, err)
		}
	}
}

// TestRead_SSHGaps: a segment lost after NEWKEYS leaves the rest
// encrypted, one lost in the banner loses the framing, and a lost segment
// of a cleartext flow is still cleartext around it.
func TestRead_SSHGaps(t *testing.T) {
	segs := flowPackets(sshSession(t, "chacha20-poly1305@openssh.com"))
	data := dataIndexes(segs, true)
	late := data[len(data)-2]
	drop := func(i int) []pkt {
		return append(append([]pkt(nil), segs[:i]...), segs[i+1:]...)
	}
	assertSealed(t, read(t, pcapFile(binary.LittleEndian, tcpFrames(drop(late), 40000, 22))), pcapsum.SSH, 22)
	// The client's banner, then its KEXINIT, sent apart: losing the first
	// leaves the second unreadable as the opening, so the direction is
	// cleartext.
	banner := []chunk{
		{up: true, data: []byte("SSH-2.0-Go\r\n")}, {up: false, data: []byte("SSH-2.0-OpenSSH_9.6\r\n")},
		{up: true, data: append([]byte{0, 0, 0, 12, 10, 20}, make([]byte, 10)...)},
	}
	bs := flowPackets(banner)
	if f := onlyFlow(t, read(t, pcapFile(binary.LittleEndian, tcpFrames(append(append([]pkt(nil), bs[:3]...), bs[5:]...), 40000, 22)))); f.Cleartext == 0 {
		t.Errorf("a lost banner: %+v", f)
	}
	// A KEXINIT of 4096 bytes across three segments, the second lost: its
	// length carries the read past the gap to NEWKEYS.
	kexinit := append([]byte{0, 0, 0x0f, 0xfc, 4, 20}, make([]byte, 4092-2)...)
	newkeys := []byte{0, 0, 0, 12, 10, 21, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	big := flowPackets([]chunk{
		{up: false, data: []byte("SSH-2.0-OpenSSH_9.6\r\n")},
		{up: true, data: append(append(append([]byte("SSH-2.0-Go\r\n"), kexinit...), newkeys...), noise(t, 900)...)},
	})
	up := dataIndexes(big, true)
	lost := append(append([]pkt(nil), big[:up[1]]...), big[up[1]+1:]...)
	assertSealed(t, read(t, pcapFile(binary.LittleEndian, tcpFrames(lost, 40000, 22))), pcapsum.SSH, 22)
	text := flowPackets([]chunk{{up: true, data: bytes.Repeat([]byte("show interfaces\r\n"), 300)}})
	s := read(t, pcapFile(binary.LittleEndian, tcpFrames(append(append([]pkt(nil), text[:5]...), text[6:]...), 40000, 23)))
	if s.Cleartext != s.Packets || len(s.Flows) != 0 {
		t.Errorf("a cleartext flow with a gap: %+v", s)
	}
}
