package credential_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// writeMasterKeyFile writes raw directly as the master key file's
// content, bypassing ResolveMasterKey's own write path entirely, so a
// test can set up a specific starting state (valid or deliberately
// malformed) before calling ResolveMasterKey.
func writeMasterKeyFile(t *testing.T, dir string, raw []byte) {
	t.Helper()
	pleiadesDir := filepath.Join(dir, ".pleiades")
	if err := os.MkdirAll(pleiadesDir, 0o700); err != nil {
		t.Fatalf("failed to create %s: %v", pleiadesDir, err)
	}
	if err := os.WriteFile(filepath.Join(pleiadesDir, "master.key"), raw, 0o600); err != nil {
		t.Fatalf("failed to write master key file: %v", err)
	}
}

// TestResolveMasterKey_EnvVarTakesPrecedence proves the environment
// variable wins even when a different, validly-formed key already
// exists on disk: an operator who deliberately set the env var must get
// exactly that key, never a silently different one from the file.
func TestResolveMasterKey_EnvVarTakesPrecedence(t *testing.T) {
	dir := t.TempDir()

	fileKey := strings.Repeat("f", 32)
	writeMasterKeyFile(t, dir, []byte(base64.StdEncoding.EncodeToString([]byte(fileKey))))

	envKey := strings.Repeat("e", 32)
	t.Setenv("PLEIADES_MASTER_KEY", base64.StdEncoding.EncodeToString([]byte(envKey)))

	got, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatalf("ResolveMasterKey() error = %v", err)
	}
	if string(got) != envKey {
		t.Fatalf("expected the env var key to win, got %q, want %q", got, envKey)
	}
}

// TestResolveMasterKey_EnvVarMalformedIsExplicitError proves a malformed
// env var is a hard error, not a silent fallback to file-based
// resolution: an operator who deliberately set a bad value should learn
// about it loudly, not have The Pleiades quietly use a different key.
func TestResolveMasterKey_EnvVarMalformedIsExplicitError(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name string
		val  string
	}{
		{"not base64", "not-valid-base64!!!"},
		{"wrong length after decode", base64.StdEncoding.EncodeToString([]byte("too-short"))},
		{"empty string", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PLEIADES_MASTER_KEY", tt.val)
			_, err := credential.ResolveMasterKey(dir)
			if err == nil {
				t.Fatal("expected an explicit error for a malformed env var, got nil")
			}
		})
	}
}

// TestResolveMasterKey_GenerateThenReuse proves calling ResolveMasterKey
// twice against the same dir with no env var set returns the same key
// both times: the key is created once and reused, never regenerated on
// a later call, which would silently strand every secret already
// encrypted under the first key.
func TestResolveMasterKey_GenerateThenReuse(t *testing.T) {
	dir := t.TempDir()

	first, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatalf("first ResolveMasterKey() error = %v", err)
	}
	if len(first) != 32 {
		t.Fatalf("expected a 32-byte key, got %d bytes", len(first))
	}

	keyPath := filepath.Join(dir, ".pleiades", "master.key")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("expected master key file to be created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("master key file mode = %o, want 0600", perm)
	}

	second, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatalf("second ResolveMasterKey() error = %v", err)
	}

	if string(first) != string(second) {
		t.Fatal("expected the same key on a second call, got a different one (key was regenerated)")
	}
}

// TestResolveMasterKey_TightensPreexistingDirPermissions is a regression
// test for FAILURE_PATTERNS.md #22, found by Phase W6's own
// Schema/Injection Hardening audit: os.MkdirAll only applies its mode
// argument to a directory it actually creates, so a .pleiades directory
// that already existed with looser permissions (here, simulated as
// 0o777, as if created by a permissive umask or an unrelated process)
// stayed at 0o777 after a naive MkdirAll(dir, 0o700) call, even though
// the master key file written inside it was correctly 0o600. This proves
// ResolveMasterKey now explicitly tightens the directory too, not just
// when it is the one creating it fresh.
func TestResolveMasterKey_TightensPreexistingDirPermissions(t *testing.T) {
	dir := t.TempDir()
	pleiadesDir := filepath.Join(dir, ".pleiades")
	if err := os.MkdirAll(pleiadesDir, 0o777); err != nil {
		t.Fatalf("failed to pre-create %s: %v", pleiadesDir, err)
	}

	if _, err := credential.ResolveMasterKey(dir); err != nil {
		t.Fatalf("ResolveMasterKey() error = %v", err)
	}

	info, err := os.Stat(pleiadesDir)
	if err != nil {
		t.Fatalf("failed to stat %s: %v", pleiadesDir, err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf(".pleiades directory mode = %o, want 0700 even though it pre-existed with looser permissions", perm)
	}
}

// TestResolveMasterKey_ReadErrorWhenPleiadesDirIsAFile covers
// ResolveMasterKey's "real read error, not a missing file" branch: a
// regular file where the .pleiades directory should be makes
// dir/.pleiades/master.key untraversable (ENOTDIR), distinct from the
// file simply not existing yet (ENOENT), and must not be silently
// treated as "generate a new key."
func TestResolveMasterKey_ReadErrorWhenPleiadesDirIsAFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".pleiades"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("failed to create test fixture: %v", err)
	}

	_, err := credential.ResolveMasterKey(dir)
	if err == nil {
		t.Fatal("expected a read error, got nil")
	}
}

// TestResolveMasterKey_CorruptFileIsExplicitError proves a corrupt or
// wrong-length key file is a hard error, never silently regenerated:
// regenerating would produce a new key that cannot decrypt secrets
// already stored under the old one, silently losing access to them.
func TestResolveMasterKey_CorruptFileIsExplicitError(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
	}{
		{"not base64", []byte("not-valid-base64!!!")},
		{"wrong length after decode", []byte(base64.StdEncoding.EncodeToString([]byte("too-short")))},
		{"empty file", []byte("")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeMasterKeyFile(t, dir, tt.content)

			_, err := credential.ResolveMasterKey(dir)
			if err == nil {
				t.Fatal("expected an explicit error for a corrupt key file, got nil")
			}
		})
	}
}
