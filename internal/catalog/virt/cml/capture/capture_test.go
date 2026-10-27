// Tests of virt.cml.capture.start and virt.cml.capture.stop against
// cmltest's controller and a project directory: a capture started with its
// bound and an undo that stops it, a second start refused while it runs, a
// stop that writes the pcap into the project, a check of each that changes
// nothing, and a stop with nothing to write refused.
package capture_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/virt/cml/capture"
)

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
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	call := d.Invoke
	if check {
		call = d.Check
	}
	rc := &projectContext{Context: cmltest.NewContext(ctl), dir: dir}
	res, err := call(context.Background(), rc, cmltest.NewDevice(t, ctl), params)
	return res, rc, err
}

// linked starts a controller holding lab cvd with R1 and R2 linked by their
// first physical ports, returning it and the link's id.
func linked(t *testing.T) (*cmltest.Controller, string) {
	t.Helper()
	ctl := cmltest.New(t, "admin", "pw")
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
	ctl.PCAP = append([]byte{0xd4, 0xc3, 0xb2, 0xa1}, make([]byte, 60)...)
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
