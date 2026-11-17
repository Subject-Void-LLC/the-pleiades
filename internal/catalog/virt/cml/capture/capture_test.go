// Tests of virt.cml.capture.start and virt.cml.capture.stop against
// cmltest's controller and a project directory: a capture started with its
// bound and an undo that stops it, a second start refused while it runs, a
// stop that writes the pcap into the project, a large one whole after the
// controller reset its stream, one over the bound refused before its body
// is read, a check of each that changes nothing, and a stop with nothing to
// write refused.
package capture_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/virt/cml/capture"
)

// A stop's wait reads the file's length every millisecond here, not every
// five seconds.
func init() { capture.SetAwaitPoll(time.Millisecond) }

// projectContext is cmltest's context with a project directory to write
// into, as the command line's has.
type projectContext struct {
	*cmltest.Context
	dir string
}

// WriteProjectFile implements sdk.ProjectFileWriter over dir, without the
// command line's guards, which internal/engine's own tests hold.
func (p *projectContext) WriteProjectFile(name string, data []byte, perm os.FileMode, write bool) (bool, error) {
	path := filepath.Join(p.dir, filepath.FromSlash(name))
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return false, nil
	}
	if !write {
		return true, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, data, perm)
}

// run calls fqcn on ctl (its check with check set) with params, in a
// project at dir.
func run(t *testing.T, ctl *cmltest.Controller, dir, fqcn string, check bool, params map[string]any) (collection.Result, *projectContext, error) {
	t.Helper()
	return runIn(context.Background(), t, ctl, dir, fqcn, check, params)
}

// runIn is run under ctx.
func runIn(ctx context.Context, t *testing.T, ctl *cmltest.Controller, dir, fqcn string, check bool, params map[string]any) (collection.Result, *projectContext, error) {
	t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	call := d.Invoke
	if check {
		call = d.Check
	}
	rc := &projectContext{Context: cmltest.NewContext(ctl), dir: dir}
	res, err := call(ctx, rc, cmltest.NewDevice(t, ctl), params)
	return res, rc, err
}

// linked starts a controller holding lab cvd with R1 and R2 linked by their
// first physical ports, returning it and the link's id.
func linked(t *testing.T) (*cmltest.Controller, string) {
	t.Helper()
	return linkedOn(t, cmltest.New(t, "admin", "pw"))
}

