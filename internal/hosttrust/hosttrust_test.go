// Tests for Merge against real known_hosts files, checked with the same
// knownhosts package every SSH connection verifies through, and for Scan
// against the in-process SSH server.
package hosttrust

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// newKey returns a fresh ed25519 public key.
func newKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// verifies reports whether the file at path admits key for address, as
// a connection's host key check would.
func verifies(t *testing.T, path, address string, key ssh.PublicKey) bool {
	t.Helper()
	callback, err := knownhosts.New(path)
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(withPort(address))
	return callback(net.JoinHostPort(host, port), &net.TCPAddr{IP: net.ParseIP(host)}, key) == nil
}

func TestMerge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh", "known_hosts")
	a, b := newKey(t), newKey(t)
	out, err := Merge(path, "192.168.56.10", []ssh.PublicKey{a, b}, false)
	if err != nil || out.Added != 2 {
		t.Fatalf("%+v, %v", out, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("a new file has mode %v", info.Mode().Perm())
	}
	if !verifies(t, path, "192.168.56.10", a) || !verifies(t, path, "192.168.56.10", b) {
		t.Fatal("a merged key does not verify")
	}
	if out, err := Merge(path, "192.168.56.10", []ssh.PublicKey{a}, false); err != nil || out.Added != 0 || out.Known != 1 {
		t.Errorf("merging a trusted key again: %+v, %v", out, err)
	}

	// Another host, and a port, beside it.
	c := newKey(t)
	if _, err := Merge(path, "192.168.56.11:2222", []ssh.PublicKey{c}, false); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "[192.168.56.11]:2222 ") || !verifies(t, path, "192.168.56.11:2222", c) {
		t.Errorf("a host on a port:\n%s", content)
	}

	// The VM made again, with new keys.
	fresh := newKey(t)
	if _, err := Merge(path, "192.168.56.10", []ssh.PublicKey{fresh}, false); err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("new keys for a trusted host: %v", err)
	}
	if verifies(t, path, "192.168.56.10", fresh) {
		t.Fatal("a refused key was written")
	}
	out, err = Merge(path, "192.168.56.10", []ssh.PublicKey{fresh}, true)
	if err != nil || out.Removed != 2 || out.Added != 1 {
		t.Fatalf("--replace: %+v, %v", out, err)
	}
	if verifies(t, path, "192.168.56.10", a) || !verifies(t, path, "192.168.56.10", fresh) || !verifies(t, path, "192.168.56.11:2222", c) {
		t.Error("--replace kept the old key, dropped the new one, or touched another host")
	}
}

func TestMerge_KeepsWhatItDoesNotOwn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	other := newKey(t)
	existing := "# a comment\n" + knownhosts.Line([]string{"example.com"}, other) // no trailing newline
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(path, "192.168.56.10", []ssh.PublicKey{newKey(t)}, false); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(content), existing+"\n") || !verifies(t, path, "example.com", other) {
		t.Errorf("the file's other lines changed:\n%s", content)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o644 {
		t.Errorf("the file's mode changed to %v", info.Mode().Perm())
	}
	if _, err := Merge(path, "x", nil, false); err == nil {
		t.Error("merged no keys")
	}
	if _, err := Merge(t.TempDir(), "x", []ssh.PublicKey{other}, false); err == nil {
		t.Error("merged into a folder")
	}
	if names("@cert-authority *.lab ssh-ed25519 AAAA", "*.lab") != true || names("# 1.2.3.4 ssh-ed25519 AAAA", "1.2.3.4") {
		t.Error("names read a marker or a comment wrongly")
	}
}

func TestScan(t *testing.T) {
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	address := net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))
	keys, err := Scan(context.Background(), address, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || string(keys[0].Marshal()) != string(srv.HostKey.Marshal()) {
		t.Errorf("scanned %d keys, not the server's", len(keys))
	}
	if srv.Logins() != 0 {
		t.Error("the scan logged in")
	}
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := listener.Addr().String()
	listener.Close()
	if _, err := Scan(context.Background(), closed, time.Second); err == nil {
		t.Error("a closed port gave keys")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, address, time.Second); err == nil {
		t.Error("a cancelled scan gave keys")
	}
}

func TestMerge_Failures(t *testing.T) {
	dir := t.TempDir()
	key := newKey(t)
	broken := filepath.Join(dir, "broken")
	if err := os.WriteFile(broken, []byte("192.168.56.10 ssh-ed25519 not-base64!\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(broken, "192.168.56.10", []ssh.PublicKey{key}, false); err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Errorf("a file that does not parse: %v", err)
	}
	if content, _ := os.ReadFile(broken); strings.Count(string(content), "\n") != 1 {
		t.Error("a file that does not parse was added to")
	}
	good := filepath.Join(dir, "good")
	if _, err := Merge(good, "192.168.56.10", []ssh.PublicKey{key}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(good, "[::1", []ssh.PublicKey{newKey(t)}, false); err == nil {
		t.Error("an address that is no address was trusted")
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(filepath.Join(file, "known_hosts"), "x", []ssh.PublicKey{key}, false); err == nil {
		t.Error("wrote under a file")
	}
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if os.Getuid() != 0 {
		if _, err := Merge(filepath.Join(locked, "known_hosts"), "x", []ssh.PublicKey{key}, false); err == nil {
			t.Error("wrote into a folder it may not write")
		}
	}
}
