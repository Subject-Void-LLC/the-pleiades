package remoteexec

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzHostKeyCallbackConstruction feeds arbitrary bytes as a
// known_hosts file's contents and asserts hostKeyCallback never panics
// regardless of file content: it always either returns a usable
// callback or a clean error. No live socket is involved in any
// iteration, per this repository's established fuzzing convention
// (internal/engine/dag_fuzz_test.go).
func FuzzHostKeyCallbackConstruction(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("\n"))
	f.Add([]byte("# just a comment\n"))
	f.Add([]byte("example.test ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIn496QwycpRQbqz6nbcx+kCaTZWjrpnTrhQK5OeUXQy\n"))
	f.Add([]byte("malformed line with no key\n"))
	f.Add([]byte("|1|abcXYZ==|def==+garbage== ssh-rsa AAAA\n"))
	f.Add([]byte("@revoked example.test ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIn496QwycpRQbqz6nbcx+kCaTZWjrpnTrhQK5OeUXQy\n"))
	f.Add([]byte{0x00, 0xff, 0x01, 0xfe, '\n', 0x7f})

	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "known_hosts")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("failed to write fuzz input to a temp known_hosts file: %v", err)
		}

		// The assertion here is solely "does not panic"; both a
		// successfully constructed callback and a clean error are valid
		// outcomes for arbitrary file content.
		cb, err := hostKeyCallback(Options{KnownHostsPath: path})
		if err == nil && cb == nil {
			t.Fatal("hostKeyCallback returned neither a callback nor an error")
		}
	})
}

// FuzzAuthFrom feeds arbitrary bytes as a private key and passphrase,
// asserting AuthFrom never panics regardless of content: parsing an
// arbitrary byte slice as a private key must always end in either a
// usable Auth or a clean, wrapped error.
func FuzzAuthFrom(f *testing.F) {
	f.Add([]byte(""), "")
	f.Add([]byte("not a pem file at all"), "")
	f.Add([]byte("-----BEGIN OPENSSH PRIVATE KEY-----\ngarbage\n-----END OPENSSH PRIVATE KEY-----\n"), "")
	f.Add([]byte("-----BEGIN OPENSSH PRIVATE KEY-----\ngarbage\n-----END OPENSSH PRIVATE KEY-----\n"), "some-passphrase")
	f.Add([]byte{0x00, 0x01, 0x02, 0x03}, "\x00")

	f.Fuzz(func(t *testing.T, keyBytes []byte, passphrase string) {
		// No password is supplied, so AuthFrom is forced down its private
		// key branch for every input rather than short-circuiting.
		auth, err := AuthFrom("u", "", keyBytes, passphrase)
		if err == nil && !auth.usable() {
			t.Fatal("AuthFrom returned neither a usable Auth nor an error")
		}
		if err != nil && auth.usable() {
			t.Fatal("AuthFrom returned a usable Auth alongside an error, which would let an unauthenticated dial proceed")
		}
	})
}
