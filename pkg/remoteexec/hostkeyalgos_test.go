// A known_hosts file that pins one host key type, against a real SSH server
// on loopback that holds several: the connection offers the pinned type first
// and succeeds, and a key that really changed is still refused as one.
package remoteexec

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// hostKeySigners returns one real host key of each type a stock sshd makes,
// keyed by the type's name.
func hostKeySigners(t *testing.T) map[string]ssh.Signer {
	t.Helper()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]ssh.Signer{ssh.KeyAlgoED25519: generateTestHostKey(t)}
	for _, key := range []any{rsaKey, ecKey} {
		signer, err := ssh.NewSignerFromKey(key)
		if err != nil {
			t.Fatal(err)
		}
		out[signer.PublicKey().Type()] = signer
	}
	return out
}

// startMultiKeySSHListener starts a real SSH server on loopback that presents
// every one of signers, as an sshd with several host keys does, and accepts
// rightPassword.
func startMultiKeySSHListener(t *testing.T, signers ...ssh.Signer) Target {
	t.Helper()
	config := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) == rightPassword {
				return nil, nil
			}
			return nil, errors.New("password refused")
		},
	}
	for _, s := range signers {
		config.AddHostKey(s)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open a loopback listener: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveOneFakeConnection(conn, config, func(string) (string, string, int) { return "ok\n", "", 0 })
		}
	}()
	addr := listener.Addr().(*net.TCPAddr)
	return Target{Host: addr.IP.String(), Port: addr.Port}
}

// TestConnect_APinnedKeyOfAnyOneTypeIsAccepted proves a known_hosts file
// holding only one of a server's host keys verifies it, whichever type that
// key is. Before this, the client offered the library's default order, the
// server answered with its ECDSA key, and a file pinning only the ed25519 or
// RSA key refused the connection as a changed key (measured 2026-09-25
// against a lab VM).
func TestConnect_APinnedKeyOfAnyOneTypeIsAccepted(t *testing.T) {
	signers := hostKeySigners(t)
	all := make([]ssh.Signer, 0, len(signers))
	for _, s := range signers {
		all = append(all, s)
	}
	target := startMultiKeySSHListener(t, all...)

	for keyType, signer := range signers {
		t.Run(keyType, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "known_hosts")
			writeKnownHostsFor(t, path, target, signer.PublicKey())
			conn, err := New(Options{KnownHostsPath: path}).Connect(context.Background(), nil, target, PasswordAuth("u", rightPassword))
			if err != nil {
				t.Fatalf("connecting with only the %s key pinned: %v", keyType, err)
			}
			defer conn.Close()
			res, err := conn.Run(context.Background(), "true")
			if err != nil || res.Stdout != "ok\n" {
				t.Fatalf("running a command after the handshake: %+v, %v", res, err)
			}
		})
	}
}

// TestConnect_AChangedKeyOfAnotherTypeIsStillAKeyError proves the order is a
// preference, not a restriction: a server that no longer has the pinned type
// presents another, and the refusal is a host key refusal (which is neither
// retried nor counted against the circuit), not a handshake that found no
// common algorithm.
func TestConnect_AChangedKeyOfAnotherTypeIsStillAKeyError(t *testing.T) {
	signers := hostKeySigners(t)
	target := startMultiKeySSHListener(t, signers[ssh.KeyAlgoECDSA256])
	path := filepath.Join(t.TempDir(), "known_hosts")
	writeKnownHostsFor(t, path, target, signers[ssh.KeyAlgoED25519].PublicKey())

	_, err := New(Options{KnownHostsPath: path}).Connect(context.Background(), nil, target, PasswordAuth("u", rightPassword))
	var keyErr *knownhosts.KeyError
	if !errors.As(err, &keyErr) {
		t.Fatalf("error = %v, want a host key refusal naming the pinned key", err)
	}
	if !hostKeyRefused(err) {
		t.Errorf("hostKeyRefused(%v) = false, want true", err)
	}
}

