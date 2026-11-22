// Package ssh: a stored RSA key against a real OpenSSH server set to take it
// only with an ssh-rsa (SHA-1) signature, through the ssh_exec transport
// every ssh_exec task runs on. OpenSSH 10.3 so set advertises
// server-sig-algs=<ssh-rsa> (measured 2026-11-22 with OpenSSH's own client),
// which is the path golang.org/x/crypto/ssh signs ssh-rsa on, so this is the
// real SHA-1 login, refused unless the record allows it.
package ssh

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	cryptossh "golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestSSHContainer_AnRSAKeySignsSHA1OnlyWhereTheRecordAllowsIt proves, against
// a real OpenSSH server that takes an RSA key only with ssh-rsa: a key-only
// credential is refused on a record allowing nothing, naming the property; the
// property logs in and runs the command, warned about; and a key and password
// credential logs in with the password, warned that the key was not used. The
// server's own log says which method each login used.
func TestSSHContainer_AnRSAKeySignsSHA1OnlyWhereTheRecordAllowsIt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH container integration test in short mode")
	}
	ctx := context.Background()
	srv, err := testsupport.StartSSHDConfigured(ctx, "PubkeyAcceptedAlgorithms ssh-rsa\n")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Terminate(context.Background()) })

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	block, err := cryptossh.MarshalPrivateKey(key, "")
	if err != nil {
		t.Fatal(err)
	}
	pub, err := cryptossh.NewPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.AuthorizeKey(ctx, pub); err != nil {
		t.Fatal(err)
	}
	known, err := srv.KnownHosts(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tr := New(Options{KnownHostsPath: known, DialTimeout: 10 * time.Second})
	endpoint := transport.NetworkEndpoint{Host: srv.Host, Port: srv.Port}
	keyOnly := credential.Credential{Username: testsupport.SSHDUser, PrivateKeyPEM: pem.EncodeToMemory(block)}

	_, err = tr.Exec(ctx, transport.Target{Endpoint: endpoint}, keyOnly, "echo in")
	if refused, ok := errors.AsType[*remoteexec.LegacySignatureRefusedError](err); !ok || refused.Property != remoteexec.LegacyUserKeySignaturesProperty {
		t.Fatalf("a key-only login on a record allowing nothing: %v, want a refusal naming %s", err, remoteexec.LegacyUserKeySignaturesProperty)
	}

	allowed := sdk.WithConnectionNotes(ctx, "db-old")
	res, err := tr.Exec(allowed, transport.Target{Endpoint: endpoint, SSHLegacy: remoteexec.Legacy{UserKeySignatures: true}}, keyOnly, "echo in")
	if err != nil || strings.TrimSpace(res.Stdout) != "in" {
		t.Fatalf("a key-only login on a record allowing ssh-rsa: %+v, %v", res, err)
	}
	if w := strings.Join(sdk.ConnectionWarnings(allowed), "\n"); !strings.Contains(w, "used legacy SSH user key signature ssh-rsa") {
		t.Errorf("warnings %q do not name the ssh-rsa signature", w)
	}

	both := keyOnly
	both.Password = testsupport.SSHDPassword
	skipped := sdk.WithConnectionNotes(ctx, "db-old")
	res, err = tr.Exec(skipped, transport.Target{Endpoint: endpoint}, both, "echo in")
	if err != nil || strings.TrimSpace(res.Stdout) != "in" {
		t.Fatalf("a key and password login on a record allowing nothing: %+v, %v", res, err)
	}
	if w := strings.Join(sdk.ConnectionWarnings(skipped), "\n"); !strings.Contains(w, `device "db-old" did not use the stored RSA key`) {
		t.Errorf("warnings %q do not say the RSA key was not used", w)
	}

	// The server's own account: one login by key, and it takes a key only
	// with ssh-rsa, and one by password.
	log, err := srv.RootExec(ctx, "cat /config/logs/openssh/current")
	if err != nil {
		t.Fatalf("reading sshd's log: %v", err)
	}
	if got := strings.Count(log, "Accepted publickey for "+testsupport.SSHDUser); got != 1 {
		t.Errorf("sshd accepted %d key logins, want 1:\n%s", got, log)
	}
	if got := strings.Count(log, "Accepted password for "+testsupport.SSHDUser); got != 1 {
		t.Errorf("sshd accepted %d password logins, want 1:\n%s", got, log)
	}
}
