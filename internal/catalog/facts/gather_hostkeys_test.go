// Tests for the ssh.host_key.* facts facts.gather records, end to end through
// Gather and sdk.Connect: a real in-process SSH server, a real known_hosts
// file read through PLEIADES_KNOWN_HOSTS, and the session's own handshake.
// What is recorded is held to the server's key, read from the server rather
// than from the method.
package facts_test

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sshfacts"
)

// gatherHostKeyServer starts a real in-process SSH server with opts and
// returns it and its address as a gatherServer.
func gatherHostKeyServer(t *testing.T, opts remoteexectest.Options) (*remoteexectest.Server, gatherServer) {
	t.Helper()
	need.POSIXShell(t)
	srv, err := remoteexectest.Start(opts)
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv, gatherServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password, hostKey: srv.HostKey}
}

// gatherHostKeyFacts is rc's ssh.host_key.* facts.
func gatherHostKeyFacts(rc *gatherContext) map[string]any {
	out := map[string]any{}
	for k, v := range rc.facts {
		if strings.HasPrefix(k, "ssh.host_key.") {
			out[k] = v
		}
	}
	return out
}

// gatherWarnings is rc's warnings.
func gatherWarnings(rc *gatherContext) []string {
	w, _ := rc.stats[sdk.StatWarnings].([]string)
	return w
}

// TestGather_RecordsTheHostKeyTheSessionVerified: the verified session's
// ed25519 key is recorded, valued as its SHA-256 fingerprint, and nothing is
// warned.
func TestGather_RecordsTheHostKeyTheSessionVerified(t *testing.T) {
	_, server := gatherHostKeyServer(t, remoteexectest.Options{})
	rc := newGatherContext(server)
	if _, err := gatherRun(t, server, rc, nil); err != nil {
		t.Fatalf("Gather: %v", err)
	}
	want := map[string]any{sshfacts.FactED25519: ssh.FingerprintSHA256(server.hostKey)}
	if got := gatherHostKeyFacts(rc); len(got) != 1 || got[sshfacts.FactED25519] != want[sshfacts.FactED25519] {
		t.Errorf("host key facts = %v, want %v", got, want)
	}
	if w := gatherWarnings(rc); len(w) != 0 {
		t.Errorf("warnings = %q, want none", w)
	}
}

// TestGather_AHostKeyFactThatCannotBeRecordedFailsTheTask: the host key facts
// go through the same emit path as every probe's, so one the store refuses
// fails the task, naming the key, rather than reading as recorded.
func TestGather_AHostKeyFactThatCannotBeRecordedFailsTheTask(t *testing.T) {
	_, server := gatherHostKeyServer(t, remoteexectest.Options{})
	rc := newGatherContext(server)
	rc.failOn = sshfacts.FactED25519
	_, err := gatherRun(t, server, rc, nil)
	if err == nil || !strings.Contains(err.Error(), sshfacts.FactED25519) {
		t.Fatalf("err = %v, want a failure naming %s", err, sshfacts.FactED25519)
	}
	if _, ok := rc.facts[sshfacts.FactED25519]; ok {
		t.Errorf("the refused host key fact was recorded: %v", rc.facts)
	}
}

// TestGather_SkippingVerificationRecordsNoHostKeyAndSaysSo: under the skip
// opt-in the run still gathers, records no host key, and says why; a filter
// that wants no host key fact is not told.
func TestGather_SkippingVerificationRecordsNoHostKeyAndSaysSo(t *testing.T) {
	_, server := gatherHostKeyServer(t, remoteexectest.Options{})
	rc := newGatherContext(server)
	if _, err := facts.Gather(context.Background(), rc, newGatherTarget(server), map[string]any{sdk.ParamInsecureSkipHostKeyVerify: true}); err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if got := gatherHostKeyFacts(rc); len(got) != 0 {
		t.Errorf("host key facts = %v from a session that skipped verification, want none", got)
	}
	if _, ok := rc.facts["os.kernel.name"]; !ok {
		t.Errorf("facts = %v, want the run's other facts still gathered", rc.facts)
	}
	if w := gatherWarnings(rc); len(w) != 1 || !strings.Contains(w[0], "did not verify") {
		t.Errorf("warnings = %q, want one saying the session did not verify the key", w)
	}

	filtered := newGatherContext(server)
	if _, err := facts.Gather(context.Background(), filtered, newGatherTarget(server), map[string]any{sdk.ParamInsecureSkipHostKeyVerify: true, "filter": []any{"os.*"}}); err != nil {
		t.Fatalf("Gather with filter os.*: %v", err)
	}
	if w := gatherWarnings(filtered); len(w) != 0 {
		t.Errorf("warnings = %q for a task that filtered the host key facts out, want none", w)
	}
}

