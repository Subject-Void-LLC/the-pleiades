// Tests for writing a restored key into a compose env file.
package setup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// TestImportKey_WritesANewFileAndReadsBack covers the clean machine: no
// .env at all, and a key under the default tag, which is left implicit.
func TestImportKey_WritesANewFileAndReadsBack(t *testing.T) {
	d, path := openTestDir(t)
	key := []byte(strings.Repeat("i", 32))

	display, err := setup.ImportKey(d, key, crypto.DefaultKeyVersion)
	if err != nil {
		t.Fatalf("ImportKey() error = %v", err)
	}
	if display != filepath.Join(path, ".env") {
		t.Fatalf("display = %q", display)
	}
	data, err := os.ReadFile(filepath.Join(path, ".env")) // #nosec G304 -- a path under t.TempDir()
	if err != nil {
		t.Fatalf("reading .env: %v", err)
	}
	file, err := setup.ParseEnvFile(data)
	if err != nil {
		t.Fatalf("the written file does not parse: %v", err)
	}
	if got, _ := file.Get(setup.VarMasterKey); got != crypto.EncodeKey(key) {
		t.Fatal("the file does not hold the key given")
	}
	if _, set := file.Get(setup.VarMasterKeyVersion); set {
		t.Fatal("the default tag was written out, which a later edit of the default would contradict")
	}
	if info, _ := os.Stat(filepath.Join(path, ".env")); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", info.Mode().Perm())
	}
}

// TestImportKey_KeepsTheOperatorsLinesAndWritesATag covers an existing file
// with other settings, and a key whose rows carry a tag of their own.
func TestImportKey_KeepsTheOperatorsLinesAndWritesATag(t *testing.T) {
	d, path := openTestDir(t)
	file := filepath.Join(path, ".env")
	before := "# mine\nCOMPOSE_PROFILES=ops\n"
	if err := os.WriteFile(file, []byte(before), 0o600); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if _, err := setup.ImportKey(d, []byte(strings.Repeat("j", 32)), "v3"); err != nil {
		t.Fatalf("ImportKey() error = %v", err)
	}
	data, _ := os.ReadFile(file) // #nosec G304 -- a path under t.TempDir()
	if !strings.HasPrefix(string(data), before) {
		t.Fatalf("the operator's lines were not kept as written:\n%s", data)
	}
	parsed, err := setup.ParseEnvFile(data)
	if err != nil {
		t.Fatalf("the written file does not parse: %v", err)
	}
	if got, _ := parsed.Get(setup.VarMasterKeyVersion); got != "v3" {
		t.Fatalf("%s = %q, want v3", setup.VarMasterKeyVersion, got)
	}
}

// TestImportKey_NeverReplacesAKey is the guard: a file holding a key, or a
// previous key, is refused and left byte for byte as it was.
func TestImportKey_NeverReplacesAKey(t *testing.T) {
	for _, held := range []string{setup.VarMasterKey, setup.VarPreviousKey} {
		t.Run(held, func(t *testing.T) {
			d, path := openTestDir(t)
			file := filepath.Join(path, ".env")
			before := held + "=" + testKey + "\n"
			if err := os.WriteFile(file, []byte(before), 0o600); err != nil {
				t.Fatalf("seeding: %v", err)
			}
			_, err := setup.ImportKey(d, []byte(strings.Repeat("n", 32)), crypto.DefaultKeyVersion)
			if err == nil || !strings.Contains(err.Error(), held) {
				t.Fatalf("ImportKey() error = %v, want a refusal naming %s", err, held)
			}
			if data, _ := os.ReadFile(file); string(data) != before { // #nosec G304 -- a path under t.TempDir()
				t.Fatalf("a refused import changed the file to %q", data)
			}
		})
	}

	d, _ := openTestDir(t)
	if _, err := setup.ImportKey(d, []byte(strings.Repeat("n", 32)), "v$1"); err == nil {
		t.Fatal("ImportKey accepted a version tag with the envelope's field separator in it")
	}
}
