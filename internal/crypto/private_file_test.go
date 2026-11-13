// Tests that a key file another account could read, change or owns is
// refused before it is used, naming the command that fixes it.
package crypto_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// writeKey writes a valid key file under dir with mode perm, and returns its
// path. The mode is set after writing, since the umask narrows a create.
func writeKey(t *testing.T, dir string, perm os.FileMode) string {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".pleiades", "master.key")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(crypto.EncodeKey(key)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestResolveKey_RefusesALooseKeyFile: a key readable by others (0644) or by
// the group (0640) is refused with ErrLooseSecretFile, naming the file and
// chmod 600; a 0600 key is read.
func TestResolveKey_RefusesALooseKeyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes do not apply on Windows, where the check checks nothing by design")
	}
	t.Setenv("PLEIADES_TEST_KEY", "")
	os.Unsetenv("PLEIADES_TEST_KEY")
	for _, perm := range []os.FileMode{0o644, 0o640, 0o620, 0o602} {
		dir := t.TempDir()
		path := writeKey(t, dir, perm)
		_, err := crypto.ResolveKey(dir, "PLEIADES_TEST_KEY", filepath.Join(".pleiades", "master.key"))
		if !errors.Is(err, crypto.ErrLooseSecretFile) || !strings.Contains(err.Error(), "chmod 600 "+path) {
			t.Errorf("mode %04o: ResolveKey = %v, want a refusal naming chmod 600 %s", perm, err, path)
		}
		// Refused, never replaced: the loose key is still there, unchanged in mode.
		if info, statErr := os.Stat(path); statErr != nil || info.Mode().Perm() != perm {
			t.Errorf("mode %04o: the refused key was changed: %v %v", perm, info, statErr)
		}
	}
	dir := t.TempDir()
	writeKey(t, dir, 0o600)
	if key, err := crypto.ResolveKey(dir, "PLEIADES_TEST_KEY", filepath.Join(".pleiades", "master.key")); err != nil || len(key) != 32 {
		t.Errorf("a 0600 key: %v, %d bytes", err, len(key))
	}
}

// TestReadPrivateFile_RefusesAFileAnotherUserOwns: a 0600 file owned by
// another user is refused naming chown. Only root can give a file away, so
// the case needs root to arrange.
func TestReadPrivateFile_RefusesAFileAnotherUserOwns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX ownership does not apply on Windows, where the check checks nothing by design")
	}
	if os.Geteuid() != 0 {
		t.Skip("giving a file to another user needs root, and this test runs as a non-root user")
	}
	path := writeKey(t, t.TempDir(), 0o600)
	if err := os.Chown(path, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if _, err := crypto.ReadPrivateFile(path); !errors.Is(err, crypto.ErrLooseSecretFile) || !strings.Contains(err.Error(), "chown 0 "+path) {
		t.Errorf("ReadPrivateFile = %v, want a refusal naming chown", err)
	}
}

// TestReadPrivateFile_MissingFileIsNotExist: a file that is not there is
// reported as not existing, so a caller can still make one.
func TestReadPrivateFile_MissingFileIsNotExist(t *testing.T) {
	if _, err := crypto.ReadPrivateFile(filepath.Join(t.TempDir(), "gone")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadPrivateFile on a missing file = %v, want not-exist", err)
	}
}
