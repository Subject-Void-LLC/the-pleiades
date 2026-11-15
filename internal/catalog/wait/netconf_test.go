// Tests for wait.netconf against the real, in-process SSH server serving a
// scripted NETCONF subsystem (pkg/remoteexec/remoteexectest), with host key
// verification on against a real known_hosts file: a listener that resets
// its first connections, as a booting device's does, is waited through; one
// that never answers is given up on at the timeout; and one whose key
// known_hosts does not hold ends the wait before any credential is sent.
package wait_test

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/wait"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// netconfTarget is a device with a NETCONF listener at netconfPort, whose
// SSH port answers nothing, so a wait that succeeds can only have used the
// listener.
type netconfTarget struct {
	*inventorytest.Stub
	netconfPort int
}

func (d *netconfTarget) SSHHost() string  { return "127.0.0.1" }
func (d *netconfTarget) SSHPort() int     { return 1 }
func (d *netconfTarget) NetconfPort() int { return d.netconfPort }

// newNetconfTarget builds the device a test waits for.
func newNetconfTarget(port int) *netconfTarget {
	return &netconfTarget{Stub: &inventorytest.Stub{StubName: "r1", Caps: []capability.Name{capability.NameSSHTransport, capability.NameNetconf}}, netconfPort: port}
}

