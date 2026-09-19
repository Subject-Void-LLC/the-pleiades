// Package ios: opening a real session, over a real SSH connection, and
// reading a real reply back through it.
//
// Everything else in this package runs through the openSession seam,
// which is what proves each method's own decisions. Nothing proved the
// seam's own implementation: the connection, the pty-req, the shell
// request, netcli.IOS's first prompt and the paging command it sends, the
// line, its echo, the reply, and closing both the session and the
// connection under it. Those ran only against real hardware in a release
// gate, so a change to any of them broke nothing that runs here.
//
// The far end is remoteexectest's scripted device: a prompt and a reply
// per line, which is what an IOS CLI can be stood in for by locally.
// What IOS itself prints, and how netcli reads it, are covered against
// bytes captured from real hardware in pkg/netcli, and end to end by
// cmd/pleiades's own release gate against a real device.
package ios

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// sshDevice is a device reachable over SSH at a real address, as the
// methods' own sdk.Connect requires.
type sshDevice struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *sshDevice) SSHHost() string { return d.host }
func (d *sshDevice) SSHPort() int    { return d.port }

// secretContext carries the credential the session authenticates with,
// which the stub context the rest of this package uses does not.
type secretContext struct {
	*stubContext
	secrets map[string]string
}

func (c *secretContext) InjectSecrets() map[string]string { return c.secrets }

// iosDevice starts a scripted device answering an IOS-shaped prompt, and
// returns it with a device and context pointed at it.
func iosDevice(t *testing.T, replies map[string]string) (*remoteexectest.Server, *sshDevice, *secretContext) {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{Device: &remoteexectest.Device{
		Prompt:  "r1#",
		Banner:  "a scripted device, not IOS",
		Replies: replies,
		Unknown: "% Invalid input detected at '^' marker.",
	}})
	if err != nil {
		t.Fatalf("starting the scripted device: %v", err)
	}
	t.Cleanup(srv.Close)
	device := &sshDevice{
		Stub: &inventorytest.Stub{StubName: "r1", Caps: []capability.Name{capability.NameSSHTransport}},
		host: srv.Host,
		port: srv.Port,
	}
	return srv, device, &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}
}

// sessionParams are the parameters every test here passes: the harness
// presents a generated host key no known_hosts file names.
func sessionParams(extra map[string]any) map[string]any {
	params := map[string]any{"insecure_skip_host_key_verify": true}
	for k, v := range extra {
		params[k] = v
	}
	return params
}

// TestOpenSession_ReachesADeviceAndDisablesPaging covers what opening a
// real session does before any method's own work: it connects, asks for a
// terminal, starts a shell, waits for the device's first prompt, and
// sends netcli.IOS's paging command. The last is the one a check of the
// session's own doc comment cannot make: a session that skipped it would
// hang on the first long reply, which is the failure this package's own
// runCommand comment records from a real device.
func TestOpenSession_ReachesADeviceAndDisablesPaging(t *testing.T) {
	srv, device, rc := iosDevice(t, map[string]string{"write memory": "Building configuration...\n[OK]"})

	result, err := Save(context.Background(), rc, device, sessionParams(nil))
	if err != nil {
		t.Fatalf("save over a real session: %v", err)
	}
	if !result.Changed {
		t.Error("a save the device took reported no change")
	}
	if got := srv.Commands(); len(got) != 2 || got[0] != "terminal length 0" || got[1] != "write memory" {
		t.Errorf("the device received %v, want paging disabled first and then the save", got)
	}
	if out, _ := rc.stats[statSaveStdout].(string); !strings.Contains(out, "[OK]") {
		t.Errorf("the recorded reply is %q, want the device's own", out)
	}
}

// TestOpenSession_ReadsARealReplyBack proves the session returns what the
// device printed and nothing else: net.ios.facts parses these replies, so
// an echoed command or a trailing prompt left in the output would be read
// as part of a fact.
func TestOpenSession_ReadsARealReplyBack(t *testing.T) {
	_, device, rc := iosDevice(t, map[string]string{
		"show version":   "Cisco IOS XE Software, Version 17.12.1\nProcessor board ID FCW2140L0GF",
		"show inventory": `NAME: "Chassis", DESCR: "test"` + "\nPID: C9300-24T, VID: V02, SN: FCW2140L0GF",
	})

	if _, err := Facts(context.Background(), rc, device, sessionParams(map[string]any{"gather_subset": []any{"min"}})); err != nil {
		t.Fatalf("facts over a real session: %v", err)
	}
	if got := rc.facts[factVersion]; got != "17.12.1" {
		t.Errorf("version fact = %v, want it parsed from the device's own reply", got)
	}
	if got := rc.facts[factSerialNum]; got != "FCW2140L0GF" {
		t.Errorf("serial fact = %v, want the chassis serial", got)
	}
}

// TestOpenSession_FailsWhenTheDeviceRefusesTheCredential covers the first
// thing that can go wrong: the connection itself. The failure names the
// method, and nothing is reported as saved.
func TestOpenSession_FailsWhenTheDeviceRefusesTheCredential(t *testing.T) {
	_, device, rc := iosDevice(t, nil)
	rc.secrets = map[string]string{"username": "someone", "password": "wrong"}

	result, err := Save(context.Background(), rc, device, sessionParams(nil))
	if err == nil || !strings.Contains(err.Error(), "net.ios.save") {
		t.Errorf("err = %v, want a failure named for the method", err)
	}
	if result.Changed {
		t.Error("a save that never authenticated reported a change")
	}
}

// TestOpenSession_FailsWhenNoPromptEverArrives covers a device that
// answers SSH and then says nothing an IOS session can synchronize on: a
// console server, or a device whose prompt is not IOS-shaped. The session
// gives up on the caller's deadline instead of waiting forever, which is
// what makes the timeout the runbook asked for the real bound.
func TestOpenSession_FailsWhenNoPromptEverArrives(t *testing.T) {
	srv, err := remoteexectest.Start(remoteexectest.Options{Device: &remoteexectest.Device{Prompt: "login: "}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	device := &sshDevice{
		Stub: &inventorytest.Stub{StubName: "r1", Caps: []capability.Name{capability.NameSSHTransport}},
		host: srv.Host, port: srv.Port,
	}
	rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := Save(ctx, rc, device, sessionParams(nil)); err == nil {
		t.Fatal("a device that never printed an IOS prompt was treated as ready")
	}
	if waited := time.Since(start); waited > 10*time.Second {
		t.Errorf("the session waited %s, past the deadline it was given", waited)
	}
}
