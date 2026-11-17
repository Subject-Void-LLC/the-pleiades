// Tests of net.pcap.summary through the command line's own project files: a
// capture of ESP and one cleartext ICMP counted as such, its pair listed, a
// pairs limit, a file that is no capture refused, and a run with no project
// refused.
package pcap_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/pcap"
)

// frame is an Ethernet frame carrying an IPv4 packet of proto from a to b.
func frame(a, b [4]byte, proto byte, payload []byte) []byte {
	ip := make([]byte, 20)
	ip[0], ip[8], ip[9] = 0x45, 64, proto
	binary.BigEndian.PutUint16(ip[2:], uint16(20+len(payload)))
	copy(ip[12:], a[:])
	copy(ip[16:], b[:])
	f := append(make([]byte, 12), 0x08, 0x00)
	return append(append(f, ip...), payload...)
}

// capture is a little-endian pcap of frames.
func capture(frames ...[]byte) []byte {
	h := make([]byte, 24)
	binary.LittleEndian.PutUint32(h, 0xa1b2c3d4)
	binary.LittleEndian.PutUint16(h[4:], 2)
	binary.LittleEndian.PutUint16(h[6:], 4)
	binary.LittleEndian.PutUint32(h[16:], 65535)
	binary.LittleEndian.PutUint32(h[20:], 1)
	for _, f := range frames {
		r := make([]byte, 16)
		binary.LittleEndian.PutUint32(r[8:], uint32(len(f)))
		binary.LittleEndian.PutUint32(r[12:], uint32(len(f)))
		h = append(append(h, r...), f...)
	}
	return h
}

