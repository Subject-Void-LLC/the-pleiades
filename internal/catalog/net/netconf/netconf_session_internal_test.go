// Package netconf: opening a real NETCONF session, over a real SSH
// connection and a real subsystem channel.
//
// The rest of this package runs through the openSession seam, which is
// what proves the method's own decisions about the datastore, the lock
// and the commit. Nothing proved the seam itself: connecting on the
// device's own NETCONF port, asking for the subsystem, the two hellos,
// the framing they negotiate, and closing the subsystem and the
// connection under it in that order. That ran only against real hardware
// in a release gate.
//
// The far end is remoteexectest's scripted NETCONF server. RFC 6241
// itself is pkg/netconf's to cover, against captured bytes and a real
// server in its own container tests.
package netconf

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// netconfDevice is a device that exposes a NETCONF port, which is what
// capability.NetconfCapable means and what the session needs to dial.
type netconfDevice struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *netconfDevice) SSHHost() string   { return d.host }
func (d *netconfDevice) SSHPort() int      { return d.port }
func (d *netconfDevice) NetconfPort() int  { return d.port }
func (d *netconfDevice) CLIPrompt() string { return "" }

// secretContext carries the credential the session authenticates with.
type secretContext struct {
	*stubContext
	secrets map[string]string
}

func (c *secretContext) InjectSecrets() map[string]string { return c.secrets }

// scriptedServer starts a NETCONF server advertising capabilities, and
// returns it with a device and context pointed at it.
func scriptedServer(t *testing.T, capabilities []string, replies map[string]string) (*remoteexectest.Server, *netconfDevice, *secretContext) {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{Netconf: &remoteexectest.NetconfDevice{
		Capabilities: capabilities,
		Replies:      replies,
	}})
	if err != nil {
		t.Fatalf("starting the scripted NETCONF server: %v", err)
	}
	t.Cleanup(srv.Close)
	device := &netconfDevice{
		Stub: &inventorytest.Stub{StubName: "r1", Caps: []capability.Name{capability.NameNetconf}},
		host: srv.Host,
		port: srv.Port,
	}
	return srv, device, &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}
}

// sessionParams are what every test here passes: the harness presents a
// generated host key no known_hosts file names.
func sessionParams(extra map[string]any) map[string]any {
	params := map[string]any{"insecure_skip_host_key_verify": true, "content": "<config/>"}
	for k, v := range extra {
		params[k] = v
	}
	return params
}

// TestOpenSession_ConfiguresADeviceOverARealSubsystem covers the whole
// path a real run takes: the connection on the device's NETCONF port, the
// subsystem channel, the hellos, and the edit-config the task asked for,
// followed by the commit the candidate datastore needs.
func TestOpenSession_ConfiguresADeviceOverARealSubsystem(t *testing.T) {
	srv, device, rc := scriptedServer(t, []string{netconf.CapabilityCandidate}, nil)

	result, err := Config(context.Background(), rc, device, sessionParams(map[string]any{"target": "candidate", "lock": "if_supported"}))
	if err != nil {
		t.Fatalf("config over a real session: %v", err)
	}
	if !result.Changed {
		t.Error("a configuration the device accepted reported no change")
	}
	got := strings.Join(srv.Commands(), ",")
	if !strings.Contains(got, "edit-config") || !strings.Contains(got, "commit") {
		t.Errorf("the device received %v, want the edit and the commit", srv.Commands())
	}
	// With lock: if_supported against a device advertising the candidate
	// datastore, a real run takes the lock and releases it on the way out.
	if !strings.Contains(got, "lock") || !strings.Contains(got, "unlock") {
		t.Errorf("the device received %v, want the datastore locked and unlocked", srv.Commands())
	}
}

// TestOpenSession_CapturesABackupThroughTheSession covers the read half
// against a real session: the running configuration the device returns
// arrives as the backup stat, which is what an operator restores from by
// hand.
func TestOpenSession_CapturesABackupThroughTheSession(t *testing.T) {
	const running = `<data><hostname>r1</hostname></data>`
	// Editing the running datastore directly is what writable-running
	// advertises; without it the session refuses before any backup.
	_, device, rc := scriptedServer(t, []string{netconf.CapabilityWritableRunning}, map[string]string{"get-config": running})

	if _, err := Config(context.Background(), rc, device, sessionParams(map[string]any{"backup": true})); err != nil {
		t.Fatalf("config with a backup: %v", err)
	}
	backup, _ := rc.stats[statBackup].(string)
	if !strings.Contains(backup, "<hostname>r1</hostname>") {
		t.Errorf("backup = %q, want the configuration the device returned", backup)
	}
}

// TestOpenSession_RefusesADatastoreTheDeviceDoesNotHave covers the guard
// that depends on the real hello: a device advertising no candidate
// datastore is not asked to configure one, so the task fails before an
// edit reaches it rather than after.
func TestOpenSession_RefusesADatastoreTheDeviceDoesNotHave(t *testing.T) {
	srv, device, rc := scriptedServer(t, nil, nil) // base:1.0 only, so running alone

	_, err := Config(context.Background(), rc, device, sessionParams(map[string]any{"target": "candidate"}))
	if err == nil || !strings.Contains(err.Error(), "candidate") {
		t.Fatalf("err = %v, want the missing datastore named", err)
	}
	for _, sent := range srv.Commands() {
		if sent == "edit-config" {
			t.Error("the device was configured despite not offering the datastore asked for")
		}
	}
}

// TestOpenSession_FailsWhenTheDeviceOffersNoNetconfSubsystem covers a
// device answering SSH with the feature switched off: the subsystem
// request is declined, and the task fails naming the method rather than
// waiting for a hello that will never come.
func TestOpenSession_FailsWhenTheDeviceOffersNoNetconfSubsystem(t *testing.T) {
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	device := &netconfDevice{
		Stub: &inventorytest.Stub{StubName: "r1", Caps: []capability.Name{capability.NameNetconf}},
		host: srv.Host, port: srv.Port,
	}
	rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Config(ctx, rc, device, sessionParams(nil)); err == nil || !strings.Contains(err.Error(), "net.netconf.config") {
		t.Errorf("err = %v, want a failure named for the method", err)
	}
}
