// A real controller's link state and packet capture: Cisco's DevNet CML
// sandbox, as client_live_test.go reaches it. Two IOL XE nodes, linked and
// booted, carry their own keepalive and CDP frames, which a capture takes and
// the client fetches as a pcap; the link is stopped and started between.
package cml_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
)

// TestClient_LinkStateAndCaptureOnARealController is the measurement and
// the proof of the capture and link-state calls.
func TestClient_LinkStateAndCaptureOnARealController(t *testing.T) {
	base, user, password, fingerprint := sandbox(t)
	ctx := context.Background()
	c, err := cml.New(base, pinned(t, fingerprint), cml.Credentials{Username: user, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	lab, err := c.CreateLab(ctx, fmt.Sprintf("pleiades-capture-%d", time.Now().Unix()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Whatever failed, the lab does not outlive the test.
		_ = c.StopLab(context.Background(), lab.ID)
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		_ = c.WaitStopped(stopCtx, lab.ID, nil, 5*time.Second)
		_ = c.WipeLab(context.Background(), lab.ID)
		_ = c.DeleteLab(context.Background(), lab.ID)
	})
	var ports, nodes []string
	for _, label := range []string{"C1", "C2"} {
		// IOL's Ethernet ports start shut down and send nothing (measured
		// 2026-10-25: a capture took no packet), so the day-0
		// configuration brings the linked port up and addresses it.
		day0 := fmt.Sprintf("hostname %s\ninterface Ethernet0/0\n ip address 10.99.0.%d 255.255.255.0\n no shutdown\nend\n", label, len(nodes)+1)
		id, err := c.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: label, Definition: "iol-xe", X: 200 * len(nodes), Configuration: day0})
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, id)
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
	if err := c.StartLab(ctx, lab.ID); err != nil {
		t.Fatal(err)
	}
	bootCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := c.WaitBooted(bootCtx, lab.ID, nodes, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	t.Logf("a started lab's link reads %q", stateOf(t, c, lab.ID, link))

	if err := c.StartCapture(ctx, lab.ID, link, cml.CaptureSpec{Packets: 20, Seconds: 45}); err != nil {
		t.Fatal(err)
	}
	status, err := c.CaptureStatus(ctx, lab.ID, link)
	if err != nil || !status.Running || status.Spec.Packets != 20 {
		t.Fatalf("a running capture's status = %+v, %v", status, err)
	}
	time.Sleep(30 * time.Second)
	if err := c.StopCapture(ctx, lab.ID, link); err != nil {
		t.Fatal(err)
	}
	if status, err = c.CaptureStatus(ctx, lab.ID, link); err != nil || status.Running {
		t.Fatalf("a stopped capture's status = %+v, %v", status, err)
	}
	key, err := c.CaptureKey(ctx, lab.ID, link)
	if err != nil {
		t.Fatal(err)
	}
	data, err := c.FetchPCAP(ctx, key, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) <= 24 {
		t.Fatalf("the capture holds %d bytes: no frame was captured in 30 s on a link between two booted routers", len(data))
	}
	t.Logf("the capture is %d bytes, magic % x", len(data), data[:4])

	if err := c.StopLink(ctx, lab.ID, link); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, c, lab.ID, link); got != cml.StateStopped {
		t.Errorf("a stopped link reads %q", got)
	}
	if err := c.StartLink(ctx, lab.ID, link); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, c, lab.ID, link); got != cml.StateStarted {
		t.Errorf("a started link reads %q", got)
	}
}