// run calls the method with params in a project at dir.
func run(t *testing.T, dir string, params map[string]any) (collection.Result, sdk.RunbookContext, error) {
	t.Helper()
	d, ok := collection.Lookup("net.pcap.summary")
	if !ok {
		t.Fatal("net.pcap.summary is not registered")
	}
	rc, err := engine.WithProjectFiles(engine.NewDeviceRunbookContext, dir)(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := d.Invoke(context.Background(), rc, nil, params)
	return res, rc, err
}

// tcpFrame is an Ethernet frame of a TCP segment from a:ap to b:bp.
func tcpFrame(a, b [4]byte, ap, bp uint16, flags byte, seq uint32, data []byte) []byte {
	h := make([]byte, 20)
	binary.BigEndian.PutUint16(h, ap)
	binary.BigEndian.PutUint16(h[2:], bp)
	binary.BigEndian.PutUint32(h[4:], seq)
	h[12], h[13] = 5<<4, flags
	return frame(a, b, 6, append(h, data...))
}

// gnmiRequest is a plaintext gRPC request as the real HTTP/2 framer and
// HPACK encoder write it: the client preface, then a HEADERS frame with
// gNMI's username and password metadata.
func gnmiRequest(t *testing.T, user, pass string) []byte {
	t.Helper()
	var hb, out bytes.Buffer
	enc := hpack.NewEncoder(&hb)
	for _, f := range []hpack.HeaderField{
		{Name: ":method", Value: "POST"}, {Name: ":path", Value: "/gnmi.gNMI/Capabilities"},
		{Name: "content-type", Value: "application/grpc"}, {Name: "username", Value: user}, {Name: "password", Value: pass},
	} {
		if err := enc.WriteField(f); err != nil {
			t.Fatal(err)
		}
	}
	out.WriteString(http2.ClientPreface)
	if err := http2.NewFramer(&out, nil).WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hb.Bytes(), EndHeaders: true}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// TestSummary_NamesCleartextGRPCAndWarnsOfItsCredential: plaintext gNMI is
// listed as a grpc flow in the clear, its credential headers named in the
// stats and in a warning, and neither value is anywhere in what the method
// recorded or returned.
func TestSummary_NamesCleartextGRPCAndWarnsOfItsCredential(t *testing.T) {
	dir := t.TempDir()
	user, pass := "u"+strings.Repeat("7", 11), strings.Repeat("9", 10)+"q"
	cl, sv := [4]byte{10, 0, 0, 1}, [4]byte{10, 0, 0, 2}
	data := capture(
		tcpFrame(cl, sv, 47814, 50001, 0x02, 99, nil),
		tcpFrame(sv, cl, 50001, 47814, 0x12, 499, nil),
		tcpFrame(cl, sv, 47814, 50001, 0x18, 100, gnmiRequest(t, user, pass)),
	)
	if err := os.MkdirAll(filepath.Join(dir, "captures"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "captures", "gnmi.pcap"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	res, rc, err := run(t, dir, map[string]any{"src": "captures/gnmi.pcap"})
	if err != nil {
		t.Fatal(err)
	}
	stats := rc.(interface{ Facts() map[string]any }).Facts()
	if stats["cleartext_credentials"] != 2 || stats["cleartext_management"] != 1 || stats["cleartext"] != 3 || stats["encrypted"] != 0 {
		t.Fatalf("stats = %v", stats)
	}
	fl := stats["flows"].([]any)
	f := fl[0].(map[string]any)
	if len(fl) != 1 || f["protocol"] != "grpc" || f["client"] != "10.0.0.1:47814" || f["server"] != "10.0.0.2:50001" || f["cleartext_management"] != true {
		t.Fatalf("flows = %v", fl)
	}
	if creds := f["cleartext_credentials"].([]any); len(creds) != 2 || creds[0] != "password" || creds[1] != "username" {
		t.Fatalf("credentials = %v", creds)
	}
	warns, _ := stats[sdk.StatWarnings].([]string)
	if len(warns) != 1 || !strings.Contains(warns[0], "password, username") || !strings.Contains(warns[0], "10.0.0.2:50001") {
		t.Fatalf("warnings = %q", warns)
	}
	if out := fmt.Sprintf("%v %v", stats, res); strings.Contains(out, user) || strings.Contains(out, pass) {
		t.Fatal("a credential's value is in the method's output")
	}
	if _, rc, err := run(t, dir, map[string]any{"src": "captures/gnmi.pcap", "flows": 0}); err != nil || len(rc.(interface{ Facts() map[string]any }).Facts()["flows"].([]any)) != 0 {
		t.Fatalf("flows 0: %v", err)
	}
	if _, _, err := run(t, dir, map[string]any{"src": "captures/gnmi.pcap", "flows": -1}); err == nil {
		t.Fatal("a negative flows was taken")
	}
}

// TestSummary_WarnsOfTextInEncryptedAndUnsearchedHeaders: a TLS record
// forged around text is a warning and cleartext_in_encrypted, and an
// HTTP/2 block that will not decode is counted in headers_unread and named
// in its flow's warning.
func TestSummary_WarnsOfTextInEncryptedAndUnsearchedHeaders(t *testing.T) {
	dir := t.TempDir()
	cl, sv := [4]byte{10, 0, 0, 1}, [4]byte{10, 0, 0, 2}
	forged := append([]byte{0x16, 3, 1, 0, 4, 1, 0, 0, 0, 0x17, 3, 3, 0x01, 0x00}, []byte(strings.Repeat("show version\r\n", 20)[:256])...)
	var h2 bytes.Buffer
	h2.WriteString(http2.ClientPreface)
	if err := http2.NewFramer(&h2, nil).WriteHeaders(http2.HeadersFrameParam{StreamID: 1, EndHeaders: true, BlockFragment: []byte{0x80 | 70}}); err != nil {
		t.Fatal(err)
	}
	data := capture(tcpFrame(cl, sv, 40000, 443, 0x18, 1, forged), tcpFrame(cl, sv, 40001, 8080, 0x18, 1, h2.Bytes()))
	if err := os.WriteFile(filepath.Join(dir, "w.pcap"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, rc, err := run(t, dir, map[string]any{"src": "w.pcap"})
	if err != nil {
		t.Fatal(err)
	}
	stats := rc.(interface{ Facts() map[string]any }).Facts()
	warns, _ := stats[sdk.StatWarnings].([]string)
	if stats["cleartext_in_encrypted"] != 1 || stats["headers_unread"] != 1 || stats["encrypted"] != 0 || len(warns) != 2 {
		t.Fatalf("stats = %v", stats)
	}
	if !strings.Contains(warns[0], "read as text") || !strings.Contains(warns[1], "1 of its headers or header blocks could not be searched") {
		t.Fatalf("warnings = %q", warns)
	}
	f := stats["flows"].([]any)[1].(map[string]any)
	if f["headers_unread"] != 1 || f["protocol"] != "http2" {
		t.Fatalf("flows = %v", stats["flows"])
	}
}

// TestSummary_WarningsNameTenFlowsAndCountTheRest: eleven cleartext flows
// to a listed management endpoint make ten warning lines and one counting
// the eleventh. Unlisted, the same server asked for "/" is a web server:
// cleartext, and neither management nor warned of, as the CML lab's
// python http.server on 8080 was before.
func TestSummary_WarningsNameTenFlowsAndCountTheRest(t *testing.T) {
	dir := t.TempDir()
	cl, sv := [4]byte{10, 0, 0, 1}, [4]byte{10, 0, 0, 2}
	var frames [][]byte
	for i := range 11 {
		var hb, out bytes.Buffer
		if err := hpack.NewEncoder(&hb).WriteField(hpack.HeaderField{Name: ":path", Value: "/"}); err != nil {
			t.Fatal(err)
		}
		out.WriteString(http2.ClientPreface)
		if err := http2.NewFramer(&out, nil).WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hb.Bytes(), EndHeaders: true}); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, tcpFrame(cl, sv, uint16(40000+i), 8080, 0x18, 1, out.Bytes()))
	}
	if err := os.WriteFile(filepath.Join(dir, "h2.pcap"), capture(frames...), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, listed := range []any{"10.0.0.2:8080", "10.0.0.2"} {
		_, rc, err := run(t, dir, map[string]any{"src": "h2.pcap", "flows": 3, "management": []any{listed}})
		if err != nil {
			t.Fatal(err)
		}
		stats := rc.(interface{ Facts() map[string]any }).Facts()
		warns, _ := stats[sdk.StatWarnings].([]string)
		if len(warns) != 11 || !strings.Contains(warns[10], "and 1 more") || !strings.Contains(warns[0], "http2 from 10.0.0.1:") || !strings.Contains(warns[0], "management: listed") || len(stats["flows"].([]any)) != 3 {
			t.Fatalf("warnings = %q, flows %v", warns, stats["flows"])
		}
		if f := stats["flows"].([]any)[0].(map[string]any); f["management"] != "listed" || stats["cleartext_management"] != 11 {
			t.Fatalf("listed %v: %v, %v", listed, f, stats["cleartext_management"])
		}
	}
	_, rc, err := run(t, dir, map[string]any{"src": "h2.pcap"})
	if err != nil {
		t.Fatal(err)
	}
	stats := rc.(interface{ Facts() map[string]any }).Facts()
	if warns, _ := stats[sdk.StatWarnings].([]string); len(warns) != 0 || stats["cleartext_management"] != 0 || stats["cleartext"] != 11 {
		t.Fatalf("a web server: warnings %q, stats %v", warns, stats)
	}
	if _, _, err := run(t, dir, map[string]any{"src": "h2.pcap", "management": []any{"r1.lab"}}); err == nil || !strings.Contains(err.Error(), "neither an address") {
		t.Fatalf("a host name in management: %v", err)
	}
	if _, _, err := run(t, dir, map[string]any{"src": "h2.pcap", "management": "10.0.0.2"}); err == nil {
		t.Fatal("management that is not a list was taken")
	}
}

// TestSummary_CountsAWANCapture is the method on a capture of two sites'
// ESP and one ICMP between them in the clear.
func TestSummary_CountsAWANCapture(t *testing.T) {
	dir := t.TempDir()
	a, b, c := [4]byte{172, 16, 1, 1}, [4]byte{172, 16, 2, 1}, [4]byte{10, 0, 0, 9}
	esp := make([]byte, 40)
	data := capture(frame(a, b, 50, esp), frame(b, a, 50, esp), frame(a, b, 1, []byte{8, 0, 0, 0, 0, 1, 0, 1}), frame(c, b, 50, esp))
	if err := os.MkdirAll(filepath.Join(dir, "captures"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "captures", "wan.pcap"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	res, rc, err := run(t, dir, map[string]any{"src": "captures/wan.pcap"})
	if err != nil || res.Changed {
		t.Fatalf("%+v, %v", res, err)
	}
	stats := rc.(interface{ Facts() map[string]any }).Facts()
	if stats["format"] != "pcap" || stats["packets"] != 4 || stats["encrypted"] != 3 || stats["cleartext"] != 1 || stats["key_exchange"] != 0 || stats["unexamined"] != 0 {
		t.Fatalf("stats = %v", stats)
	}
	// Every packet is unmarked (DF, RFC 4594's Standard), and its pair's
	// table says the same; the records' times are all the epoch.
	if d := stats["dscp"].([]any); len(d) != 1 || d[0].(map[string]any)["name"] != "DF" || d[0].(map[string]any)["class"] != "Standard" || d[0].(map[string]any)["packets"] != 4 {
		t.Fatalf("dscp = %v", stats["dscp"])
	}
	if stats["first"] != "1970-01-01T00:00:00Z" || stats["duration_seconds"] != 0.0 {
		t.Fatalf("times: %v %v", stats["first"], stats["duration_seconds"])
	}
	pairs := stats["pairs"].([]any)
	if d := pairs[0].(map[string]any)["dscp"].([]any); len(d) != 1 {
		t.Fatalf("pair dscp = %v", d)
	}
	top := pairs[0].(map[string]any)
	if len(pairs) != 2 || top["a"] != "172.16.1.1" || top["cleartext"] != 1 || top["encrypted"] != 2 {
		t.Fatalf("pairs = %v", pairs)
	}

	// A frame carrying no IP (an 802.3 length, as CDP's) is unexamined, never
	// cleartext.
	cdp := append(make([]byte, 12), 0x00, 0x40)
	if err := os.WriteFile(filepath.Join(dir, "captures", "cdp.pcap"), capture(frame(a, b, 50, esp), cdp), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, rc, err := run(t, dir, map[string]any{"src": "captures/cdp.pcap"}); err != nil {
		t.Fatal(err)
	} else if s := rc.(interface{ Facts() map[string]any }).Facts(); s["unexamined"] != 1 || s["cleartext"] != 0 {
		t.Errorf("a non-IP frame: %v", s)
	}
	if _, rc, err := run(t, dir, map[string]any{"src": "captures/wan.pcap", "pairs": 1}); err != nil || len(rc.(interface{ Facts() map[string]any }).Facts()["pairs"].([]any)) != 1 {
		t.Fatalf("pairs 1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "captures", "page.html"), []byte("<html>this is no capture at all, padded</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, dir, map[string]any{"src": "captures/page.html"}); err == nil || !strings.Contains(err.Error(), "not a pcap") {
		t.Fatalf("a page: %v", err)
	}
	if _, _, err := run(t, dir, map[string]any{"src": "captures/wan.pcap", "pairs": -1}); err == nil {
		t.Fatal("a negative pairs was taken")
	}
	d, _ := collection.Lookup("net.pcap.summary")
	if _, err := d.Invoke(context.Background(), engine.NewRunbookContext(nil), nil, map[string]any{"src": "captures/wan.pcap"}); err == nil || !strings.Contains(err.Error(), "no project directory") {
		t.Fatalf("a run with no project: %v", err)
	}
}