// linkedOn is linked on the controller ctl.
func linkedOn(t *testing.T, ctl *cmltest.Controller) (*cmltest.Controller, string) {
	t.Helper()
	client, err := cml.New(ctl.URL, ctl.Client(), cml.Credentials{Username: "admin", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	lab, err := client.CreateLab(ctx, "cvd")
	if err != nil {
		t.Fatal(err)
	}
	var ports []string
	for _, label := range []string{"R1", "R2"} {
		id, err := client.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: label, Definition: "iol-xe"})
		if err != nil {
			t.Fatal(err)
		}
		ifaces, err := client.NodeInterfaces(ctx, lab.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range ifaces {
			if i.Slot != nil {
				ports = append(ports, i.ID)
				break
			}
		}
	}
	link, err := client.CreateLink(ctx, lab.ID, ports[0], ports[1])
	if err != nil {
		t.Fatal(err)
	}
	return ctl, link
}

// TestCapture_StartStopAndWrite is the round trip.
func TestCapture_StartStopAndWrite(t *testing.T) {
	ctl, link := linked(t)
	dir := t.TempDir()
	start := map[string]any{"lab": "cvd", "link": link, "packets": 100, "seconds": 30, "filter": "esp or icmp"}

	if res, _, err := run(t, ctl, dir, "virt.cml.capture.start", true, start); err != nil || !res.Changed {
		t.Fatalf("check start: %+v, %v", res, err)
	}
	res, rc, err := run(t, ctl, dir, "virt.cml.capture.start", false, start)
	if err != nil || !res.Changed {
		t.Fatalf("start: %+v, %v", res, err)
	}
	if inv, ok := rc.Inverse(); !ok || inv[sdk.InverseFQCNKey] != "virt.cml.capture.stop" || inv["params"].(map[string]any)["link"] != link {
		t.Fatalf("start's undo = %v", inv)
	}
	if _, _, err := run(t, ctl, dir, "virt.cml.capture.start", false, start); err == nil || !strings.Contains(err.Error(), "already capturing") {
		t.Fatalf("a second start: %v", err)
	}

	ctl.Mu.Lock()
	// A 64-byte pcap file of two 4-byte records.
	ctl.PCAP = pcapFile(2, 4)
	ctl.Mu.Unlock()
	stop := map[string]any{"lab": "cvd", "link": link, "dest": "captures/r1-r2.pcap"}
	if res, _, err := run(t, ctl, dir, "virt.cml.capture.stop", true, stop); err != nil || !res.Changed {
		t.Fatalf("check stop: %+v, %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "captures", "r1-r2.pcap")); !os.IsNotExist(err) {
		t.Fatal("a check wrote the file")
	}
	res, rc, err = run(t, ctl, dir, "virt.cml.capture.stop", false, stop)
	if err != nil || !res.Changed || rc.Stats["bytes"] != 64 || len(rc.Stats["sha256"].(string)) != 64 {
		t.Fatalf("stop: %+v, %v, stats %v", res, err, rc.Stats)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "captures", "r1-r2.pcap")); err != nil || len(data) != 64 { // #nosec G304 -- this test's own directory
		t.Fatalf("the file: %d bytes, %v", len(data), err)
	}
	if res, _, err := run(t, ctl, dir, "virt.cml.capture.stop", false, stop); err != nil || res.Changed {
		t.Fatalf("a second stop writing the same file: %+v, %v", res, err)
	}
}

// TestCapture_Refusals: no bound, a filter too long, a link not started, and
// a stop with nothing captured to write are each refused.
func TestCapture_Refusals(t *testing.T) {
	ctl, link := linked(t)
	dir := t.TempDir()
	for name, params := range map[string]map[string]any{
		"no bound":      {"lab": "cvd", "link": link},
		"a long filter": {"lab": "cvd", "link": link, "packets": 5, "filter": strings.Repeat("x", 200)},
	} {
		if _, _, err := run(t, ctl, dir, "virt.cml.capture.start", false, params); err == nil {
			t.Errorf("%s: started", name)
		}
	}
	for _, dest := range []string{"runbooks/site.yaml", "captures/x.pcap.txt"} {
		if _, _, err := run(t, ctl, dir, "virt.cml.capture.stop", false, map[string]any{"lab": "cvd", "link": link, "dest": dest}); err == nil || !strings.Contains(err.Error(), "must name a .pcap or .pcapng file") {
			t.Errorf("dest %s: %v", dest, err)
		}
	}
	if _, _, err := run(t, ctl, dir, "virt.cml.capture.stop", false, map[string]any{"lab": "cvd", "link": link, "dest": "x.pcap"}); err == nil || !strings.Contains(err.Error(), "no capture to write") {
		t.Errorf("a stop with nothing captured: %v", err)
	}
	ctl.Mu.Lock()
	for _, l := range ctl.Labs {
		for _, k := range l.Links {
			k.State = "STOPPED"
		}
	}
	ctl.Mu.Unlock()
	if _, _, err := run(t, ctl, dir, "virt.cml.capture.start", false, map[string]any{"lab": "cvd", "link": link, "packets": 5}); err == nil || !strings.Contains(err.Error(), "started link") {
		t.Errorf("a stopped link: %v", err)
	}
}

// pcapFile is a little-endian pcap file of n records, each a 16-byte
// header holding its length and then size bytes.
func pcapFile(n, size int) []byte {
	b := bytes.NewBuffer([]byte{0xd4, 0xc3, 0xb2, 0xa1, 2, 0, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0, 0, 1, 0, 0, 0})
	for i := range n {
		hdr := make([]byte, 16)
		binary.LittleEndian.PutUint32(hdr[8:], uint32(size))
		binary.LittleEndian.PutUint32(hdr[12:], uint32(size))
		b.Write(hdr)
		b.Write(bytes.Repeat([]byte{byte(i)}, size))
	}
	return b.Bytes()
}

// TestCapture_ALargeCaptureIsWrittenWholeAfterAReset: a 12 MiB capture
// (about what 22,419 packets came to on a real controller) whose download
// the controller resets a quarter of the way in, over HTTP/2 as a real
// one serves it, is fetched again and written whole.
func TestCapture_ALargeCaptureIsWrittenWholeAfterAReset(t *testing.T) {
	ctl, link := linkedOn(t, cmltest.NewHTTP2(t, "admin", "pw"))
	dir := t.TempDir()
	if _, _, err := run(t, ctl, dir, "virt.cml.capture.start", false, map[string]any{"lab": "cvd", "link": link, "packets": 25000}); err != nil {
		t.Fatal(err)
	}
	want := pcapFile(12<<20/1016, 1000)
	ctl.Mu.Lock()
	// The count the capture reports before the stop, which the file holds.
	for _, l := range ctl.Labs {
		l.Links[link].Capture.Packets = 12 << 20 / 1016
	}
	ctl.PCAPAt = func(n int) []byte {
		if n == 3 {
			// The fetch, after two reads of the length (GETs, since HEAD
			// is refused and no range is served), is cut.
			ctl.PCAPCuts = 1
		}
		return want
	}
	ctl.Mu.Unlock()
	res, rc, err := run(t, ctl, dir, "virt.cml.capture.stop", false, map[string]any{"lab": "cvd", "link": link, "dest": "captures/big.pcap"})
	if err != nil || !res.Changed || rc.Stats["bytes"] != len(want) {
		t.Fatalf("stop: %+v, %v, stats %v", res, err, rc.Stats)
	}
	got, err := os.ReadFile(filepath.Join(dir, "captures", "big.pcap")) // #nosec G304 -- this test's own directory
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("the file: %d bytes, %v", len(got), err)
	}
	ctl.Mu.Lock()
	defer ctl.Mu.Unlock()
	// HEAD refused, two reads of the length, a fetch cut, and the fetch
	// again whole.
	wantReqs := "HEAD HTTP/2.0 |GET HTTP/2.0 bytes=0-0|GET HTTP/2.0 |GET HTTP/2.0 |GET HTTP/2.0 "
	if got := strings.Join(ctl.PCAPRequests, "|"); got != wantReqs {
		t.Fatalf("pcap requests %q, not %q", got, wantReqs)
	}
}

