// Tests that a stored RSA or DSA key signs a login with SHA-2 unless the
// device's record allows a legacy signature, against the in-process SSH server
// set to take a key only with ssh-rsa (or ssh-dss), as OpenSSH set to
// PubkeyAcceptedAlgorithms ssh-rsa does (measured on OpenSSH 10.3), and that
// every outcome is warned about. A server that sends no server-sig-algs at all
// (OpenSSH before 7.2 and some network devices, by their documentation, not
// measured here) the in-process server cannot imitate. The server reports the
// algorithm it verified, so each test reads what was signed from the far side
// rather than from the client's own account.
package remoteexec

import (
	"context"
	"crypto/dsa"
	"crypto/rand"
	"crypto/rsa"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"math/big"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// keyServer is an in-process SSH server that accepts one stored key, and a
// Runner that verifies its host key against a real known_hosts file.
type keyServer struct {
	srv    *remoteexectest.Server
	runner *Runner
	target Target
}

// startKeyServer starts a server that accepts key for its user, takes a key
// only with algorithms (the library's default set when nil) and refuses
// every password when refusePasswords is set.
func startKeyServer(t *testing.T, key ssh.PublicKey, algorithms []string, refusePasswords bool) keyServer {
	t.Helper()
	need.POSIXShell(t)
	srv, err := remoteexectest.Start(remoteexectest.Options{AuthorizedKey: key, PublicKeyAuthAlgorithms: algorithms, RefusePasswords: refusePasswords})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	target := Target{Host: srv.Host, Port: srv.Port}
	path := filepath.Join(t.TempDir(), "known_hosts")
	writeKnownHostsFor(t, path, target, srv.HostKey)
	return keyServer{srv: srv, runner: New(Options{KnownHostsPath: path}), target: target}
}

// connect logs in to s with auth under legacy.
func (s keyServer) connect(auth Auth, legacy Legacy) (*Conn, error) {
	target := s.target
	target.Legacy = legacy
	return s.runner.Connect(context.Background(), nil, target, auth)
}

// rsaKeyPEM returns a new RSA private key, OpenSSH-encoded and encrypted under
// passphrase when it is not empty, and its public half.
func rsaKeyPEM(t *testing.T, passphrase string) ([]byte, ssh.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(key, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(key, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(block), pub
}

// dsaKeyPEM returns a new DSA private key in the "DSA PRIVATE KEY" PEM form
// OpenSSH's ssh-keygen -t dsa wrote, and its public half.
func dsaKeyPEM(t *testing.T) ([]byte, ssh.PublicKey) {
	t.Helper()
	var key dsa.PrivateKey
	if err := dsa.GenerateParameters(&key.Parameters, rand.Reader, dsa.L1024N160); err != nil {
		t.Fatal(err)
	}
	if err := dsa.GenerateKey(&key, rand.Reader); err != nil {
		t.Fatal(err)
	}
	der, err := asn1.Marshal(struct {
		Version       int
		P, Q, G, Y, X *big.Int
	}{0, key.P, key.Q, key.G, key.Y, key.X})
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "DSA PRIVATE KEY", Bytes: der}), pub
}

// keyOnly returns an Auth holding only keyPEM, for user.
func keyOnly(t *testing.T, user string, keyPEM []byte, passphrase string) Auth {
	t.Helper()
	auth, err := PrivateKeyAuth(user, keyPEM, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

// TestConnect_AnRSAKeySignsWithSHA2WhereTheServerTakesIt proves an RSA key
// signs with rsa-sha2-512 on a server that advertises SHA-2, even one that
// also takes ssh-rsa (the library's default set), and even with the property
// set; the property then only warns that the connection did not need it.
func TestConnect_AnRSAKeySignsWithSHA2WhereTheServerTakesIt(t *testing.T) {
	keyPEM, pub := rsaKeyPEM(t, "")
	for name, algorithms := range map[string][]string{
		"the default set, ssh-rsa included": nil,
		"SHA-2 only":                        {ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSASHA512},
	} {
		t.Run(name, func(t *testing.T) {
			s := startKeyServer(t, pub, algorithms, true)
			auth := keyOnly(t, s.srv.Username, keyPEM, "")

			conn, err := s.connect(auth, Legacy{})
			if err != nil {
				t.Fatal(err)
			}
			if w := conn.LegacyWarnings("db-new"); w != nil {
				t.Errorf("a SHA-2 login on a record allowing nothing warned %q", w)
			}
			_ = conn.Close()

			conn, err = s.connect(auth, Legacy{UserKeySignatures: true})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if got := s.srv.KeySignatures(); !slices.Equal(got, []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA512}) {
				t.Errorf("the server verified %v, want rsa-sha2-512 both times", got)
			}
			warnings := conn.LegacyWarnings("db-new")
			if len(warnings) != 1 || !strings.Contains(warnings[0], "did not need it") || !strings.Contains(warnings[0], LegacyUserKeySignaturesProperty) {
				t.Errorf("warnings %q, want one saying %s was not needed", warnings, LegacyUserKeySignaturesProperty)
			}
		})
	}
}

// TestRSAKeyRefusesSHA1WithoutProperty proves an RSA key on a server that
// takes it only with ssh-rsa is refused unless the record sets the property,
// naming it, whether the key is encrypted or not; that no other legacy flag
// allows it; that the refusal is an answer, never retried or counted against
// the circuit; and that the property logs in with ssh-rsa, warned about.
func TestRSAKeyRefusesSHA1WithoutProperty(t *testing.T) {
	for _, passphrase := range []string{"", "pass phrase"} {
		t.Run("passphrase "+strconv.Quote(passphrase), func(t *testing.T) {
			keyPEM, pub := rsaKeyPEM(t, passphrase)
			s := startKeyServer(t, pub, []string{ssh.KeyAlgoRSA}, true)
			auth := keyOnly(t, s.srv.Username, keyPEM, passphrase)

			for _, legacy := range []Legacy{{}, {KeyExchange: true, HostKeys: true, Ciphers: true}} {
				_, err := s.connect(auth, legacy)
				refused, ok := errors.AsType[*LegacySignatureRefusedError](err)
				if !ok || refused.Property != LegacyUserKeySignaturesProperty || refused.Algorithm != ssh.KeyAlgoRSA || refused.Key != "RSA key" {
					t.Fatalf("record %+v: %v, want a refusal naming %s", legacy, err, LegacyUserKeySignaturesProperty)
				}
				// It says what is known (no SHA-2 advertised), never that the
				// server takes the key only with ssh-rsa, warns that a removed
				// advertisement looks the same, and says a Runner needs a
				// Controller that passes the property on.
				for _, want := range []string{"did not advertise a SHA-2 signature for this RSA key", "ssh-rsa (SHA-1)", "removed in transit", "set " + LegacyUserKeySignaturesProperty + ": true", "upgrade Controllers before Runners"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not say %q: %v", want, err)
					}
				}
				if strings.Contains(err.Error(), "only with an ssh-rsa") {
					t.Errorf("the refusal claims the server takes the key only with ssh-rsa, which is not known: %v", err)
				}
				dialErr, ok := errors.AsType[*DialError](err)
				if !ok || !dialErr.Refused() || dialErr.Unreached() {
					t.Errorf("a refused signature reads as unreached (%v): a wait would keep trying it", err)
				}
			}
			if s.srv.KeyLogins() != 0 {
				t.Fatalf("the server verified %d key logins while the record allowed no SHA-1 signature", s.srv.KeyLogins())
			}
			for i := 0; i < 2*breaker.DefaultThreshold; i++ {
				if _, err := s.connect(auth, Legacy{}); errors.Is(err, breaker.ErrOpen) {
					t.Fatalf("refusal %d opened the circuit: %v", i, err)
				}
			}

			conn, err := s.connect(auth, Legacy{UserKeySignatures: true})
			if err != nil {
				t.Fatalf("the record allows the signature: %v", err)
			}
			defer conn.Close()
			if res, err := conn.Run(context.Background(), "echo in"); err != nil || res.Stdout != "in\n" {
				t.Fatalf("a command after the SHA-1 login: %+v, %v", res, err)
			}
			if got := s.srv.KeySignatures(); !slices.Equal(got, []string{ssh.KeyAlgoRSA}) {
				t.Errorf("the server verified %v, want one ssh-rsa login", got)
			}
			warnings := conn.LegacyWarnings("sw-access-07")
			if len(warnings) != 1 || !strings.Contains(warnings[0], "used legacy SSH user key signature ssh-rsa ("+LegacyUserKeySignaturesProperty+")") {
				t.Errorf("warnings %q, want one naming the ssh-rsa signature", warnings)
			}
		})
	}
}

// TestKeySkippedForSHA1WarnsBesidePassword proves a key and password
// credential on a server that takes the key only with ssh-rsa logs in with
// the password, with a warning that the key was not used, on a record that
// allows nothing; and that a refused password then names the key's refusal
// too, so the operator learns both.
func TestKeySkippedForSHA1WarnsBesidePassword(t *testing.T) {
	keyPEM, pub := rsaKeyPEM(t, "")
	s := startKeyServer(t, pub, []string{ssh.KeyAlgoRSA}, false)

	auth, err := AuthFrom(s.srv.Username, s.srv.Password, keyPEM, "")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := s.connect(auth, Legacy{})
	if err != nil {
		t.Fatalf("the password should log in: %v", err)
	}
	defer conn.Close()
	if s.srv.Logins() != 1 || s.srv.KeyLogins() != 0 {
		t.Errorf("%d logins, %d by key; want one by password", s.srv.Logins(), s.srv.KeyLogins())
	}
	warnings := conn.LegacyWarnings("sw-access-07")
	if len(warnings) != 1 || !strings.Contains(warnings[0], `device "sw-access-07" did not use the stored RSA key`) ||
		!strings.Contains(warnings[0], "did not advertise a SHA-2 signature") ||
		!strings.Contains(warnings[0], LegacyUserKeySignaturesProperty) || !strings.Contains(warnings[0], "logged in with the password") {
		t.Errorf("warnings %q, want one saying the RSA key was not used", warnings)
	}

	wrong, err := AuthFrom(s.srv.Username, "wrong", keyPEM, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.connect(wrong, Legacy{})
	if _, ok := errors.AsType[*LegacySignatureRefusedError](err); !ok || !strings.Contains(err.Error(), authRejectedText) {
		t.Fatalf("a refused password beside a refused key: %v, want both named", err)
	}
	if dialErr, ok := errors.AsType[*DialError](err); !ok || !dialErr.Refused() {
		t.Errorf("a refused login reads as unreached: %v", err)
	}
}

// TestDSAUserKeyRefusedWithoutProperty proves a DSA key, which signs only
// with ssh-dss, is refused unless the record sets the property, and logs in
// with it, warned about, once it does.
func TestDSAUserKeyRefusedWithoutProperty(t *testing.T) {
	keyPEM, pub := dsaKeyPEM(t)
	s := startKeyServer(t, pub, []string{ssh.InsecureKeyAlgoDSA}, true)
	auth := keyOnly(t, s.srv.Username, keyPEM, "")

	_, err := s.connect(auth, Legacy{})
	if refused, ok := errors.AsType[*LegacySignatureRefusedError](err); !ok || refused.Algorithm != ssh.InsecureKeyAlgoDSA || refused.Key != "DSA key" || refused.Property == "" {
		t.Fatalf("a DSA key on a record allowing nothing: %v, want a refusal naming the property", err)
	}
	// A DSA key has no SHA-2 signature, so there is no advertisement that
	// could have been removed: the refusal says what the key is instead.
	if !strings.Contains(err.Error(), "which signs only with an ssh-dss (DSA) signature") || strings.Contains(err.Error(), "removed in transit") {
		t.Errorf("a DSA refusal says %v", err)
	}
	conn, err := s.connect(auth, Legacy{UserKeySignatures: true})
	if err != nil {
		t.Fatalf("the record allows the signature: %v", err)
	}
	defer conn.Close()
	if got := s.srv.KeySignatures(); !slices.Equal(got, []string{ssh.InsecureKeyAlgoDSA}) {
		t.Errorf("the server verified %v, want one ssh-dss login", got)
	}
	if w := strings.Join(conn.LegacyWarnings("old-box"), "\n"); !strings.Contains(w, "used legacy SSH user key signature "+ssh.InsecureKeyAlgoDSA) {
		t.Errorf("warnings %q do not name the ssh-dss signature", w)
	}
}
