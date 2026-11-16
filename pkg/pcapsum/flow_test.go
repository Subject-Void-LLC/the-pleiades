// Tests that a TCP flow is judged by its bytes: real SSH, TLS and plaintext
// gRPC sessions recorded over loopback and written as captures, then lost,
// repeated, reordered and cut segments, cleartext on SSH's and HTTPS's own
// ports, and ESP beside them all counted exactly as before.
package pcapsum_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/pcapsum"
)

// read is the summary of data, which must read whole.
func read(t *testing.T, data []byte) pcapsum.Summary {
	t.Helper()
	s, err := pcapsum.Read(data)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// onlyFlow is s's one flow.
func onlyFlow(t *testing.T, s pcapsum.Summary) pcapsum.Flow {
	t.Helper()
	if len(s.Flows) != 1 {
		t.Fatalf("flows = %+v", s.Flows)
	}
	return s.Flows[0]
}

// assertSealed: every packet of s is encrypted or key exchange, both of
// which it holds, and its one flow is proto's from the client to sport.
func assertSealed(t *testing.T, s pcapsum.Summary, proto string, sport uint16) {
	t.Helper()
	f := onlyFlow(t, s)
	if s.Cleartext != 0 || s.Encrypted == 0 || s.KeyExchange == 0 || s.Encrypted+s.KeyExchange != s.Packets {
		t.Errorf("encrypted %d, key exchange %d, cleartext %d of %d", s.Encrypted, s.KeyExchange, s.Cleartext, s.Packets)
	}
	if f.Protocol != proto || f.Cleartext != 0 || f.Packets != s.Packets || f.CleartextManagement || len(f.CleartextCredentials) != 0 {
		t.Errorf("flow = %+v", f)
	}
	if f.Client != "192.0.2.10:40000" || f.Server != fmt.Sprintf("198.51.100.20:%d", sport) {
		t.Errorf("flow ends: %s to %s", f.Client, f.Server)
	}
	if s.Protocols[proto].Packets != s.Packets || s.Pairs[0].Cleartext != 0 {
		t.Errorf("protocols %v, pair %+v", s.Protocols, s.Pairs[0])
	}
}

// TestRead_SSHIsKeyExchangeThenEncrypted: a real SSH session under each
// kind of cipher (a stream cipher with its length encrypted, an AEAD with
// its length in the clear, a block cipher with a MAC) is its banner and key
// exchange, then nothing but encrypted bytes, wherever its port.
func TestRead_SSHIsKeyExchangeThenEncrypted(t *testing.T) {
	for _, cipher := range []string{"chacha20-poly1305@openssh.com", "aes128-gcm@openssh.com", "aes128-ctr"} {
		t.Run(cipher, func(t *testing.T) {
			chunks := sshSession(t, cipher)
			assertSealed(t, read(t, sessionCapture(chunks, 40000, 830)), pcapsum.SSH, 830)
			// On a port nothing claims, it is still SSH.
			assertSealed(t, read(t, sessionCapture(chunks, 40000, 7001)), pcapsum.SSH, 7001)
		})
	}
}

// TestRead_TLSIsHandshakeThenEncrypted: a real TLS 1.2 and 1.3 session is
// handshake records, then application data, and the request it carried is
// not in the capture.
func TestRead_TLSIsHandshakeThenEncrypted(t *testing.T) {
	request := bytes.Repeat([]byte("GET /restconf/data/ietf-interfaces:interfaces "), 400)
	for name, v := range map[string]uint16{"1.2": 0x0303, "1.3": 0x0304} {
		t.Run(name, func(t *testing.T) {
			data := sessionCapture(tlsSession(t, v, request), 40000, 443)
			if bytes.Contains(data, []byte("ietf-interfaces")) {
				t.Fatal("the request is in the capture in the clear")
			}
			s := read(t, data)
			assertSealed(t, s, pcapsum.TLS, 443)
			// The request and its answer, many segments each way, are
			// application data: more of it than of the handshake.
			if s.Encrypted <= 2*s.KeyExchange {
				t.Errorf("encrypted %d, key exchange %d", s.Encrypted, s.KeyExchange)
			}
		})
	}
}

// TestRead_PlaintextGRPCIsNamedAndItsCredentialFlagged: real plaintext gRPC
// with gNMI's username and password metadata is cleartext management,
// named grpc, with both headers flagged by name. The values are
// Huffman-coded, so a search of the capture for them finds nothing, and no
// output holds either.
func TestRead_PlaintextGRPCIsNamedAndItsCredentialFlagged(t *testing.T) {
	user, pass := throwaway(t), throwaway(t)
	data := sessionCapture(grpcSession(t, user, pass), 40000, 50001)
	if bytes.Contains(data, []byte(pass)) || bytes.Contains(data, []byte(user)) {
		t.Fatal("a value is in the capture as plain bytes: this test would not need HPACK")
	}
	s := read(t, data)
	f := onlyFlow(t, s)
	if f.Protocol != pcapsum.GRPC || !f.CleartextManagement || f.Cleartext != s.Packets || f.Server != "198.51.100.20:50001" {
		t.Errorf("flow = %+v", f)
	}
	if !slices.Equal(f.CleartextCredentials, []string{"password", "username"}) || s.CleartextCredentials != 2 || s.CleartextManagement != 1 {
		t.Errorf("credentials %v, %d, management %d", f.CleartextCredentials, s.CleartextCredentials, s.CleartextManagement)
	}
	if s.Encrypted != 0 || s.KeyExchange != 0 || s.Cleartext != s.Packets {
		t.Errorf("kinds: %d %d %d of %d", s.Encrypted, s.KeyExchange, s.Cleartext, s.Packets)
	}
	assertNoValue(t, s, user, pass)
}

// assertNoValue fails when any of values is in s as JSON or as Go prints it.
func assertNoValue(t *testing.T, s pcapsum.Summary, values ...string) {
	t.Helper()
	js, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{string(js), fmt.Sprintf("%#v", s)} {
		for _, v := range values {
			if strings.Contains(out, v) {
				t.Fatal("a credential's value is in the summary")
			}
		}
	}
}

// TestRead_APortAloneNeverDecides: cleartext on SSH's, HTTPS's and
// NETCONF's own ports is cleartext, and so is a banner that SSH does not
// follow.
func TestRead_APortAloneNeverDecides(t *testing.T) {
	text := []chunk{{up: true, data: []byte("show running-config\r\n")}, {up: false, data: []byte("hostname core1\r\nenable secret 5 x\r\n")}}
	for _, port := range []uint16{22, 443, 830} {
		s := read(t, sessionCapture(text, 40000, port))
		if s.Cleartext != s.Packets || s.Encrypted != 0 || s.KeyExchange != 0 || len(s.Flows) != 0 || s.Protocols[pcapsum.TCP].Packets != s.Packets {
			t.Errorf("port %d: %+v", port, s)
		}
	}
	banner := []chunk{{up: false, data: []byte("SSH-2.0-OpenSSH_9.6\r\n")}, {up: false, data: []byte("but then this is just text, not a packet\r\n")}}
	s := read(t, sessionCapture(banner, 40000, 22))
	if f := onlyFlow(t, s); f.Protocol != pcapsum.SSH || f.Cleartext == 0 || f.Encrypted != 0 {
		t.Errorf("a banner and then text: %+v", f)
	}
	// TLS's record header with a ServerHello's type, then text where the
	// next record header should be.
	fake := []chunk{{up: false, data: append([]byte{0x16, 3, 3, 0, 4, 2, 0, 0, 0}, "plain text after a record"...)}}
	s = read(t, sessionCapture(fake, 40000, 443))
	if f := onlyFlow(t, s); f.Protocol != pcapsum.TLS || f.Cleartext == 0 || f.Encrypted != 0 {
		t.Errorf("a record and then text: %+v", f)
	}
}

// dataIndexes are the indexes in segs of the server's segments carrying
// data.
func dataIndexes(segs []pkt, up bool) []int {
	var out []int
	for i, s := range segs {
		if s.up == up && len(s.data) > 0 {
			out = append(out, i)
		}
	}
	return out
}

// recordInside sorts the server's data segments of a TLS session by
// whether a record header begins inside one (after its first byte): a
// segment with none lies within one record, or begins one (within); one
// whose first record header begins past its 64th byte is crossing, so a cut
// at 96 bytes of frame hides that header.
func recordInside(segs []pkt) (within, crossing []int) {
	var stream []byte
	offs := map[int]int{}
	for _, i := range dataIndexes(segs, false) {
		offs[i] = len(stream)
		stream = append(stream, segs[i].data...)
	}
	starts := map[int]bool{}
	for off := 0; off+5 <= len(stream); off += 5 + int(binary.BigEndian.Uint16(stream[off+3:])) {
		starts[off] = true
	}
	for _, i := range dataIndexes(segs, false) {
		first := 0
		for b := offs[i] + 1; b < offs[i]+len(segs[i].data) && first == 0; b++ {
			if starts[b] {
				first = b - offs[i]
			}
		}
		switch {
		case first == 0:
			within = append(within, i)
		case first > 64:
			crossing = append(crossing, i)
		}
	}
	return within, crossing
}

// TestRead_SegmentsAreReadInSequenceOrder: a TLS session whose segments
// were retransmitted, arrived out of order, or were lost inside a record
// is read exactly as it was sent: still all handshake and encrypted.
func TestRead_SegmentsAreReadInSequenceOrder(t *testing.T) {
	// A long answer: records of 16 KiB, each across many segments.
	segs := flowPackets(tlsSession(t, 0x0304, bytes.Repeat([]byte("x"), 64<<10)))
	within, crossing := recordInside(segs)
	mid := within[len(within)/2]
	// A segment holding a record header past its start, far from mid: read
	// in arrival order, the one after it would begin mid-record, past a
	// header never read.
	late := -1
	for _, i := range crossing {
		if (i > mid+10 || i < mid-10) && i+2 < len(segs) && !segs[i+2].up && len(segs[i+2].data) > 0 {
			late = i
		}
	}
	if late < 0 {
		t.Fatalf("no record header to reorder around: %v", crossing)
	}
	var changed []pkt
	for i, s := range segs {
		switch {
		case i == mid:
			// Lost: inside a record, its bytes are skipped by length.
			continue
		case i == mid+2:
			// Retransmitted, twice.
			changed = append(changed, s, s, s)
		case i == late:
			// Arrives after the segment that follows it.
			changed = append(changed, segs[i+2], s)
		case i == late+2:
			// Already sent above.
		default:
			changed = append(changed, s)
		}
	}
	assertSealed(t, read(t, pcapFile(binary.LittleEndian, tcpFrames(changed, 40000, 443))), pcapsum.TLS, 443)
}

// TestRead_ALostRecordHeaderLosesTheFraming: when the capture lacks bytes
// past a record's end, what follows is cleartext, not encrypted, until a
// segment begins with a record header again.
func TestRead_ALostRecordHeaderLosesTheFraming(t *testing.T) {
	segs := flowPackets(tlsSession(t, 0x0304, bytes.Repeat([]byte("y"), 64<<10)))
	_, crossing := recordInside(segs)
	if len(crossing) == 0 {
		t.Fatal("no segment holds a record header past its start")
	}
	// Lose one segment a record header begins inside of.
	lost := crossing[len(crossing)/2]
	var kept []pkt
	for i, s := range segs {
		if i != lost {
			kept = append(kept, s)
		}
	}
	s := read(t, pcapFile(binary.LittleEndian, tcpFrames(kept, 40000, 443)))
	if f := onlyFlow(t, s); f.Protocol != pcapsum.TLS || f.Cleartext == 0 || f.Encrypted == 0 {
		t.Errorf("a lost record header: %+v", f)
	}
}

// TestRead_AFlowWhoseStartIsMissingIsNotCalledEncrypted: a capture begun
// mid-session holds encrypted bytes it cannot prove are, so they are
// cleartext TCP.
func TestRead_AFlowWhoseStartIsMissingIsNotCalledEncrypted(t *testing.T) {
	segs := flowPackets(sshSession(t, "aes128-gcm@openssh.com"))
	s := read(t, pcapFile(binary.LittleEndian, tcpFrames(segs[len(segs)/2:], 40000, 22)))
	if s.Encrypted != 0 || s.Cleartext != s.Packets || len(s.Flows) != 0 {
		t.Errorf("a session's second half: %+v", s)
	}
}

// TestRead_ASnapshotLengthKeepsTheFramingInsideARecord: frames cut at 96
// bytes, inside a record, are read past by the record's length and the IP
// header's, and the session is still all handshake and encrypted. A cut
// that hides a record header loses the framing as a lost segment does.
func TestRead_ASnapshotLengthKeepsTheFramingInsideARecord(t *testing.T) {
	segs := flowPackets(tlsSession(t, 0x0304, bytes.Repeat([]byte("z"), 64<<10)))
	within, crossing := recordInside(segs)
	for _, i := range within {
		segs[i].snap = 96
	}
	assertSealed(t, read(t, pcapFile(binary.LittleEndian, tcpFrames(segs, 40000, 443))), pcapsum.TLS, 443)
	if len(crossing) == 0 {
		t.Fatal("no segment holds a record header past its start")
	}
	segs[crossing[len(crossing)/2]].snap = 96
	if f := onlyFlow(t, read(t, pcapFile(binary.LittleEndian, tcpFrames(segs, 40000, 443)))); f.Cleartext == 0 {
		t.Errorf("a cut record header: %+v", f)
	}
}

// TestRead_ESPBesideFlowsIsCountedAsBefore: IKE, ESP and the rest counted
// exactly as without the flows, beside an SSH session and a cleartext one.
func TestRead_ESPBesideFlowsIsCountedAsBefore(t *testing.T) {
	ssh := tcpFrames(flowPackets(sshSession(t, "aes128-ctr")), 40000, 22)
	text := tcpFrames(flowPackets([]chunk{{up: true, data: []byte("hello\r\n")}}), 40001, 23)
	all := append(append(frames(), ssh...), text...)
	s := read(t, pcapFile(binary.BigEndian, all))
	for proto, n := range map[string]int{pcapsum.IKE: 3, pcapsum.ESP: 6, pcapsum.ESPInUDP: 1, pcapsum.GRE: 1, pcapsum.BGP: 1, pcapsum.SSH: len(ssh), pcapsum.TCP: len(text)} {
		if s.Protocols[proto].Packets != n {
			t.Errorf("%s = %d, want %d", proto, s.Protocols[proto].Packets, n)
		}
	}
	f := onlyFlow(t, s)
	if s.Encrypted != 7+f.Encrypted || s.KeyExchange != 3+f.KeyExchange || s.Cleartext != 4+len(text) || f.Cleartext != 0 {
		t.Errorf("encrypted %d, key exchange %d, cleartext %d; ssh %+v", s.Encrypted, s.KeyExchange, s.Cleartext, f)
	}
	if top := s.Pairs[0]; top.A != "172.16.1.1" && top.A != "192.0.2.10" {
		t.Errorf("pairs: %+v", s.Pairs)
	}
	for _, p := range s.Pairs {
		if p.A == "172.16.1.1" && (p.Encrypted != 7 || p.KeyExchange != 3 || p.Cleartext != 3) {
			t.Errorf("the site pair = %+v", p)
		}
	}
}