// TestCapture_StopWaitsForEveryPacketItTook: a stop waits, up to its
// timeout, for the file to hold every packet the capture had taken when it
// was stopped, as a real controller writes the file behind the capture; a
// file still short of them when the wait ends (here at a known request) is
// refused with how far it got, nothing written; the timeout ends a wait
// that never would; and a timeout out of range is refused before any call.
func TestCapture_StopWaitsForEveryPacketItTook(t *testing.T) {
	ctl, link := linked(t)
	dir := t.TempDir()
	if _, _, err := run(t, ctl, dir, "virt.cml.capture.start", false, map[string]any{"lab": "cvd", "link": link, "packets": 25000}); err != nil {
		t.Fatal(err)
	}
	ctl.Mu.Lock()
	// Two of the three packets taken.
	ctl.PCAP = pcapFile(2, 4)
	for _, l := range ctl.Labs {
		l.Links[link].Capture.Packets = 3
	}
	ctl.Mu.Unlock()
	stop := map[string]any{"lab": "cvd", "link": link, "dest": "captures/short.pcap"}
	for _, bad := range []any{0, 86401, "soon"} {
		stop["timeout"] = bad
		if _, _, err := run(t, ctl, dir, "virt.cml.capture.stop", false, stop); err == nil || !strings.Contains(err.Error(), "timeout") {
			t.Errorf("timeout %v: %v", bad, err)
		}
	}
	// Ended at the read of the length after the fetch (the fourth GET):
	// whether that read is answered or not, the wait ends at that poll.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctl.Mu.Lock()
	ctl.PCAPAt = func(n int) []byte {
		if n == 4 {
			cancel()
		}
		return pcapFile(2, 4)
	}
	ctl.Mu.Unlock()
	delete(stop, "timeout")
	_, _, err := runIn(ctx, t, ctl, dir, "virt.cml.capture.stop", false, stop)
	if err == nil || !strings.Contains(err.Error(), "held 2 packets of the 3 the capture took") || !strings.HasSuffix(err.Error(), "context canceled") {
		t.Fatalf("stop: %v", err)
	}
	// The capture is stopped now, so its count is not known: a second stop
	// waits on the length alone, which holds still, but the timeout of a
	// second still ends it when the file never ends a record.
	ctl.Mu.Lock()
	ctl.PCAPAt = func(int) []byte { return pcapFile(2, 4)[:50] }
	ctl.Mu.Unlock()
	stop["timeout"] = 1
	if _, _, err := run(t, ctl, dir, "virt.cml.capture.stop", false, stop); err == nil || !strings.Contains(err.Error(), "had not finished writing the capture file") || !strings.HasSuffix(err.Error(), "context deadline exceeded") {
		t.Fatalf("stop with timeout 1: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "captures", "short.pcap")); !os.IsNotExist(err) {
		t.Fatalf("a file short of its packets was written: %v", err)
	}
}

// TestCapture_TheTimeoutBoundsACheck: a check's one fetch is bounded by the
// task's timeout, not only by the minute a fetch may sit idle: a controller
// that sends a file's first bytes and then nothing ends the check when the
// timeout does.
func TestCapture_TheTimeoutBoundsACheck(t *testing.T) {
	ctl, link := linkedOn(t, cmltest.NewHTTP2(t, "admin", "pw"))
	dir := t.TempDir()
	if _, _, err := run(t, ctl, dir, "virt.cml.capture.start", false, map[string]any{"lab": "cvd", "link": link, "packets": 25000}); err != nil {
		t.Fatal(err)
	}
	ctl.Mu.Lock()
	ctl.OnCall = func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.Contains(r.URL.Path, "/pcap/") {
			return false
		}
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte{0xd4, 0xc3, 0xb2, 0xa1})
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
		return true
	}
	ctl.Mu.Unlock()
	_, _, err := run(t, ctl, dir, "virt.cml.capture.stop", true, map[string]any{"lab": "cvd", "link": link, "dest": "captures/c.pcap", "timeout": 1})
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") || strings.Contains(err.Error(), "idle bound") {
		t.Fatalf("check: %v", err)
	}
}

