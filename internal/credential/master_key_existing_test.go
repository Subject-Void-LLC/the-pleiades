// Tests of ExistingMasterKey, the resolver that never makes a key.
package credential_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// TestExistingMasterKey_NeverMakesOne: a project with no key answers
// ErrNoMasterKey and gains no key file; once the project has a key, the same
// one is returned; and the environment's key wins, as it does for
// ResolveMasterKey.
func TestExistingMasterKey_NeverMakesOne(t *testing.T) {
	if _, set := os.LookupEnv("PLEIADES_MASTER_KEY"); set {
		t.Setenv("PLEIADES_MASTER_KEY", "")
		_ = os.Unsetenv("PLEIADES_MASTER_KEY")
	}
	dir := t.TempDir()
	if key, err := credential.ExistingMasterKey(dir); !errors.Is(err, credential.ErrNoMasterKey) || key != nil {
		t.Fatalf("ExistingMasterKey with no key = %v, %v; want ErrNoMasterKey", key, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".pleiades")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a read made %s: %v", filepath.Join(dir, ".pleiades"), err)
	}
	made, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := credential.ExistingMasterKey(dir); err != nil || !bytes.Equal(got, made) {
		t.Fatalf("ExistingMasterKey after one was made = %v, %v", got, err)
	}
	// A key file that is not a key is an error of its own, never "no key".
	if err := os.WriteFile(filepath.Join(dir, ".pleiades", "master.key"), []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := credential.ExistingMasterKey(dir); err == nil || errors.Is(err, credential.ErrNoMasterKey) {
		t.Fatalf("a corrupt key file: %v, want an error that is not ErrNoMasterKey", err)
	}
	t.Setenv("PLEIADES_MASTER_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if got, err := credential.ExistingMasterKey(t.TempDir()); err != nil || len(got) != 32 {
		t.Fatalf("ExistingMasterKey from the environment = %v, %v", got, err)
	}
}