// TestHostKeyAlgorithms_OrdersTheKnownTypesFirst proves the order itself: the
// algorithms of the keys known_hosts holds for the host come first, RSA's
// SHA-2 forms ahead of SHA-1, then every other default algorithm once, so a
// server with only an old key type is still reached.
func TestHostKeyAlgorithms_OrdersTheKnownTypesFirst(t *testing.T) {
	signers := hostKeySigners(t)
	hostPort := "router.test:22"
	path := filepath.Join(t.TempDir(), "known_hosts")
	body := knownhosts.Line([]string{knownhosts.Normalize(hostPort)}, signers[ssh.KeyAlgoRSA].PublicKey()) + "\n" +
		// A hashed entry, as ssh-keyscan -H writes: matched by the library's
		// own rules, never by this package reading names.
		knownhosts.Line([]string{knownhosts.HashHostname(knownhosts.Normalize(hostPort))}, signers[ssh.KeyAlgoED25519].PublicKey()) + "\n" +
		// Another host's ECDSA key must not reach this host's order.
		knownhosts.Line([]string{"other.test"}, signers[ssh.KeyAlgoECDSA256].PublicKey()) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cb, err := hostKeyCallback(Options{KnownHostsPath: path})
	if err != nil {
		t.Fatal(err)
	}
	got := hostKeyAlgorithms(cb, hostPort)
	want := []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA, ssh.KeyAlgoED25519}
	if len(got) < len(want) || !slices.Equal(got[:len(want)], want) {
		t.Fatalf("order = %v, want it to start %v", got, want)
	}
	for _, algo := range defaultHostKeyAlgorithms() {
		if n := countOf(got, algo); n != 1 {
			t.Errorf("%s appears %d times in %v, want once", algo, n, got)
		}
	}
}

// countOf returns how many times s holds v.
func countOf(s []string, v string) int {
	n := 0
	for _, e := range s {
		if e == v {
			n++
		}
	}
	return n
}

// TestHostKeyAlgorithms_LeavesTheDefaultAlone proves each case that must not
// change the offer: verification skipped, a host known_hosts does not name, a
// host trusted through a certificate authority, and no callback at all.
func TestHostKeyAlgorithms_LeavesTheDefaultAlone(t *testing.T) {
	ed := generateTestHostKey(t).PublicKey()
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte(knownhosts.Line([]string{"known.test"}, ed)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(dir, "ca")
	caLine := "# a comment line counts in the numbering\n@cert-authority *.lab.test " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(ed))) + "\n"
	if err := os.WriteFile(ca, []byte(caLine), 0o600); err != nil {
		t.Fatal(err)
	}
	callback := func(path string, skip bool) ssh.HostKeyCallback {
		cb, err := hostKeyCallbackFor(Options{KnownHostsPath: path}, skip)
		if err != nil {
			t.Fatal(err)
		}
		return cb
	}
	cases := map[string]struct {
		cb   ssh.HostKeyCallback
		addr string
	}{
		"verification skipped": {callback(plain, true), "known.test:22"},
		"unknown host":         {callback(plain, false), "unknown.test:22"},
		"cert authority":       {callback(ca, false), "r1.lab.test:22"},
		"no callback":          {nil, "known.test:22"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := hostKeyAlgorithms(c.cb, c.addr); got != nil {
				t.Errorf("hostKeyAlgorithms = %v, want nil (the library's default)", got)
			}
		})
	}
	// The control: the same plain file does set an order for the host it names.
	if got := hostKeyAlgorithms(callback(plain, false), "known.test:22"); len(got) == 0 || got[0] != ssh.KeyAlgoED25519 {
		t.Errorf("control: hostKeyAlgorithms for a pinned ed25519 host = %v, want it first", got)
	}
}

// TestCertAuthorityLines_UnreadableFileIsEmpty proves a file that cannot be
// read again names no line, so the caller keeps the order it computed.
func TestCertAuthorityLines_UnreadableFileIsEmpty(t *testing.T) {
	if got := certAuthorityLines(filepath.Join(t.TempDir(), "gone")); len(got) != 0 {
		t.Errorf("certAuthorityLines on a missing file = %v, want empty", got)
	}
}

// TestAlgorithmsForKeyType_UnknownTypeIsNotOffered proves a key type the
// library cannot negotiate as a host key adds nothing to the offer.
func TestAlgorithmsForKeyType_UnknownTypeIsNotOffered(t *testing.T) {
	if got := algorithmsForKeyType("sk-ssh-ed25519@openssh.com"); got != nil {
		t.Errorf("algorithmsForKeyType(security key) = %v, want nil", got)
	}
	if got := algorithmsForKeyType(ssh.InsecureKeyAlgoDSA); !slices.Equal(got, []string{ssh.InsecureKeyAlgoDSA}) {
		t.Errorf("algorithmsForKeyType(DSA) = %v, want the one DSA algorithm the default still offers", got)
	}
}
