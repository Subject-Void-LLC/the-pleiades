// A refused host key, against a real SSH server on loopback, names the
// address and port, the key presented and each key known_hosts trusts with
// its file and line, and is still the library's refusal underneath.
package remoteexec

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// TestConnect_AChangedHostKeyNamesBothKeys proves the refusal of a host whose
// key changed (a device that made a new key at boot) says exactly what
// changed: the port, the new key's fingerprint and the trusted one's, with the
// line of known_hosts that trusts it. The key stays refused.
func TestConnect_AChangedHostKeyNamesBothKeys(t *testing.T) {
	target, presented, _ := startHandshakeCountingSSHListener(t)
	old := generateTestHostKey(t).PublicKey()
	path := filepath.Join(t.TempDir(), "known_hosts")
	writeKnownHostsFor(t, path, target, old)

	_, err := New(Options{KnownHostsPath: path}).Connect(context.Background(), nil, target, PasswordAuth("u", rightPassword))
	var hostErr *HostKeyError
	if !errors.As(err, &hostErr) || !hostErr.Changed() {
		t.Fatalf("error = %v, want a changed host key", err)
	}
	var keyErr *knownhosts.KeyError
	if !errors.As(err, &keyErr) || !hostKeyRefused(err) {
		t.Errorf("the library's refusal is no longer under it: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		"port " + strconv.Itoa(target.Port) + " changed",
		"presented " + presented.Type() + " " + Fingerprint(presented),
		"trusts " + old.Type() + " " + Fingerprint(old) + " (" + path + ":1)",
		"knownhosts: key mismatch",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal %q does not say %q", msg, want)
		}
	}
	if hostErr.Port() != strconv.Itoa(target.Port) {
		t.Errorf("Port() = %s", hostErr.Port())
	}
}

// TestConnect_AnUnknownHostKeyNamesTheKeyPresented proves the refusal of a
// host known_hosts does not name gives the presented key's fingerprint, to
// check against the device before trusting it, and keeps the library's words.
func TestConnect_AnUnknownHostKeyNamesTheKeyPresented(t *testing.T) {
	target, presented, _ := startHandshakeCountingSSHListener(t)
	path := filepath.Join(t.TempDir(), "known_hosts")
	writeKnownHostsFor(t, path, target, nil)

	_, err := New(Options{KnownHostsPath: path}).Connect(context.Background(), nil, target, PasswordAuth("u", rightPassword))
	var hostErr *HostKeyError
	if !errors.As(err, &hostErr) || hostErr.Changed() {
		t.Fatalf("error = %v, want an unknown host key", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "is not in known_hosts: it presented "+presented.Type()+" "+Fingerprint(presented)) || !strings.Contains(msg, "key is unknown") {
		t.Errorf("the refusal %q does not name the key presented", msg)
	}
}

// TestDescribeHostKeyRefusal_ChangesOnlyTheWords proves the wrapper accepts
// what the callback accepts, passes on an error it does not know, describes a
// revoked key, and leaves a nil callback nil.
func TestDescribeHostKeyRefusal_ChangesOnlyTheWords(t *testing.T) {
	key := generateTestHostKey(t).PublicKey()
	if describeHostKeyRefusal(nil) != nil {
		t.Error("a nil callback was wrapped")
	}
	accept := describeHostKeyRefusal(func(string, net.Addr, ssh.PublicKey) error { return nil })
	if err := accept("h:22", nil, key); err != nil {
		t.Errorf("an accepted key was refused: %v", err)
	}
	other := errors.New("some other failure")
	if err := describeHostKeyRefusal(func(string, net.Addr, ssh.PublicKey) error { return other })("h:22", nil, key); err != other {
		t.Errorf("an unknown error was rewritten: %v", err)
	}
	revoked := describeHostKeyRefusal(func(string, net.Addr, ssh.PublicKey) error {
		return &knownhosts.RevokedError{Revoked: knownhosts.KnownKey{Key: key, Filename: "kh", Line: 3}}
	})("h:2222", nil, key)
	var hostErr *HostKeyError
	if !errors.As(revoked, &hostErr) || !hostErr.Revoked || hostErr.Changed() || !strings.Contains(revoked.Error(), "h port 2222") || !strings.Contains(revoked.Error(), "is revoked") {
		t.Errorf("a revoked key: %v", revoked)
	}
	if !hostKeyRefused(revoked) {
		t.Error("a revoked key no longer reads as refused")
	}
	if (&HostKeyError{Addr: "no-port"}).Port() != "22" {
		t.Error("an address with no port is not port 22")
	}
}
