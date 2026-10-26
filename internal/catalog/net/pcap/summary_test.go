// Tests of net.pcap.summary through the command line's own project files: a
// capture of ESP and one cleartext ICMP counted as such, its pair listed, a
// pairs limit, a file that is no capture refused, and a run with no project
// refused.
package pcap_test

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if stats["format"] != "pcap" || stats["packets"] != 4 || stats["encrypted"] != 3 || stats["cleartext"] != 1 || stats["key_exchange"] != 0 {
		t.Fatalf("stats = %v", stats)
	}
	pairs := stats["pairs"].([]any)
	top := pairs[0].(map[string]any)
	if len(pairs) != 2 || top["a"] != "172.16.1.1" || top["cleartext"] != 1 || top["encrypted"] != 2 {
		t.Fatalf("pairs = %v", pairs)
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