// startNetconfServer starts the in-process SSH server answering a netconf
// subsystem, which answers every RPC, close-session included, with <ok/>.
func startNetconfServer(t *testing.T) *remoteexectest.Server {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{Netconf: &remoteexectest.NetconfDevice{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// trustOnly points known_hosts at a file of its own, holding srv's key for
// port when trust is set and nothing otherwise.
func trustOnly(t *testing.T, srv *remoteexectest.Server, port int, trust bool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	var body []byte
	if trust {
		body = []byte(knownhosts.Line([]string{knownhosts.Normalize(net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))}, srv.HostKey) + "\n")
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(remoteexec.KnownHostsEnv, path)
}

// lateListener listens on a port of its own and resets its first resets
// connections at once, as a device whose NETCONF server has not started
// does, then carries every later one to srv. It returns the port.
func lateListener(t *testing.T, srv *remoteexectest.Server, resets int32) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var seen atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			if seen.Add(1) <= resets {
				// A zero linger makes Close send a reset, not a clean end.
				_ = conn.(*net.TCPConn).SetLinger(0)
				_ = conn.Close()
				continue
			}
			go carry(conn, srv.Addr())
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// carry copies conn to and from addr until either side ends.
func carry(conn net.Conn, addr string) {
	defer func() { _ = conn.Close() }()
	far, err := net.Dial("tcp", addr)
	if err != nil {
		return
	}
	defer func() { _ = far.Close() }()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(far, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, far); done <- struct{}{} }()
	<-done
}

// netconfContext is the run context carrying srv's credential.
func netconfContext(srv *remoteexectest.Server) *waitContext {
	return &waitContext{secrets: srv.Secrets(), stats: map[string]any{}}
}

func TestNetconf_Registered(t *testing.T) {
	d, ok := collection.Lookup("wait.netconf")
	if !ok || d.Manifest.Status != collection.StatusImplemented || d.Invoke == nil {
		t.Fatalf("wait.netconf: %+v", d.Manifest)
	}
	if d.Manifest.Reversibility.Reversible || d.Check != nil || d.Manifest.NoCheckReason == "" {
		t.Errorf("a wait changes nothing, has no check, and says why: %+v", d.Manifest)
	}
	for _, p := range d.Manifest.Doc.Params {
		if p.Name == sdk.ParamInsecureSkipHostKeyVerify {
			t.Error("wait.netconf declares a way to skip host key verification")
		}
	}
}

// TestNetconf_WaitsThroughResetsUntilTheListenerAnswers: a listener that
// resets its first connections is tried again until a session opens, and
// the stats say how it went.
func TestNetconf_WaitsThroughResetsUntilTheListenerAnswers(t *testing.T) {
	t.Cleanup(remoteexec.SnapshotForTest())
	srv := startNetconfServer(t)
	port := lateListener(t, srv, 3)
	trustOnly(t, srv, port, true)
	rc := netconfContext(srv)
	result, err := wait.Netconf(context.Background(), rc, newNetconfTarget(port), map[string]any{"timeout": 60, "sleep": 1, "connect_timeout": 5})
	if err != nil || result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if rc.stats["port"] != port || rc.stats["failures"].(int) < 1 || rc.stats["tries"].(int) < 2 {
		t.Errorf("stats %v", rc.stats)
	}
	// The session it opened was a real one, ended politely.
	if got := srv.Commands(); len(got) != 1 || got[0] != "close-session" {
		t.Errorf("the listener received %q, want one close-session", got)
	}
	diff, _ := rc.stats[sdk.StatDiff].(map[string]any)
	if diff[sdk.DiffBefore].(map[string]any)["port"] != port {
		t.Errorf("diff %v", rc.stats[sdk.StatDiff])
	}
}

// TestNetconf_GivesUpAtItsTimeout: a listener that never answers ends the
// wait at its timeout, quoting the last try.
func TestNetconf_GivesUpAtItsTimeout(t *testing.T) {
	srv := startNetconfServer(t)
	dead := deadPort(t)
	trustOnly(t, srv, dead, true)
	start := time.Now()
	_, err := wait.Netconf(context.Background(), netconfContext(srv), newNetconfTarget(dead), map[string]any{"timeout": 2, "sleep": 1, "connect_timeout": 1})
	if err == nil || !strings.Contains(err.Error(), "the NETCONF listener of r1 on port "+strconv.Itoa(dead)+" did not answer") || !strings.Contains(err.Error(), "timed out after 2s; the last try said") {
		t.Errorf("gave up with %v", err)
	}
	if took := time.Since(start); took > 6*time.Second {
		t.Errorf("gave up after %s, well past its 2s timeout", took)
	}
}

// TestNetconf_AnSSHServerWithNoNetconfIsNotReady: a login that succeeds on
// a server declining the netconf subsystem is a failed try, since the
// hello is what the wait waits for.
func TestNetconf_AnSSHServerWithNoNetconfIsNotReady(t *testing.T) {
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	trustOnly(t, srv, srv.Port, true)
	_, err = wait.Netconf(context.Background(), netconfContext(srv), newNetconfTarget(srv.Port), map[string]any{"timeout": 2, "sleep": 1, "connect_timeout": 1})
	if err == nil || !strings.Contains(err.Error(), "timed out") || srv.Logins() == 0 {
		t.Errorf("a server with no NETCONF: %v after %d logins", err, srv.Logins())
	}
}

// TestNetconf_AnUnknownHostKeyEndsTheWaitBeforeAnyCredential: a listener
// whose key known_hosts does not hold ends the wait on the first try,
// naming the command that trusts it, and the server saw no login and no
// password, even with insecure_skip_host_key_verify passed.
func TestNetconf_AnUnknownHostKeyEndsTheWaitBeforeAnyCredential(t *testing.T) {
	srv := startNetconfServer(t)
	trustOnly(t, srv, srv.Port, false)
	start := time.Now()
	params := map[string]any{"timeout": 60, "sleep": 1, sdk.ParamInsecureSkipHostKeyVerify: true}
	_, err := wait.Netconf(context.Background(), netconfContext(srv), newNetconfTarget(srv.Port), params)
	if err == nil || !strings.Contains(err.Error(), "stopped at try 1") || !strings.Contains(err.Error(), "pleiades trust-host r1 --first-connect --netconf") {
		t.Fatalf("an unknown host key: %v", err)
	}
	if srv.Logins() != 0 || srv.Refusals() != 0 {
		t.Errorf("a credential reached a listener whose key is unknown: %d logins, %d refusals", srv.Logins(), srv.Refusals())
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("an unknown key took %s to end the wait", took)
	}
}

// TestNetconf_ARefusedLoginEndsTheWait: a password the listener refuses is
// sent once, not at every try until the timeout.
func TestNetconf_ARefusedLoginEndsTheWait(t *testing.T) {
	srv := startNetconfServer(t)
	trustOnly(t, srv, srv.Port, true)
	rc := &waitContext{secrets: map[string]string{"username": srv.Username, "password": "wrong"}, stats: map[string]any{}}
	_, err := wait.Netconf(context.Background(), rc, newNetconfTarget(srv.Port), map[string]any{"timeout": 60, "sleep": 1})
	if err == nil || !strings.Contains(err.Error(), "stopped at try 1") || srv.Refusals() != 1 {
		t.Errorf("a refused login: %v after %d refused passwords", err, srv.Refusals())
	}
}

// TestNetconf_Refusals: bad timing and a device with no NETCONF listener
// are refused before any try.
func TestNetconf_Refusals(t *testing.T) {
	srv := startNetconfServer(t)
	if _, err := wait.Netconf(context.Background(), netconfContext(srv), newNetconfTarget(srv.Port), map[string]any{"timeout": 0}); err == nil {
		t.Error("a zero timeout was accepted")
	}
	if _, err := wait.Netconf(context.Background(), netconfContext(srv), nil, nil); err == nil {
		t.Error("waited for no device")
	}
	plain := &waitTarget{Stub: newWaitStub(), host: "127.0.0.1", port: srv.Port}
	if _, err := wait.Netconf(context.Background(), netconfContext(srv), plain, nil); err == nil || !strings.Contains(err.Error(), "has no NETCONF listener") {
		t.Errorf("a device with no listener: %v", err)
	}
}

// TestNetconf_ADeviceNamingNoPortIsTriedOn830: a device whose NetconfPort
// is zero is tried on RFC 6242's port, which the error names.
func TestNetconf_ADeviceNamingNoPortIsTriedOn830(t *testing.T) {
	srv := startNetconfServer(t)
	trustOnly(t, srv, srv.Port, true)
	_, err := wait.Netconf(context.Background(), netconfContext(srv), newNetconfTarget(0), map[string]any{"timeout": 1, "sleep": 1, "connect_timeout": 1})
	if err == nil || !strings.Contains(err.Error(), "on port 830 did not answer") {
		t.Errorf("a device naming no port: %v", err)
	}
}

// TestNetconf_AHelloItCannotReadIsNotReady: a listener whose hello does not
// parse is a failed try at every attempt, never taken as ready.
func TestNetconf_AHelloItCannotReadIsNotReady(t *testing.T) {
	srv, err := remoteexectest.Start(remoteexectest.Options{Netconf: &remoteexectest.NetconfDevice{SessionID: "</broken"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	trustOnly(t, srv, srv.Port, true)
	_, err = wait.Netconf(context.Background(), netconfContext(srv), newNetconfTarget(srv.Port), map[string]any{"timeout": 2, "sleep": 1, "connect_timeout": 1})
	if err == nil || !strings.Contains(err.Error(), "timed out") || srv.Logins() == 0 {
		t.Errorf("an unreadable hello: %v after %d logins", err, srv.Logins())
	}
}

// TestNetconf_AStatItCannotRecordFailsTheTask: a ready listener whose diff
// or port stat cannot be recorded fails the task rather than reporting a
// wait whose outcome nobody can read.
func TestNetconf_AStatItCannotRecordFailsTheTask(t *testing.T) {
	for _, key := range []string{sdk.StatDiff, "port"} {
		t.Run(key, func(t *testing.T) {
			srv := startNetconfServer(t)
			trustOnly(t, srv, srv.Port, true)
			rc := netconfContext(srv)
			rc.failOn = key
			_, err := wait.Netconf(context.Background(), rc, newNetconfTarget(srv.Port), map[string]any{"timeout": 10, "sleep": 1, "connect_timeout": 5})
			if !errors.Is(err, errWaitStat) {
				t.Errorf("failing the %s stat: %v", key, err)
			}
		})
	}
}
