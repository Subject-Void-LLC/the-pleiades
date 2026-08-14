package credential_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// The error paths of the file-backed store.
//
// These went untested until Phase 22 moved the masking algorithm out of
// this package and the coverage ratchet noticed that what remained was
// less covered than the package as a whole had been. The right response
// was to test the remainder rather than lower the floor: this package is
// secrets at rest, and its error paths are the ones that decide whether a
// failed write leaves a half-written credentials file behind or a corrupt
// entry reads back as an empty password.
//
// Every case below drives the real filesystem, because every one of them
// is about what the real filesystem does. A mocked os would assert this
// package calls the functions it already visibly calls.

// writableKey is a valid AES-256 key. The value does not matter; the
// length does.
var writableKey = []byte("0123456789abcdef0123456789abcdef")

// TestSaveFileStore_WrongLengthKeyIsExplicitError covers the constructor
// failure inside Save. NewFileStore has its own test for this; Save builds
// its own service and had no equivalent, so a bad key reached a different
// line with a different message.
func TestSaveFileStore_WrongLengthKeyIsExplicitError(t *testing.T) {
	t.Parallel()

	err := credential.SaveFileStore(t.TempDir(), []byte("too short"), "router-1", credential.Credential{
		Username: "admin", Password: "hunter2",
	})
	if err == nil {
		t.Fatal("SaveFileStore() accepted a key of the wrong length")
	}
	if !strings.Contains(err.Error(), "encryption") {
		t.Errorf("error does not name the encryption setup: %v", err)
	}
}

// TestSaveFileStore_ReportsAFailedRename covers the last step of the
// atomic write, and the assertion that matters is the second one: a Save
// that cannot complete must leave no temp file behind in the credentials
// directory. That directory holds secrets, and a stray .tmp-* file from a
// failed write is a secret nobody knows is there.
//
// A directory sitting where credentials.yaml belongs is what makes the
// rename fail. That is not a contrived shape: it is what a container
// orchestrator produces when a volume mount names a path that does not
// exist yet.
//
// An earlier version of this test removed the write permission on the
// directory instead. That was wrong twice over. SaveFileStore chmods the
// directory back to 0700 before writing, so an owned-but-unwritable
// directory is repaired rather than refused, and the permission bit is
// ignored entirely when the tests run as root, so the case silently
// skipped rather than failing.
func TestSaveFileStore_ReportsAFailedRename(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pleiades := filepath.Join(dir, ".pleiades")
	if err := os.MkdirAll(filepath.Join(pleiades, "credentials.yaml"), 0o700); err != nil {
		t.Fatalf("MkdirAll() failed: %v", err)
	}

	err := credential.SaveFileStore(dir, writableKey, "router-1", credential.Credential{
		Username: "admin", Password: "hunter2",
	})
	if err == nil {
		t.Fatal("SaveFileStore() succeeded with a directory where the credentials file belongs")
	}

	entries, readErr := os.ReadDir(pleiades)
	if readErr != nil {
		t.Fatalf("ReadDir() failed: %v", readErr)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("a failed save left a temp file behind in the credentials directory: %s", e.Name())
		}
	}
}

