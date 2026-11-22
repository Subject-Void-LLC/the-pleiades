// Tests that the legacy user key signature rule follows a connection where it
// goes: a Pool never lends a login signed with ssh-rsa to a record that does
// not allow it, and a bastion's skipped key is warned about on the connection
// it carries, whether its hops were dialed by Connect or by a route
// (DialThroughHops under ConnectVia).
package remoteexec

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// TestPool_NeverLendsALegacySignedLoginToARecordWithoutTheProperty proves a
// pooled connection whose key signed with ssh-rsa is lent again only to a
// record that allows it, warned about on every lend, and that a record
// without the property is refused afresh rather than handed that login.
func TestPool_NeverLendsALegacySignedLoginToARecordWithoutTheProperty(t *testing.T) {
	keyPEM, pub := rsaKeyPEM(t, "")
	s := startKeyServer(t, pub, []string{ssh.KeyAlgoRSA}, true)
	auth := keyOnly(t, s.srv.Username, keyPEM, "")
	pool := NewPool(time.Minute)
	t.Cleanup(func() { _ = pool.Close() })
	ctx := context.Background()
	allowed := s.target
	allowed.Legacy = Legacy{UserKeySignatures: true}

	for i := range 2 {
		conn, err := pool.Connect(ctx, s.runner, "sw-access-07", allowed, auth)
		if err != nil {
			t.Fatalf("borrow %d: %v", i, err)
		}
		if w := strings.Join(conn.LegacyWarnings("sw-access-07"), "\n"); !strings.Contains(w, "used legacy SSH user key signature ssh-rsa") {
			t.Errorf("borrow %d warned %q, want the ssh-rsa signature named", i, w)
		}
		_ = conn.Close()
	}
	if s.srv.Logins() != 1 {
		t.Fatalf("%d logins, want the second borrow lent the first", s.srv.Logins())
	}

	_, err := pool.Connect(ctx, s.runner, "sw-access-07", s.target, auth)
	if _, ok := errors.AsType[*LegacySignatureRefusedError](err); !ok {
		t.Fatalf("a record without the property: %v, want a fresh login refused", err)
	}
	if s.srv.KeyLogins() != 1 {
		t.Errorf("%d key logins, want only the first", s.srv.KeyLogins())
	}
}

// TestHops_ABastionsSkippedKeyIsWarned proves a bastion whose key and
// password credential logged in by password, because it takes the RSA key
// only with ssh-rsa, is warned about on the connection to the device behind
// it, whichever way the hops were dialed.
func TestHops_ABastionsSkippedKeyIsWarned(t *testing.T) {
	need.POSIXShell(t)
	keyPEM, pub := rsaKeyPEM(t, "")
	bastion, err := remoteexectest.Start(remoteexectest.Options{AuthorizedKey: pub, PublicKeyAuthAlgorithms: []string{ssh.KeyAlgoRSA}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bastion.Close)
	device, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(device.Close)

	r := New(Options{KnownHostsPath: writeMultiKnownHosts(t, map[string]ssh.PublicKey{bastion.Addr(): bastion.HostKey, device.Addr(): device.HostKey})})
	hopAuth, err := AuthFrom(bastion.Username, bastion.Password, keyPEM, "")
	if err != nil {
		t.Fatal(err)
	}
	hops := []Hop{{Target: Target{Host: bastion.Host, Port: bastion.Port}, Auth: hopAuth}}
	target := Target{Host: device.Host, Port: device.Port}
	auth := PasswordAuth(device.Username, device.Password)
	ctx := context.Background()
	route := func(ctx context.Context, _, address string) (net.Conn, error) {
		host, port := splitHostPortT(t, address)
		return r.DialThroughHops(ctx, hops, Target{Host: host, Port: port})
	}

	for name, connect := range map[string]func() (*Conn, error){
		"Connect with hops":  func() (*Conn, error) { return r.Connect(ctx, hops, target, auth) },
		"ConnectVia a route": func() (*Conn, error) { return r.ConnectVia(ctx, route, target, auth) },
	} {
		conn, err := connect()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := `device "bastion ` + bastion.Addr() + ` carrying db-old" did not use the stored RSA key`
		if w := strings.Join(conn.LegacyWarnings("db-old"), "\n"); !strings.Contains(w, want) {
			t.Errorf("%s warned %q, want %q", name, w, want)
		}
		_ = conn.Close()
	}
	if bastion.KeyLogins() != 0 || bastion.Logins() != 2 {
		t.Errorf("the bastion saw %d logins, %d by key; want two by password", bastion.Logins(), bastion.KeyLogins())
	}
}
