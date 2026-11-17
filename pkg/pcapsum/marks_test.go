// Tests that a capture's DSCP marks, packet times and each flow's bytes are
// read, and that HTTP in the clear is management only to a management
// endpoint. The class mix mirrors the CML lab's QoS capture of 2026-11-17
// (voice-like EF, video-like AF41, CS6 network control, AF21 web, CS1
// scavenger), written here frame by frame with documentation addresses.
package pcapsum_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/pcapsum"
)

// marked is frame f, an untagged Ethernet frame carrying IPv4, with its DS
// field set to dscp.
func marked(f []byte, dscp int) []byte {
	f = append([]byte(nil), f...)
	f[15] = byte(dscp << 2)
	return f
}

// timedPcap writes frames as a classic pcap file, each record stamped with
// its time from at, in nanoseconds when nano is set and else in
// microseconds.
func timedPcap(order binary.ByteOrder, nano bool, frames [][]byte, at []time.Time) []byte {
	var b bytes.Buffer
	h := make([]byte, 24)
	magic := uint32(0xa1b2c3d4)
	if nano {
		magic = 0xa1b23c4d
	}
	order.PutUint32(h, magic)
	order.PutUint16(h[4:], 2)
	order.PutUint16(h[6:], 4)
	order.PutUint32(h[16:], 65535)
	order.PutUint32(h[20:], 1)
	b.Write(h)
	for i, f := range frames {
		r := make([]byte, 16)
		order.PutUint32(r, uint32(at[i].Unix()))
		frac := uint32(at[i].Nanosecond())
		if !nano {
			frac /= 1000
		}
		order.PutUint32(r[4:], frac)
		order.PutUint32(r[8:], uint32(len(f)))
		order.PutUint32(r[12:], uint32(len(f)))
		b.Write(r)
		b.Write(f)
	}
	return b.Bytes()
}

