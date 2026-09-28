package crypto_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

const (
	testEnvVar   = "PLEIADES_TEST_RESOLVE_KEY"
	testFileName = ".pleiades-test/resolve.key"
)

// writeKeyFile writes raw directly as the key file's content, bypassing
// ResolveKey's own write path entirely, so a test can set up a specific
// starting state (valid or deliberately malformed) before calling
// ResolveKey.
func writeKeyFile(t *testing.T, dir string, raw []byte) {
	t.Helper()
	keyPath := filepath.Join(dir, testFileName)
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("failed to create %s: %v", filepath.Dir(keyPath), err)
	}
	if err := os.WriteFile(keyPath, raw, 0o600); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}
}

// TestResolveKey_EnvVarTakesPrecedence proves the environment variable
// wins even when a different, validly-formed key already exists on disk:
// an operator who deliberately set the env var must get exactly that key,
// never a silently different one from the file.
func TestResolveKey_EnvVarTakesPrecedence(t *testing.T) {
	dir := t.TempDir()

	fileKey := strings.Repeat("f", 32)
	writeKeyFile(t, dir, []byte(base64.StdEncoding.EncodeToString([]byte(fileKey))))

	envKey := strings.Repeat("e", 32)
	t.Setenv(testEnvVar, base64.StdEncoding.EncodeToString([]byte(envKey)))

	got, err := crypto.ResolveKey(dir, testEnvVar, testFileName)
	if err != nil {
		t.Fatalf("ResolveKey() error = %v", err)
	}
	if string(got) != envKey {
		t.Fatalf("expected the env var key to win, got %q, want %q", got, envKey)
	}
}

// TestResolveKey_EnvVarMalformedIsExplicitError proves a malformed env var
// is a hard error, not a silent fallback to file-based resolution: an
// operator who deliberately set a bad value should learn about it loudly,
// not have The Pleiades quietly use a different key.
func TestResolveKey_EnvVarMalformedIsExplicitError(t *testing.T) {
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
			t.Setenv(testEnvVar, tt.val)
			_, err := crypto.ResolveKey(dir, testEnvVar, testFileName)
			if err == nil {
				t.Fatal("expected an explicit error for a malformed env var, got nil")
			}
		})
	}
}

// TestResolveKey_GenerateThenReuse proves calling ResolveKey twice against
// the same dir with no env var set returns the same key both times: the
// key is created once and reused, never regenerated on a later call, which
// would silently strand anything already encrypted under the first key.
func TestResolveKey_GenerateThenReuse(t *testing.T) {
	dir := t.TempDir()

	first, err := crypto.ResolveKey(dir, testEnvVar, testFileName)
	if err != nil {
		t.Fatalf("first ResolveKey() error = %v", err)
	}
	if len(first) != 32 {
		t.Fatalf("expected a 32-byte key, got %d bytes", len(first))
	}

	keyPath := filepath.Join(dir, testFileName)
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("expected key file to be created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %o, want 0600", perm)
	}

	second, err := crypto.ResolveKey(dir, testEnvVar, testFileName)
	if err != nil {
		t.Fatalf("second ResolveKey() error = %v", err)
	}

	if string(first) != string(second) {
		t.Fatal("expected the same key on a second call, got a different one (key was regenerated)")
	}
}

// TestResolveKey_TightensPreexistingDirPermissions is a regression test
// for FAILURE_PATTERNS.md #22: os.MkdirAll only applies its mode argument
// to a directory it actually creates, so a parent directory that already
// existed with looser permissions (here, simulated as 0o777, as if
// created by a permissive umask or an unrelated process) stayed at 0o777
// after a naive MkdirAll(dir, 0o700) call, even though the key file
// written inside it was correctly 0o600. This proves ResolveKey now
// explicitly tightens the directory too, not just when it is the one
// creating it fresh.
func TestResolveKey_TightensPreexistingDirPermissions(t *testing.T) {
	dir := t.TempDir()
	keyDir := filepath.Join(dir, filepath.Dir(testFileName))
	if err := os.MkdirAll(keyDir, 0o777); err != nil {
		t.Fatalf("failed to pre-create %s: %v", keyDir, err)
	}

	if _, err := crypto.ResolveKey(dir, testEnvVar, testFileName); err != nil {
		t.Fatalf("ResolveKey() error = %v", err)
	}

	info, err := os.Stat(keyDir)
	if err != nil {
		t.Fatalf("failed to stat %s: %v", keyDir, err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("key directory mode = %o, want 0700 even though it pre-existed with looser permissions", perm)
	}
}

// TestResolveKey_ReadErrorWhenParentDirIsAFile covers ResolveKey's "real
// read error, not a missing file" branch: a regular file where the key
// file's parent directory should be makes the key path untraversable
// (ENOTDIR), distinct from the file simply not existing yet (ENOENT), and
// must not be silently treated as "generate a new key."
func TestResolveKey_ReadErrorWhenParentDirIsAFile(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, filepath.Dir(testFileName))
	if err := os.WriteFile(parent, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("failed to create test fixture: %v", err)
	}

	_, err := crypto.ResolveKey(dir, testEnvVar, testFileName)
	if err == nil {
		t.Fatal("expected a read error, got nil")
	}
}

// TestResolveKey_CorruptFileIsExplicitError proves a corrupt or
// wrong-length key file is a hard error, never silently regenerated:
// regenerating would produce a new key that cannot decrypt anything
// already stored under the old one, silently losing access to it.
func TestResolveKey_CorruptFileIsExplicitError(t *testing.T) {
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
			writeKeyFile(t, dir, tt.content)

			_, err := crypto.ResolveKey(dir, testEnvVar, testFileName)
			if err == nil {
				t.Fatal("expected an explicit error for a corrupt key file, got nil")
			}
		})
	}
}

// TestResolveKey_ConcurrentGenerationConverges is the regression test for
// a real TOCTOU race an adversarial review caught: multiple concurrent
// callers reaching ResolveKey against the same not-yet-existing directory
// each generate a random key before any of them has persisted one, and
// without the fix (key_resolve.go's O_EXCL create) only the last writer's
// key would actually survive on disk while the others silently kept using
// their own, different, unpersisted key in memory. This proves every
// concurrent caller converges on the exact same key, with none returning
// an error.
func TestResolveKey_ConcurrentGenerationConverges(t *testing.T) {
	dir := t.TempDir()
	const goroutines = 16

	var (
		start sync.WaitGroup
		wg    sync.WaitGroup
		mu    sync.Mutex
	)
	start.Add(1)
	keys := make([][]byte, 0, goroutines)
	errs := make([]error, 0, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start.Wait() // align every goroutine to start as close together as possible
			key, err := crypto.ResolveKey(dir, testEnvVar, testFileName)
			mu.Lock()
			defer mu.Unlock()
			keys = append(keys, key)
			errs = append(errs, err)
		}()
	}
	start.Done()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: ResolveKey() error = %v", i, err)
		}
	}
	first := string(keys[0])
	for i, key := range keys {
		if string(key) != first {
			t.Fatalf("goroutine %d returned a different key than goroutine 0: concurrent generation did not converge on one persisted key", i)
		}
	}
}
