// Tests for the ssh.host_key.* facts over real SSH sessions: remoteexectest's
// in-process server, checked against real known_hosts files, with the value
// held to what OpenSSH's own ssh-keygen -lf prints for the same key.
package sshfacts_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sshfacts"
)

// recorder is a RunbookContext keeping facts and stats apart, and failing
// EmitFact when fail is set.
type recorder struct {
	facts map[string]any
	stats map[string]any
	fail  bool
}

// newRecorder returns an empty recorder.
func newRecorder() *recorder {
	return &recorder{facts: map[string]any{}, stats: map[string]any{}}
}

// InjectSecrets returns no secret.
func (r *recorder) InjectSecrets() map[string]string { return nil }

// SetStat keeps a stat.
func (r *recorder) SetStat(key string, value any) error {
	r.stats[key] = value
	return nil
}

// EmitFact keeps a fact, or fails when fail is set.
func (r *recorder) EmitFact(key string, value any) error {
	if r.fail {
		return errors.New("recording failed")
	}
	r.facts[key] = value
	return nil
}

// startServer starts a real in-process SSH server for one test.
func startServer(t *testing.T, opts remoteexectest.Options) *remoteexectest.Server {
	t.Helper()
	need.POSIXShell(t)
	srv, err := remoteexectest.Start(opts)
	if err != nil {
		t.Fatalf("starting the in-process SSH server: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// knownHostsFile writes one known_hosts line per key for hostPort.
func knownHostsFile(t *testing.T, hostPort string, keys ...ssh.PublicKey) string {
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

// session dials target with opts, as a facts method's connection is dialed,
// and returns what its check accepted.
func session(t *testing.T, opts remoteexec.Options, target remoteexec.Target, srv *remoteexectest.Server) remoteexec.HostKeys {
	t.Helper()
	opts.MaxRetries = 3
	conn, err := remoteexec.New(opts).Connect(context.Background(), nil, target, remoteexec.PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()
	return conn.VerifiedHostKeys()
}

// keygenFingerprint is what ssh-keygen -lf prints as key's fingerprint, the
// second field of its one line, read with OpenSSH's own tool.
func keygenFingerprint(t *testing.T, key ssh.PublicKey) string {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("needs ssh-keygen: OpenSSH's client tools are not installed")
	}
	path := filepath.Join(t.TempDir(), "host_key.pub")
	if err := os.WriteFile(path, ssh.MarshalAuthorizedKey(key), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ssh-keygen", "-lf", path).Output() // #nosec G204 -- a fixed tool on a file this test wrote
	if err != nil {
		t.Fatalf("ssh-keygen -lf: %v", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		t.Fatalf("ssh-keygen -lf printed %q", out)
	}
	return fields[1]
}

// TestRecord_AVerifiedSessionRecordsItsKeyAsSSHKeygenPrintsIt is RED test 1:
// a session whose known_hosts check accepted the server's ed25519 key records
// ssh.host_key.ed25519, and its value is exactly what ssh-keygen -lf prints.
func TestRecord_AVerifiedSessionRecordsItsKeyAsSSHKeygenPrintsIt(t *testing.T) {
	srv := startServer(t, remoteexectest.Options{})
	seen := session(t, remoteexec.Options{KnownHostsPath: knownHostsFile(t, srv.Addr(), srv.HostKey)}, remoteexec.Target{Host: srv.Host, Port: srv.Port}, srv)
	rc := newRecorder()
	warnings, err := sshfacts.Record(rc, "web1", seen)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("Record = %q, %v; want no warning", warnings, err)
	}
	want := keygenFingerprint(t, srv.HostKey)
	if got := rc.facts[sshfacts.FactED25519]; got != want {
		t.Errorf("%s = %v, want %q (ssh-keygen -lf)", sshfacts.FactED25519, got, want)
	}
	if want != ssh.FingerprintSHA256(srv.HostKey) {
		t.Errorf("ssh-keygen printed %q and the library %q", want, ssh.FingerprintSHA256(srv.HostKey))
	}
	if len(rc.facts) != 1 || len(rc.stats) != 0 {
		t.Errorf("facts = %v, stats = %v; want the one fact and no stat", rc.facts, rc.stats)
	}
}

// TestRecord_ASkippedVerificationRecordsNothingAndSaysSo is RED test 2.
func TestRecord_ASkippedVerificationRecordsNothingAndSaysSo(t *testing.T) {
	srv := startServer(t, remoteexectest.Options{})
	seen := session(t, remoteexec.Options{InsecureSkipHostKeyVerify: true}, remoteexec.Target{Host: srv.Host, Port: srv.Port}, srv)
	rc := newRecorder()
	warnings, err := sshfacts.Record(rc, "vm7", seen)
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.facts) != 0 {
		t.Errorf("facts = %v from a session that skipped verification, want none", rc.facts)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "did not verify") || !strings.Contains(warnings[0], `"vm7"`) {
		t.Errorf("warnings = %q, want one saying the session did not verify the key", warnings)
	}
}

// TestRecord_ALegacyRSAKeyIsRecordedAsRSA is RED test 3: a server proving
// itself only with ssh-rsa (SHA-1), reached with the record's legacy
// allowances, records ssh.host_key.rsa, under the key's type rather than its
// SHA-1 signature algorithm.
func TestRecord_ALegacyRSAKeyIsRecordedAsRSA(t *testing.T) {
	srv := startServer(t, remoteexectest.Options{Legacy: true})
	target := remoteexec.Target{Host: srv.Host, Port: srv.Port, Legacy: remoteexec.Legacy{KeyExchange: true, HostKeys: true}}
	seen := session(t, remoteexec.Options{KnownHostsPath: knownHostsFile(t, srv.Addr(), srv.HostKey)}, target, srv)
	rc := newRecorder()
	if _, err := sshfacts.Record(rc, "sw3", seen); err != nil {
		t.Fatal(err)
	}
	if got, want := rc.facts[sshfacts.FactRSA], keygenFingerprint(t, srv.HostKey); got != want {
		t.Errorf("%s = %v, want %q", sshfacts.FactRSA, got, want)
	}
	if len(rc.facts) != 1 {
		t.Errorf("facts = %v, want only the RSA key's", rc.facts)
	}
}

// TestRecord_TwoServersBehindOneNameRecordNone is RED test 5: one read whose
// dial met two servers, both in known_hosts for the address, records no host
// key fact and warns, whether the second server's key is of the same type as
// the first's or another.
func TestRecord_TwoServersBehindOneNameRecordNone(t *testing.T) {
	cases := map[string]struct {
		opts   remoteexectest.Options
		legacy remoteexec.Legacy
	}{
		"same type":      {},
		"different type": {opts: remoteexectest.Options{Legacy: true}, legacy: remoteexec.Legacy{KeyExchange: true, HostKeys: true}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := startServer(t, tc.opts)
			front, err := remoteexectest.StartTwoServers(srv)
			if err != nil {
				t.Fatalf("StartTwoServers: %v", err)
			}
			t.Cleanup(front.Close)
			target := remoteexec.Target{Host: front.Host, Port: front.Port, Legacy: tc.legacy}
			seen := session(t, remoteexec.Options{KnownHostsPath: knownHostsFile(t, front.Addr(), front.FirstKey, srv.HostKey)}, target, srv)
			rc := newRecorder()
			warnings, err := sshfacts.Record(rc, "web1", seen)
			if err != nil {
				t.Fatal(err)
			}
			if len(rc.facts) != 0 {
				t.Errorf("facts = %v, want none when two servers answer for one name", rc.facts)
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], "more than one server") {
				t.Errorf("warnings = %q, want one naming more than one server", warnings)
			}
		})
	}
}

// TestRecord_TwoConnectionsToDifferentServersRecordNone: two verified
// connections of one read that proved themselves with different keys record
// none, and two to the same server record its key once.
func TestRecord_TwoConnectionsToDifferentServersRecordNone(t *testing.T) {
	a := startServer(t, remoteexectest.Options{})
	b := startServer(t, remoteexectest.Options{})
	first := session(t, remoteexec.Options{KnownHostsPath: knownHostsFile(t, a.Addr(), a.HostKey)}, remoteexec.Target{Host: a.Host, Port: a.Port}, a)
	second := session(t, remoteexec.Options{KnownHostsPath: knownHostsFile(t, b.Addr(), b.HostKey)}, remoteexec.Target{Host: b.Host, Port: b.Port}, b)
	if facts, w := sshfacts.Read("web1", first, second); len(facts) != 0 || len(w) != 1 {
		t.Errorf("Read over two servers = %+v, %q; want no fact and one warning", facts, w)
	}
	if facts, w := sshfacts.Read("web1", first, first); len(facts) != 1 || len(w) != 0 {
		t.Errorf("Read over one server twice = %+v, %q; want its one fact", facts, w)
	}
}

// TestRecord_AFailedEmitIsReturned: a fact the context cannot record fails
// the call, naming the fact.
func TestRecord_AFailedEmitIsReturned(t *testing.T) {
	srv := startServer(t, remoteexectest.Options{})
	seen := session(t, remoteexec.Options{KnownHostsPath: knownHostsFile(t, srv.Addr(), srv.HostKey)}, remoteexec.Target{Host: srv.Host, Port: srv.Port}, srv)
	rc := newRecorder()
	rc.fail = true
	if _, err := sshfacts.Record(rc, "web1", seen); err == nil || !strings.Contains(err.Error(), sshfacts.FactED25519) {
		t.Errorf("Record = %v, want an error naming %s", err, sshfacts.FactED25519)
	}
}

// TestReturns_DocumentsEveryFact: each fact key has its documentation, and
// no other is documented.
func TestReturns_DocumentsEveryFact(t *testing.T) {
	returns := sshfacts.Returns()
	if len(returns) != len(sshfacts.FactKeys) {
		t.Fatalf("Returns() documents %d facts, want %d", len(returns), len(sshfacts.FactKeys))
	}
	for i, r := range returns {
		if r.Name != sshfacts.FactKeys[i] || r.Type != "string" || r.Description == "" || !strings.HasPrefix(r.Name, sshfacts.Family) {
			t.Errorf("Returns()[%d] = %+v, want %s, a described string", i, r, sshfacts.FactKeys[i])
		}
	}
}
