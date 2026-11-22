// Tests through the real binary that a project whose master key was lost
// refuses every command that would make a new key beside what the old one
// sealed, naming what it holds and the key to restore, and that a new project
// still gets its key from its first command (FAILURE_PATTERNS 1344).
package main_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// keyWriters are the commands that store something under the master key,
// making the key when a project has none, each with what it reads on
// standard input.
var keyWriters = []struct {
	name  string
	stdin string
	args  []string
}{
	{"add-credential", "s3cret-two\n", []string{"add-credential", "web02", "--username", "u", "--password-stdin", "--no-device"}},
	{"add-credential --generate", "", []string{"add-credential", "web03", "--username", "u", "--generate", "--no-device"}},
	{"add-secret", "s3cret-psk2\n", []string{"add-secret", "ike2", "--field", "psk", "--value-stdin"}},
	{"add-registry-credential", "s3cret-reg\n", []string{"add-registry-credential", "registry.example.com", "--username", "u", "--password-stdin"}},
	{"add-database-connection", "s3cret-db\n", []string{"add-database-connection", "db", "--host", "db.example.com", "--user", "u", "--password-stdin"}},
	{"seal-key", "", []string{"seal-key"}},
}

// unsetMasterKeyEnv keeps PLEIADES_MASTER_KEY from the binary's environment,
// so the key file is the only key a project has.
func unsetMasterKeyEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PLEIADES_MASTER_KEY", "")
	if err := os.Unsetenv("PLEIADES_MASTER_KEY"); err != nil {
		t.Fatal(err)
	}
}

// TestCLI_ALostMasterKeyIsNeverReplaced: a project holding a credential and a
// named secret whose master key was then lost refuses every writer, naming the
// credential file, the key file and PLEIADES_MASTER_KEY and saying what to
// remove when the key is gone, shows none of its secrets, and is left exactly
// as it was with no new key. Restoring the key lets the same command work.
func TestCLI_ALostMasterKeyIsNeverReplaced(t *testing.T) {
	unsetMasterKeyEnv(t)
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithStdin(t, dir, "s3cret-one\n", "add-credential", "web01", "--username", "u", "--password-stdin", "--no-device"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithStdin(t, dir, "s3cret-psk\n", "add-secret", "ike", "--field", "psk", "--value-stdin"); err != nil {
		t.Fatalf("add-secret: %v\n%s", err, out)
	}
	key := filepath.Join(dir, ".pleiades", "master.key")
	saved, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	for _, w := range keyWriters {
		t.Run(w.name, func(t *testing.T) {
			// Each writer meets the lost key on its own, whatever one before
			// it did.
			if err := os.Remove(key); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			before := vaultSnapshot(t, dir)
			out, err := runPleiadesWithStdin(t, dir, w.stdin, w.args...)
			if err == nil {
				t.Fatalf("%s in a project that lost its key succeeded:\n%s", w.name, out)
			}
			for _, want := range []string{"credentials.yaml (1 device credential, 1 named secret)", ".pleiades/master.key", "PLEIADES_MASTER_KEY", "no new key is made", "remove what you accept losing"} {
				if !strings.Contains(out, want) {
					t.Errorf("%s's refusal does not say %q:\n%s", w.name, want, out)
				}
			}
			if strings.Contains(out, "s3cret") {
				t.Errorf("%s's refusal shows a secret:\n%s", w.name, out)
			}
			if _, statErr := os.Stat(key); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("%s made a new key beside the sealed credentials: %v", w.name, statErr)
			}
			if after := vaultSnapshot(t, dir); !bytes.Equal(before, after) {
				t.Errorf("%s changed the project though it was refused", w.name)
			}
		})
	}
	// A listing, which decrypts nothing and needs no key, still works, and
	// makes no key either.
	if out, err := runPleiades(t, dir, "list-secrets"); err != nil || !strings.Contains(out, "ike") {
		t.Errorf("list-secrets in a project that lost its key: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(key); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a reading command made a key: %v", statErr)
	}
	// The key restored, the same command works.
	if err := os.Remove(key); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, saved, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiadesWithStdin(t, dir, "s3cret-two\n", keyWriters[0].args...); err != nil {
		t.Fatalf("add-credential after the key was restored: %v\n%s", err, out)
	}
}

// TestCLI_ANewProjectGetsItsKeyFromItsFirstWriter: in a project holding
// nothing sealed, each writer, run first, makes the master key and stores what
// it was given, exactly as before; a new user's first command still works.
func TestCLI_ANewProjectGetsItsKeyFromItsFirstWriter(t *testing.T) {
	unsetMasterKeyEnv(t)
	for _, w := range keyWriters {
		t.Run(w.name, func(t *testing.T) {
			dir := t.TempDir()
			if out, err := runPleiades(t, dir, "init"); err != nil {
				t.Fatalf("init: %v\n%s", err, out)
			}
			if out, err := runPleiadesWithStdin(t, dir, w.stdin, w.args...); err != nil {
				t.Fatalf("%s as a new project's first command: %v\n%s", w.name, err, out)
			}
			info, err := os.Stat(filepath.Join(dir, ".pleiades", "master.key"))
			if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
				t.Fatalf("%s made no owner-only key: %v", w.name, err)
			}
		})
	}
}

// vaultSnapshot is every file under dir's .pleiades with its bytes.
func vaultSnapshot(t *testing.T, dir string) []byte {
	t.Helper()
	var out bytes.Buffer
	err := filepath.WalkDir(filepath.Join(dir, ".pleiades"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		out.WriteString(path + "\x00")
		out.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
