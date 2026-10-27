// Tests of a link's state and its packet captures, against cmltest's
// controller: a link stopped and started, a capture started with a bound,
// stopped and fetched, and every refusal the client makes before a call.
package cml_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
)

// linkedLab is a lab with two nodes joined by one link, returning the lab's
// id and the link's.
func linkedLab(t *testing.T, c *cml.Client) (string, string) {
	t.Helper()
	ctx := context.Background()
	lab, err := c.CreateLab(ctx, "cap")
	if err != nil {
		t.Fatal(err)
	}
	var ports []string
	for _, label := range []string{"R1", "R2"} {
		id, err := c.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: label, Definition: "iol-xe"})
		if err != nil {
			t.Fatal(err)
		}
		ifaces, err := c.NodeInterfaces(ctx, lab.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		ports = append(ports, physicalPort(t, ifaces))
	}
	link, err := c.CreateLink(ctx, lab.ID, ports[0], ports[1])
	if err != nil {
		t.Fatal(err)
	}
	return lab.ID, link
}

// physicalPort is the first interface of ifaces with a slot: a loopback,
// which a real controller lists first, has none and takes no link.
func physicalPort(t *testing.T, ifaces []cml.Interface) string {
	t.Helper()
	for _, i := range ifaces {
		if i.Slot != nil {
			return i.ID
		}
	}
	t.Fatalf("no physical interface among %+v", ifaces)
	return ""
}

// stateOf is link's state as Links reads it.
func stateOf(t *testing.T, c *cml.Client, lab, link string) string {
	t.Helper()
	links, err := c.Links(context.Background(), lab)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range links {
		if l.ID == link {
			return l.State
		}
	}
	t.Fatalf("no link %s", link)
	return ""
}

// TestLinks_StopAndStart: a link stops and starts, read back each time.
func TestLinks_StopAndStart(t *testing.T) {
	ctl := cmltest.New(t, "admin", "pw")
	c := connected(t, ctl)
	lab, link := linkedLab(t, c)
	if got := stateOf(t, c, lab, link); got != cml.StateStarted {
		t.Fatalf("a new link reads %q", got)
	}
	if err := c.StopLink(context.Background(), lab, link); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, c, lab, link); got != cml.StateStopped {
		t.Fatalf("a stopped link reads %q", got)
	}
	if err := c.StartLink(context.Background(), lab, link); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, c, lab, link); got != cml.StateStarted {
		t.Fatalf("a started link reads %q", got)
	}
}

// TestCaptures_StartStopFetch: a capture starts with its bound and filter,
// stops, and its file comes back; a second start while one runs is the
// controller's refusal.
func TestCaptures_StartStopFetch(t *testing.T) {
	ctl := cmltest.New(t, "admin", "pw")
	c := connected(t, ctl)
	ctx := context.Background()
	lab, link := linkedLab(t, c)
	if err := c.StartCapture(ctx, lab, link, cml.CaptureSpec{Packets: 500, Filter: "esp or udp port 500"}); err != nil {
		t.Fatal(err)
	}
	if err := c.StartCapture(ctx, lab, link, cml.CaptureSpec{Seconds: 5}); err == nil {
		t.Error("a second capture on a link already capturing was started")
	}
	if status, err := c.CaptureStatus(ctx, lab, link); err != nil || !status.Running || status.Spec.Packets != 500 || status.Spec.Filter != "esp or udp port 500" {
		t.Fatalf("status = %+v, %v", status, err)
	}
	ctl.Mu.Lock()
	captured := ctl.Labs[lab].Links[link].Capture
	ctl.Mu.Unlock()
	if captured.MaxPackets != 500 || captured.Filter != "esp or udp port 500" {
		t.Fatalf("the controller was asked for %+v", captured)
	}
	if err := c.StopCapture(ctx, lab, link); err != nil {
		t.Fatal(err)
	}
	if status, err := c.CaptureStatus(ctx, lab, link); err != nil || status.Running {
		t.Fatalf("a stopped capture's status = %+v, %v", status, err)
	}
	key, err := c.CaptureKey(ctx, lab, link)
	if err != nil {
		t.Fatal(err)
	}
	data, err := c.FetchPCAP(ctx, key, 1<<20)
	if err != nil || len(data) != 24 {
		t.Fatalf("FetchPCAP = %d bytes, %v", len(data), err)
	}

	ctl.Mu.Lock()
	ctl.PCAP = make([]byte, 4096)
	copy(ctl.PCAP, []byte{0x0a, 0x0d, 0x0d, 0x0a})
	ctl.Mu.Unlock()
	if _, err := c.FetchPCAP(ctx, key, 1024); err == nil || !strings.Contains(err.Error(), "over 1024 bytes") {
		t.Fatalf("a capture over its bound: %v", err)
	}
	ctl.Mu.Lock()
	ctl.PCAP = []byte("<html>not a capture</html>")
	ctl.Mu.Unlock()
	if _, err := c.FetchPCAP(ctx, key, 1<<20); err == nil || !strings.Contains(err.Error(), "not a pcap") {
		t.Fatalf("an answer that is no capture: %v", err)
	}
	if _, err := c.FetchPCAP(ctx, "nokey", 1<<20); err == nil {
		t.Error("an unknown key fetched a file")
	}
}

// TestCaptures_RefusedBeforeTheCall: a capture with no bound, out of the
// API's bounds, or a filter of two lines is refused without a call.
func TestCaptures_RefusedBeforeTheCall(t *testing.T) {
	ctl := cmltest.New(t, "admin", "pw")
	c := connected(t, ctl)
	lab, link := linkedLab(t, c)
	before := len(ctl.Calls)
	for name, spec := range map[string]cml.CaptureSpec{
		"no bound":          {},
		"too many packets":  {Packets: cml.MaxCapturePackets + 1},
		"too long":          {Seconds: cml.MaxCaptureSeconds + 1},
		"a negative count":  {Packets: -1},
		"a long filter":     {Packets: 1, Filter: strings.Repeat("x", cml.MaxBPFilter+1)},
		"a two-line filter": {Packets: 1, Filter: "esp\nicmp"},
	} {
		if err := c.StartCapture(context.Background(), lab, link, spec); err == nil {
			t.Errorf("%s: started", name)
		}
	}
	if len(ctl.Calls) != before {
		t.Errorf("a refused capture reached the controller: %v", ctl.Calls[before:])
	}
}
