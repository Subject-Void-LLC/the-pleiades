// Tests for VerifiedHostKeys: what a connection's known_hosts check accepted
// for its target, over real SSH handshakes (remoteexectest's in-process
// server, this package's fake listener for a bastion), with real known_hosts
// files. Each test fails with the capture removed, since the zero HostKeys
// reports no key.
package remoteexec

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// startHostKeyServer starts a real in-process SSH server with opts for one test.
func startHostKeyServer(t *testing.T, opts remoteexectest.Options) *remoteexectest.Server {
	t.Helper()
	need.POSIXShell(t)
	srv, err := remoteexectest.Start(opts)
	if err != nil {
		t.Fatalf("starting the in-process SSH server: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// writeKnownHostsLines writes one known_hosts line per key, all for hostPort,
// into a fresh file, and returns its path.
func writeKnownHostsLines(t *testing.T, hostPort string, keys ...ssh.PublicKey) string {
	t.Helper()
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(knownhosts.Line([]string{hostPort}, key) + "\n")
	}
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("writing known_hosts: %v", err)
	}
	return path
}

// wantKeys fails t unless hk was verified and accepted exactly want, in
// order, with no more kept.
func wantKeys(t *testing.T, hk HostKeys, want ...ssh.PublicKey) {
	t.Helper()
	if !hk.Verified() {
		t.Fatal("Verified() = false on a connection whose known_hosts check ran")
	}
	got, more := hk.Keys()
	if more {
		t.Error("Keys() says more were accepted than kept")
	}
	if len(got) != len(want) {
		t.Fatalf("Keys() = %d keys, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i].Marshal(), want[i].Marshal()) {
			t.Errorf("key %d = %s, want %s", i, ssh.FingerprintSHA256(got[i]), ssh.FingerprintSHA256(want[i]))
		}
	}
}

// TestVerifiedHostKeys_AConnectionKeepsTheKeyItsCheckAccepted is the direct
// case: the server's own key, from a real handshake checked against a real
// known_hosts line.
func TestVerifiedHostKeys_AConnectionKeepsTheKeyItsCheckAccepted(t *testing.T) {
	srv := startHostKeyServer(t, remoteexectest.Options{})
	r := New(Options{KnownHostsPath: writeKnownHosts(t, srv.Addr(), srv.HostKey), MaxRetries: 1})
	conn, err := r.Connect(context.Background(), nil, target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()
	wantKeys(t, conn.VerifiedHostKeys(), srv.HostKey)
	// A view of the connection reports the same keys (sdk.Connect hands a
	// network device's method the Raw view).
	wantKeys(t, conn.Raw().VerifiedHostKeys(), srv.HostKey)
}

// TestVerifiedHostKeys_SkippingVerificationKeepsNone: under the skip opt-in
// the callback accepts whatever key it is shown, so none is kept and the
// connection says it did not verify.
func TestVerifiedHostKeys_SkippingVerificationKeepsNone(t *testing.T) {
	srv := startHostKeyServer(t, remoteexectest.Options{})
	r := New(Options{InsecureSkipHostKeyVerify: true, MaxRetries: 1})
	conn, err := r.Connect(context.Background(), nil, target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()
	hk := conn.VerifiedHostKeys()
	if hk.Verified() {
		t.Error("Verified() = true on a connection that skipped the check")
	}
	if keys, _ := hk.Keys(); len(keys) != 0 {
		t.Errorf("Keys() = %d keys from a connection that skipped the check, want none", len(keys))
	}
}

// TestVerifiedHostKeys_ALegacyRSAKeyIsKeptOnlyWhereTheRecordAllowsIt: a
// server proving itself only with ssh-rsa (SHA-1), as an IOS 15.2 switch
// does, is reached with the record's legacy allowances and its RSA key kept;
// without them there is no session at all, and so no key.
func TestVerifiedHostKeys_ALegacyRSAKeyIsKeptOnlyWhereTheRecordAllowsIt(t *testing.T) {
	srv := startHostKeyServer(t, remoteexectest.Options{Legacy: true})
	r := New(Options{KnownHostsPath: writeKnownHosts(t, srv.Addr(), srv.HostKey), MaxRetries: 1})
	auth := PasswordAuth(srv.Username, srv.Password)

	allowed := Target{Host: srv.Host, Port: srv.Port, Legacy: Legacy{KeyExchange: true, HostKeys: true}}
	conn, err := r.Connect(context.Background(), nil, allowed, auth)
	if err != nil {
		t.Fatalf("Connect with legacy host keys allowed: %v", err)
	}
	defer func() { _ = conn.Close() }()
	wantKeys(t, conn.VerifiedHostKeys(), srv.HostKey)
	if got := srv.HostKey.Type(); got != ssh.KeyAlgoRSA {
		t.Fatalf("the legacy server's key is %s, want an RSA key", got)
	}

	refused := Target{Host: srv.Host, Port: srv.Port, Legacy: Legacy{KeyExchange: true}}
	if c, err := r.Connect(context.Background(), nil, refused, auth); err == nil {
		_ = c.Close()
		t.Fatal("Connect without legacy host keys allowed reached a server offering only ssh-rsa")
	}
}

// TestVerifiedHostKeys_AJumpHostsKeyIsNeverTheDevices: through a bastion,
// only the target's own leg is kept, though the bastion's key was checked
// against the same known_hosts file.
func TestVerifiedHostKeys_AJumpHostsKeyIsNeverTheDevices(t *testing.T) {
	bastionAddr, bastionKey := startFakeSSHListener(t, func(string) (string, string, int) { return "", "", 1 })
	targetAddr, targetKey := startFakeSSHListener(t, func(cmd string) (string, string, int) { return "ok", "", 0 })
	r := New(Options{KnownHostsPath: writeMultiKnownHosts(t, map[string]ssh.PublicKey{bastionAddr: bastionKey, targetAddr: targetKey}), MaxRetries: 1})
	bHost, bPort := splitHostPortT(t, bastionAddr)
	tHost, tPort := splitHostPortT(t, targetAddr)
	conn, err := r.Connect(context.Background(), []Hop{{Target: Target{Host: bHost, Port: bPort}, Auth: testAuth}}, Target{Host: tHost, Port: tPort}, testAuth)
	if err != nil {
		t.Fatalf("Connect through the bastion: %v", err)
	}
	defer func() { _ = conn.Close() }()
	wantKeys(t, conn.VerifiedHostKeys(), targetKey)
}

// TestVerifiedHostKeys_ARouteKeepsOnlyTheTargetsKey: ConnectVia, the path
// sdk.Connect takes for a device behind a route, keeps the target's key and
// nothing of whatever dialed the route.
func TestVerifiedHostKeys_ARouteKeepsOnlyTheTargetsKey(t *testing.T) {
	srv := startHostKeyServer(t, remoteexectest.Options{})
	r := New(Options{KnownHostsPath: writeKnownHosts(t, srv.Addr(), srv.HostKey), MaxRetries: 1})
	var d net.Dialer
	conn, err := r.ConnectVia(context.Background(), d.DialContext, target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("ConnectVia: %v", err)
	}
	defer func() { _ = conn.Close() }()
	wantKeys(t, conn.VerifiedHostKeys(), srv.HostKey)
}

// TestVerifiedHostKeys_ALentConnectionReportsItsOwnHandshake: a Pool lends a
// connection again with no handshake, and the later borrower still reads the
// key the first dial verified. The server's login count proves the second
// borrow reused the connection rather than dialing again.
func TestVerifiedHostKeys_ALentConnectionReportsItsOwnHandshake(t *testing.T) {
	srv := startHostKeyServer(t, remoteexectest.Options{})
	r := New(Options{KnownHostsPath: writeKnownHosts(t, srv.Addr(), srv.HostKey), MaxRetries: 1})
	p := NewPool(0)
	defer func() { _ = p.Close() }()
	auth := PasswordAuth(srv.Username, srv.Password)
	for i := 0; i < 2; i++ {
		conn, err := p.Connect(context.Background(), r, "web1", target(srv), auth)
		if err != nil {
			t.Fatalf("borrow %d: %v", i+1, err)
		}
		wantKeys(t, conn.VerifiedHostKeys(), srv.HostKey)
		if err := conn.Close(); err != nil {
			t.Fatalf("give back %d: %v", i+1, err)
		}
	}
	if got := srv.Logins(); got != 1 {
		t.Fatalf("server saw %d logins, want 1: the second borrow did not reuse the connection, so this proved nothing about a lent one", got)
	}
}

// TestVerifiedHostKeys_ARetryKeepsOnlyTheSessionsKey: the first dial attempt
// reaches a server whose key known_hosts also lists for the address and which
// drops the login, and the retry reaches the real one. Only the key of the
// attempt that became the session is the connection's, and the connection
// says an earlier attempt accepted another, so pkg/sshfacts can refuse to
// record either.
func TestVerifiedHostKeys_ARetryKeepsOnlyTheSessionsKey(t *testing.T) {
	cases := map[string]struct {
		opts   remoteexectest.Options
		legacy Legacy
	}{
		"same type":      {},
		"different type": {opts: remoteexectest.Options{Legacy: true}, legacy: Legacy{KeyExchange: true, HostKeys: true}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := startHostKeyServer(t, tc.opts)
			front, err := remoteexectest.StartTwoServers(srv)
			if err != nil {
				t.Fatalf("StartTwoServers: %v", err)
			}
			t.Cleanup(front.Close)
			knownHosts := writeKnownHostsLines(t, front.Addr(), front.FirstKey, srv.HostKey)
			r := New(Options{KnownHostsPath: knownHosts, MaxRetries: 3})
			conn, err := r.Connect(context.Background(), nil, Target{Host: front.Host, Port: front.Port, Legacy: tc.legacy}, PasswordAuth(srv.Username, srv.Password))
			if err != nil {
				t.Fatalf("Connect: %v", err)
			}
			defer func() { _ = conn.Close() }()
			hk := conn.VerifiedHostKeys()
			wantKeys(t, hk, srv.HostKey)
			if !hk.OtherAttempt() {
				t.Error("OtherAttempt() = false, though the first attempt accepted the other server's key")
			}
		})
	}
}

// TestVerifiedHostKeys_OneServerIsNoOtherAttempt: a dial that succeeds at
// once says no earlier attempt accepted another key.
func TestVerifiedHostKeys_OneServerIsNoOtherAttempt(t *testing.T) {
	srv := startHostKeyServer(t, remoteexectest.Options{})
	r := New(Options{KnownHostsPath: writeKnownHosts(t, srv.Addr(), srv.HostKey), MaxRetries: 3})
	conn, err := r.Connect(context.Background(), nil, target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if conn.VerifiedHostKeys().OtherAttempt() {
		t.Error("OtherAttempt() = true on a dial that reached one server once")
	}
}

// TestHostKeyLog_AttemptsKeepEarlierKeysApart: what an attempt accepted moves
// to earlier when the next begins; the same key again is no other attempt.
func TestHostKeyLog_AttemptsKeepEarlierKeysApart(t *testing.T) {
	a, b := generateTestHostKey(t).PublicKey(), generateTestHostKey(t).PublicKey()
	log := newHostKeyLog(true)
	log.beginAttempt()
	log.add(a)
	log.beginAttempt()
	log.add(a)
	if hk := log.snapshot(); hk.OtherAttempt() {
		t.Error("a retry that met the same key reports another attempt")
	}
	log.beginAttempt()
	log.add(b)
	hk := log.snapshot()
	wantKeys(t, hk, b)
	if !hk.OtherAttempt() {
		t.Error("a retry that met another key does not report it")
	}
}

// TestKeepAccepted_KeepsNothingRefusedAndNeverTheProbe: a key the check
// refuses is not kept, the algorithm probe is not kept even if a check
// accepted it, and a key accepted twice is kept once, up to the bound.
func TestKeepAccepted_KeepsNothingRefusedAndNeverTheProbe(t *testing.T) {
	accepted := generateTestHostKey(t).PublicKey()
	refused := generateTestHostKey(t).PublicKey()
	log := newHostKeyLog(true)
	cb := keepAccepted(func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if bytes.Equal(key.Marshal(), refused.Marshal()) {
			return errors.New("refused")
		}
		return nil
	}, log)
	remote := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22}
	for _, key := range []ssh.PublicKey{refused, hostKeyProbe(), accepted, accepted} {
		_ = cb("web1:22", remote, key)
	}
	wantKeys(t, log.snapshot(), accepted)

	for i := 0; i < maxHostKeys+1; i++ {
		_ = cb("web1:22", remote, generateTestHostKey(t).PublicKey())
	}
	if keys, more := log.snapshot().Keys(); len(keys) != maxHostKeys || !more {
		t.Errorf("after %d more keys: kept %d, more = %v; want %d kept and more set", maxHostKeys+1, len(keys), more, maxHostKeys)
	}
	if hk := (*Conn)(nil).VerifiedHostKeys(); hk.Verified() {
		t.Error("a nil Conn reports a verified host key")
	}
}