// TestFileStore_CorruptCiphertextIsAnErrorPerField covers decryption
// failure for each of the three encrypted fields independently.
//
// Per field matters. The three branches are separate code, and the failure
// this guards against is the one where a corrupt field is skipped rather
// than reported: a Lookup that returned a Credential with an empty
// password would authenticate against nothing, and the resulting failure
// would be attributed to the device rather than to the store.
func TestFileStore_CorruptCiphertextIsAnErrorPerField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cred  credential.Credential
		field string
	}{
		{
			name:  "password",
			cred:  credential.Credential{Username: "admin", Password: "hunter2"},
			field: "password_encrypted",
		},
		{
			name:  "private key",
			cred:  credential.Credential{Username: "admin", PrivateKeyPEM: []byte("PRIVATE KEY BODY")},
			field: "private_key_encrypted",
		},
		{
			name:  "passphrase",
			cred:  credential.Credential{Username: "admin", PrivateKeyPEM: []byte("KEY"), Passphrase: "unlock-me"},
			field: "passphrase_encrypted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if err := credential.SaveFileStore(dir, writableKey, "router-1", tt.cred); err != nil {
				t.Fatalf("SaveFileStore() failed: %v", err)
			}

			path := filepath.Join(dir, ".pleiades", "credentials.yaml")
			data, err := os.ReadFile(path) //  #nosec G304 -- a path this test just created inside its own TempDir
			if err != nil {
				t.Fatalf("ReadFile() failed: %v", err)
			}

			corrupted := corruptYAMLField(t, string(data), tt.field)
			if err := os.WriteFile(path, []byte(corrupted), 0o600); err != nil {
				t.Fatalf("WriteFile() failed: %v", err)
			}

			store, err := credential.NewFileStore(dir, writableKey)
			if err != nil {
				t.Fatalf("NewFileStore() failed: %v", err)
			}

			got, err := store.Lookup(context.Background(), "router-1")
			if err == nil {
				t.Fatalf("Lookup() succeeded against corrupt %s and returned %v", tt.field, got)
			}
			if !strings.Contains(err.Error(), "router-1") {
				t.Errorf("the error does not name the device: %v", err)
			}
		})
	}
}

// corruptYAMLField replaces the value of the named field with something
// that is still valid base64-ish text but decrypts to nothing.
func corruptYAMLField(t *testing.T, doc, field string) string {
	t.Helper()

	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, field+":") {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
		lines[i] = indent + field + ": bm90LWEtcmVhbC1jaXBoZXJ0ZXh0"
		return strings.Join(lines, "\n")
	}

	t.Fatalf("field %q not found in the credentials document:\n%s", field, doc)
	return ""
}

// TestFileStore_MalformedYAMLIsAnExplicitError covers the read path's own
// parse failure, which sits above the per-field decryption above.
func TestFileStore_MalformedYAMLIsAnExplicitError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pleiades := filepath.Join(dir, ".pleiades")
	if err := os.MkdirAll(pleiades, 0o700); err != nil {
		t.Fatalf("MkdirAll() failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pleiades, "credentials.yaml"), []byte("devices: [not a map"), 0o600); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	store, err := credential.NewFileStore(dir, writableKey)
	if err != nil {
		// Construction reading the file eagerly is also an acceptable
		// place to notice, so this is not a failure on its own.
		return
	}
	if _, err := store.Lookup(context.Background(), "router-1"); err == nil {
		t.Fatal("Lookup() succeeded against a malformed credentials file")
	}
}

// TestSaveFileStore_LeavesNoTempFileOnSuccess is the positive counterpart
// to the unwritable-directory case. The temp file is an implementation
// detail of the atomic write, and it must not survive a successful one
// either.
func TestSaveFileStore_LeavesNoTempFileOnSuccess(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := credential.SaveFileStore(dir, writableKey, "router-1", credential.Credential{
		Username: "admin", Password: "hunter2",
	}); err != nil {
		t.Fatalf("SaveFileStore() failed: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, ".pleiades"))
	if err != nil {
		t.Fatalf("ReadDir() failed: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("a successful save left a temp file behind: %s", e.Name())
		}
	}
}

// TestSaveFileStore_WritesTheFileOwnerReadableOnly pins the permission the
// whole package exists to guarantee. It is asserted here rather than
// trusted from the atomic writer's own Chmod call, because the mode a file
// ends up with is the product of that call and the process umask.
func TestSaveFileStore_WritesTheFileOwnerReadableOnly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := credential.SaveFileStore(dir, writableKey, "router-1", credential.Credential{
		Username: "admin", Password: "hunter2",
	}); err != nil {
		t.Fatalf("SaveFileStore() failed: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, ".pleiades", "credentials.yaml"))
	if err != nil {
		t.Fatalf("Stat() failed: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("credentials.yaml has mode %o, want 600: any other local user can read it", perm)
	}
}