// TestGather_ALegacyRSAKeyIsRecordedWhereTheRecordAllowsIt: a device that
// proves itself only with ssh-rsa (SHA-1) records ssh.host_key.rsa when its
// record allows legacy host keys, and without that has no session at all.
func TestGather_ALegacyRSAKeyIsRecordedWhereTheRecordAllowsIt(t *testing.T) {
	_, server := gatherHostKeyServer(t, remoteexectest.Options{Legacy: true})
	gatherTrustServer(t, server)
	device := newGatherTarget(server)
	device.Props = map[string]inventory.PropertyValue{remoteexec.LegacyKeyExchangeProperty: true, remoteexec.LegacyHostKeysProperty: true}
	rc := newGatherContext(server)
	if _, err := facts.Gather(context.Background(), rc, device, nil); err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if got := gatherHostKeyFacts(rc); len(got) != 1 || got[sshfacts.FactRSA] != ssh.FingerprintSHA256(server.hostKey) {
		t.Errorf("host key facts = %v, want only %s = %s", got, sshfacts.FactRSA, ssh.FingerprintSHA256(server.hostKey))
	}

	device.Props = map[string]inventory.PropertyValue{remoteexec.LegacyKeyExchangeProperty: true}
	refused := newGatherContext(server)
	if _, err := facts.Gather(context.Background(), refused, device, nil); err == nil {
		t.Fatal("Gather reached a server offering only ssh-rsa without legacy host keys allowed")
	}
	if len(refused.facts) != 0 {
		t.Errorf("facts = %v from a refused session, want none", refused.facts)
	}
}

// TestGather_TwoServersBehindOneNameRecordNoHostKey: a read whose dial met two
// servers with different ed25519 keys, both listed for the address, records
// no host key fact and warns, while its other facts are gathered.
func TestGather_TwoServersBehindOneNameRecordNoHostKey(t *testing.T) {
	srv, behind := gatherHostKeyServer(t, remoteexectest.Options{})
	front, err := remoteexectest.StartTwoServers(srv)
	if err != nil {
		t.Fatalf("StartTwoServers: %v", err)
	}
	t.Cleanup(front.Close)
	server := behind
	server.host, server.port = front.Host, front.Port
	gatherTrustServer(t, server, front.FirstKey)
	rc := newGatherContext(server)
	if _, err := facts.Gather(context.Background(), rc, newGatherTarget(server), nil); err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if got := gatherHostKeyFacts(rc); len(got) != 0 {
		t.Errorf("host key facts = %v, want none when two servers answer for one name", got)
	}
	if w := gatherWarnings(rc); len(w) != 1 || !strings.Contains(w[0], "more than one server") {
		t.Errorf("warnings = %q, want one naming more than one server", w)
	}
}

// TestGather_TheFilterAndSubsetsNarrowTheHostKey: the host key facts are the
// min subset's and the filter's like any other: ssh.* alone is a filter the
// plan accepts and gathers only them, and a run without min records none.
func TestGather_TheFilterAndSubsetsNarrowTheHostKey(t *testing.T) {
	_, server := gatherHostKeyServer(t, remoteexectest.Options{})
	only := newGatherContext(server)
	if _, err := gatherRun(t, server, only, map[string]any{"filter": []any{"ssh.*"}}); err != nil {
		t.Fatalf("Gather with filter ssh.*: %v", err)
	}
	if len(only.facts) != 1 || only.facts[sshfacts.FactED25519] == nil {
		t.Errorf("facts = %v, want only %s", only.facts, sshfacts.FactED25519)
	}

	hardware := newGatherContext(server)
	if _, err := gatherRun(t, server, hardware, map[string]any{"gather_subset": []any{"!min", "hardware"}}); err != nil {
		t.Fatalf("Gather without min: %v", err)
	}
	if got := gatherHostKeyFacts(hardware); len(got) != 0 {
		t.Errorf("host key facts = %v from a run without min, want none", got)
	}
}

