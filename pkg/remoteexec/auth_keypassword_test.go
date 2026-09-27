// Tests that a credential holding a key and a password logs in the way
// OpenSSH's client does, against the in-process SSH server: by key where
// the server refuses passwords, and by password where it knows no key.
package remoteexec

import (
	"context"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

func TestAuthFrom_KeyAndPassword(t *testing.T) {
	keyPEM := marshalTestPrivateKey(t, "")
	signer, err := ssh.ParsePrivateKey(keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	for name, tt := range map[string]struct {
		opts      remoteexectest.Options
		keyLogins int64
	}{
		// Ubuntu's root: PermitRootLogin prohibit-password.
		"a server that refuses passwords": {remoteexectest.Options{AuthorizedKey: signer.PublicKey(), RefusePasswords: true}, 1},
		"a server that knows no key":      {remoteexectest.Options{}, 0},
	} {
		srv, err := remoteexectest.Start(tt.opts)
		if err != nil {
			t.Fatal(err)
		}
		auth, err := AuthFrom(srv.Username, srv.Password, keyPEM, "")
		if err != nil {
			t.Fatal(err)
		}
		result, err := New(Options{InsecureSkipHostKeyVerify: true}).Run(context.Background(), nil, Target{Host: srv.Host, Port: srv.Port}, auth, "echo in")
		if err != nil || result.Stdout != "in\n" {
			t.Errorf("%s: %+v, %v", name, result, err)
		}
		if srv.Logins() != 1 || srv.KeyLogins() != tt.keyLogins {
			t.Errorf("%s: %d logins, %d by key; want 1 and %d", name, srv.Logins(), srv.KeyLogins(), tt.keyLogins)
		}
		srv.Close()
	}

	// The two halves are both part of the identity a Pool keys by.
	both, _ := AuthFrom("u", "p", keyPEM, "")
	other, _ := AuthFrom("u", "q", keyPEM, "")
	keyOnly, _ := AuthFrom("u", "", keyPEM, "")
	if both.identity == other.identity || both.identity == keyOnly.identity {
		t.Error("a key-and-password identity does not depend on both halves")
	}
}