// TestCapture_ACaptureOverTheBoundIsRefusedBeforeItsBody: a capture whose
// answer states more than a project file holds is refused with its size,
// and nothing is written.
func TestCapture_ACaptureOverTheBoundIsRefusedBeforeItsBody(t *testing.T) {
	ctl, link := linkedOn(t, cmltest.NewHTTP2(t, "admin", "pw"))
	dir := t.TempDir()
	if _, _, err := run(t, ctl, dir, "virt.cml.capture.start", false, map[string]any{"lab": "cvd", "link": link, "packets": 25000}); err != nil {
		t.Fatal(err)
	}
	ctl.Mu.Lock()
	ctl.OnCall = func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.Contains(r.URL.Path, "/pcap/") {
			return false
		}
		w.Header().Set("Content-Length", "40000000")
		_, _ = w.Write([]byte{0xd4, 0xc3, 0xb2, 0xa1})
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
		return true
	}
	ctl.Mu.Unlock()
	_, _, err := run(t, ctl, dir, "virt.cml.capture.stop", false, map[string]any{"lab": "cvd", "link": link, "dest": "captures/big.pcap"})
	if err == nil || !strings.Contains(err.Error(), "the capture is 40000000 bytes, over the 16777216") {
		t.Fatalf("stop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "captures", "big.pcap")); !os.IsNotExist(err) {
		t.Fatalf("a refused capture was written: %v", err)
	}
}