// gatherRoutedContext is gatherContext for a device reached through a route
// of SSH hops (sdk.Router).
type gatherRoutedContext struct {
	*gatherContext
	route sdk.RouteDial
}

// DeviceRoute implements sdk.Router.
func (c gatherRoutedContext) DeviceRoute() sdk.RouteDial { return c.route }

// TestGather_ABastionsKeyIsNeverTheDevices is RED test 4: a device reached
// through a bastion records its own key, never the bastion's, though both
// were verified against the same known_hosts file.
func TestGather_ABastionsKeyIsNeverTheDevices(t *testing.T) {
	bastion, _ := gatherHostKeyServer(t, remoteexectest.Options{})
	_, server := gatherHostKeyServer(t, remoteexectest.Options{})
	// One file holding both, each under its own address, as a deployment's
	// does.
	gatherTrustLines(t, map[string][]ssh.PublicKey{
		bastion.Addr(): {bastion.HostKey},
		net.JoinHostPort(server.host, strconv.Itoa(server.port)): {server.hostKey},
	})
	hop := remoteexec.Hop{Target: remoteexec.Target{Host: bastion.Host, Port: bastion.Port}, Auth: remoteexec.PasswordAuth(bastion.Username, bastion.Password)}
	route := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return remoteexec.New(remoteexec.Options{MaxRetries: 1}).DialThroughHops(ctx, []remoteexec.Hop{hop}, remoteexec.Target{Host: server.host, Port: server.port})
	}
	rc := gatherRoutedContext{gatherContext: newGatherContext(server), route: route}
	if _, err := facts.Gather(context.Background(), rc, newGatherTarget(server), nil); err != nil {
		t.Fatalf("Gather through the bastion: %v", err)
	}
	got := gatherHostKeyFacts(rc.gatherContext)
	if len(got) != 1 || got[sshfacts.FactED25519] != ssh.FingerprintSHA256(server.hostKey) {
		t.Errorf("host key facts = %v, want only the device's %s (the bastion's is %s)", got, ssh.FingerprintSHA256(server.hostKey), ssh.FingerprintSHA256(bastion.HostKey))
	}
}

// gatherPooledContext is gatherContext with a run's connection pool
// (sdk.ConnectionPooler), so a second task borrows the first one's session.
type gatherPooledContext struct {
	*gatherContext
	pool *remoteexec.Pool
}

// ConnectionPool implements sdk.ConnectionPooler.
func (c gatherPooledContext) ConnectionPool() *remoteexec.Pool { return c.pool }

// TestGather_ABorrowedSessionStillRecordsItsVerifiedKey: a second gather that
// borrows the first one's pooled session, with no handshake of its own,
// records the key that session's handshake verified. The server's login
// count proves the session was borrowed rather than dialed again.
func TestGather_ABorrowedSessionStillRecordsItsVerifiedKey(t *testing.T) {
	srv, server := gatherHostKeyServer(t, remoteexectest.Options{})
	gatherTrustServer(t, server)
	pool := remoteexec.NewPool(0)
	t.Cleanup(func() { _ = pool.Close() })
	for i := 0; i < 2; i++ {
		rc := gatherPooledContext{gatherContext: newGatherContext(server), pool: pool}
		if _, err := facts.Gather(context.Background(), rc, newGatherTarget(server), map[string]any{"filter": []any{"ssh.*"}}); err != nil {
			t.Fatalf("Gather %d: %v", i+1, err)
		}
		if got := rc.facts[sshfacts.FactED25519]; got != ssh.FingerprintSHA256(server.hostKey) {
			t.Errorf("gather %d: %s = %v, want %s", i+1, sshfacts.FactED25519, got, ssh.FingerprintSHA256(server.hostKey))
		}
	}
	if got := srv.Logins(); got != 1 {
		t.Fatalf("server saw %d logins, want 1: the second gather did not borrow the session, so this proved nothing about a borrowed one", got)
	}
}