// timedPcapng writes frames as pcapng enhanced packet blocks on one
// Ethernet interface whose description carries opts, each stamped with its
// count of the interface's units.
func timedPcapng(opts []byte, counts []uint64, frames [][]byte) []byte {
	shb := binary.LittleEndian.AppendUint32(nil, 0x1a2b3c4d)
	shb = append(shb, 1, 0, 0, 0)
	shb = append(shb, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	out := block(0x0a0d0d0a, shb)
	out = append(out, block(1, append([]byte{1, 0, 0, 0, 0, 0, 0, 0}, opts...))...)
	for i, f := range frames {
		epb := make([]byte, 20)
		binary.LittleEndian.PutUint32(epb[4:], uint32(counts[i]>>32))
		binary.LittleEndian.PutUint32(epb[8:], uint32(counts[i]))
		binary.LittleEndian.PutUint32(epb[12:], uint32(len(f)))
		binary.LittleEndian.PutUint32(epb[16:], uint32(len(f)))
		out = append(out, block(6, append(epb, f...))...)
	}
	return out
}

// near reports whether two durations in seconds agree to a nanosecond,
// since a float of seconds need not equal its decimal spelling.
func near(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// tsresol is an if_tsresol option of value v, then the end of options.
func tsresol(v byte) []byte {
	return []byte{9, 0, 1, 0, v, 0, 0, 0, 0, 0, 0, 0}
}

// TestRead_CountsDSCPByClass: every IP packet is counted by its mark, with
// the codepoint's name and RFC 4594's service class, over the capture and
// over its pair, an IPv6 traffic class included; a value no standard names
// is counted with neither.
func TestRead_CountsDSCPByClass(t *testing.T) {
	voice := eth(0x0800, ipv4(host, siteA, 17, udp(16400, 16400, make([]byte, 160))), false)
	video := eth(0x0800, ipv4(host, siteA, 17, udp(16500, 16500, make([]byte, 1000))), false)
	ospf := eth(0x0800, ipv4(siteA, [4]byte{224, 0, 0, 5}, 89, make([]byte, 44)), false)
	var frames [][]byte
	for range 3 {
		frames = append(frames, marked(voice, 46), marked(video, 34), marked(ospf, 48))
	}
	frames = append(frames, marked(voice, 8), marked(voice, 63))
	// IPv6, traffic class AF21 (18 << 2 = 0x48): 0x6 0x48 across the first
	// two bytes' nibbles.
	v6 := ipv6(client6, server6, 17, udp(443, 50000, make([]byte, 20)))
	v6[0], v6[1] = 0x64, 0x80
	frames = append(frames, eth(0x86dd, v6, false))
	s := read(t, pcapFile(binary.LittleEndian, frames))
	want := map[int][3]any{
		34: {"AF41", "Multimedia Conferencing", 3},
		46: {"EF", "Telephony", 3},
		48: {"CS6", "Network Control", 3},
		8:  {"CS1", "Low-Priority Data", 1},
		63: {"", "", 1},
		18: {"AF21", "Low-Latency Data", 1},
	}
	if len(s.DSCP) != len(want) {
		t.Fatalf("dscp = %+v", s.DSCP)
	}
	for _, d := range s.DSCP {
		w := want[d.DSCP]
		if d.Name != w[0] || d.Class != w[1] || d.Packets != w[2] {
			t.Errorf("%+v, want %v", d, w)
		}
	}
	// Most bytes first: the video-like flow's large packets.
	if s.DSCP[0].DSCP != 34 || s.DSCP[0].Bytes != 3*int64(len(video)) {
		t.Errorf("first class %+v", s.DSCP[0])
	}
	for _, p := range s.Pairs {
		if p.A == "10.1.1.10" && p.B == "172.16.1.1" {
			// Voice, video, scavenger and the unnamed value; OSPF is another pair.
			if len(p.DSCP) != 4 {
				t.Errorf("pair %s-%s dscp %+v", p.A, p.B, p.DSCP)
			}
		}
	}
}

// TestRead_TimesEveryFormat: pcap's microsecond and nanosecond records and
// pcapng's default, decimal and binary resolutions give each packet its
// time; a resolution too fine to count, and a simple packet block, give
// none.
func TestRead_TimesEveryFormat(t *testing.T) {
	frame := eth(0x0800, ipv4(siteA, siteB, 1, []byte{8, 0, 0, 0, 0, 1, 0, 1}), false)
	frames := [][]byte{frame, frame, frame}
	first := time.Date(2026, 11, 17, 3, 17, 35, 564104000, time.UTC)
	at := []time.Time{first, first.Add(10890939 * time.Microsecond), first.Add(time.Second)}
	for name, data := range map[string][]byte{
		"pcap microseconds": timedPcap(binary.LittleEndian, false, frames, at),
		"pcap nanoseconds":  timedPcap(binary.BigEndian, true, frames, at),
	} {
		s := read(t, data)
		if !s.First.Equal(at[0]) || !s.Last.Equal(at[1]) || !near(s.DurationSeconds, 10.890939) {
			t.Errorf("%s: %v to %v, %v s", name, s.First, s.Last, s.DurationSeconds)
		}
	}
	micros := func(tm time.Time) uint64 { return uint64(tm.UnixMicro()) }
	counts := []uint64{micros(at[0]), micros(at[1]), micros(at[2])}
	nanos := []uint64{uint64(at[0].UnixNano()), uint64(at[1].UnixNano()), uint64(at[2].UnixNano())}
	// 2^-20 of a second: the count is seconds shifted 20 bits.
	binary20 := []uint64{uint64(at[0].Unix()) << 20, uint64(at[0].Unix()+10) << 20, uint64(at[0].Unix()+1) << 20}
	for name, tc := range map[string]struct {
		opts   []byte
		counts []uint64
		first  time.Time
		dur    float64
	}{
		"pcapng with no resolution": {nil, counts, at[0], 10.890939},
		"pcapng in nanoseconds":     {tsresol(9), nanos, at[0], 10.890939},
		"pcapng in 2^-20 seconds":   {tsresol(0x80 | 20), binary20, time.Unix(at[0].Unix(), 0).UTC(), 10},
	} {
		s := read(t, timedPcapng(tc.opts, tc.counts, frames))
		if !s.First.Equal(tc.first) || !near(s.DurationSeconds, tc.dur) {
			t.Errorf("%s: first %v, %v s", name, s.First, s.DurationSeconds)
		}
	}
	// 10^-20 does not fit a 64-bit count of a second: no time at all.
	if s := read(t, timedPcapng(tsresol(20), counts, frames)); !s.First.IsZero() || s.DurationSeconds != 0 {
		t.Errorf("an unreadable resolution gave %v, %v s", s.First, s.DurationSeconds)
	}
	// The simple packet block pcapngFile writes first has no time, so the
	// span is the enhanced blocks' alone.
	if s := read(t, pcapngFile(frames)); s.DurationSeconds != 0 || s.First.IsZero() {
		t.Errorf("simple and enhanced blocks: %v, %v s", s.First, s.DurationSeconds)
	}
}

// TestRead_AFlowKeepsItsBytesMarksAndSpan: a real HTTP/1.1 session's flow
// carries its frames' bytes, both directions' marks and its first and last
// packet's times.
func TestRead_AFlowKeepsItsBytesMarksAndSpan(t *testing.T) {
	segs := flowPackets(getSession(t, "/"))
	frames := tcpFrames(segs, 40000, 8080)
	var total int64
	at := make([]time.Time, len(frames))
	start := time.Date(2026, 11, 17, 3, 17, 38, 203390000, time.UTC)
	for i := range frames {
		// The client marks AF21 and the server answers unmarked, as the lab's
		// web server did.
		if segs[i].up {
			frames[i] = marked(frames[i], 18)
		}
		total += int64(len(frames[i]))
		at[i] = start.Add(time.Duration(i) * time.Millisecond)
	}
	f := onlyFlow(t, read(t, timedPcap(binary.LittleEndian, false, frames, at)))
	if f.Bytes != total || !f.First.Equal(at[0]) || !f.Last.Equal(at[len(at)-1]) || !near(f.DurationSeconds, float64(len(at)-1)/1000) {
		t.Errorf("flow %+v, want %d bytes over %d ms", f, total, len(at)-1)
	}
	if len(f.DSCP) != 2 || f.DSCP[0].Packets+f.DSCP[1].Packets != f.Packets {
		t.Errorf("dscp %+v", f.DSCP)
	}
}

// TestRead_HTTPIsManagementOnlyToAManagementEndpoint: a lab web server on
// 8080 answering GET / is cleartext and not management, as the CML lab's was
// flagged before; the same server answering RESTCONF's root is management by
// the API its requests named; and listed in Options, by address or by
// address and port, it is management whatever was asked.
func TestRead_HTTPIsManagementOnlyToAManagementEndpoint(t *testing.T) {
	web := sessionCapture(getSession(t, "/"), 40000, 8080)
	f := onlyFlow(t, read(t, web))
	if f.Protocol != pcapsum.HTTP || f.CleartextManagement || f.Management != "" || f.Cleartext != f.Packets {
		t.Errorf("a web server: %+v", f)
	}
	if s := read(t, web); s.CleartextManagement != 0 {
		t.Errorf("a web server counted as management: %d", s.CleartextManagement)
	}
	for _, path := range []string{"/restconf/data/ietf-interfaces:interfaces", "/.well-known/host-meta"} {
		f := onlyFlow(t, read(t, sessionCapture(getSession(t, path), 40000, 8080)))
		if !f.CleartextManagement || f.Management != pcapsum.ManagementRESTCONF {
			t.Errorf("%s: %+v", path, f)
		}
	}
	for _, listed := range []netip.AddrPort{netip.MustParseAddrPort("198.51.100.20:8080"), netip.AddrPortFrom(netip.MustParseAddr("198.51.100.20"), 0)} {
		s, err := pcapsum.ReadWith(web, pcapsum.Options{Management: []netip.AddrPort{listed}})
		if err != nil || s.CleartextManagement != 1 || s.Flows[0].Management != pcapsum.ManagementListed {
			t.Errorf("listed %s: %+v, %v", listed, s.Flows, err)
		}
	}
}

// getSession is a real HTTP/1.1 GET of path through net/http, answered with
// a small page, as a lab's python http.server answers.
func getSession(t *testing.T, path string) []chunk {
	t.Helper()
	l := listen(t)
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>lab</html>"))
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	dialed := make(chan *recorder, 1)
	tr := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		raw, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		rec := &recorder{Conn: raw}
		select {
		case dialed <- rec:
		default:
		}
		return rec, nil
	}}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr, Timeout: 10 * time.Second}).Get("http://" + l.Addr().String() + path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	select {
	case rec := <-dialed:
		return rec.recorded()
	default:
		t.Fatal("the client never dialed")
	}
	return nil
}
