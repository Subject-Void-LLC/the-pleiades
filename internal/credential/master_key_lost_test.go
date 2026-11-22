// Tests that a project's master key is made only where nothing is sealed
// under one yet: a project that lost its key while it holds credentials,
// named secrets, a seal key or stored facts is refused by every writer,
// naming what was found and the key to restore, and gains no new key beside
// them (FAILURE_PATTERNS 1344). A fresh project still gets its key on first
// use.
package credential_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// withoutKeyEnv clears PLEIADES_MASTER_KEY for the test, so the key file is
// the only key a project has.
func withoutKeyEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PLEIADES_MASTER_KEY", "")
	if err := os.Unsetenv("PLEIADES_MASTER_KEY"); err != nil {
		t.Fatal(err)
	}
}

// keyPath is dir's master key file.
func keyPath(dir string) string { return filepath.Join(dir, ".pleiades", "master.key") }

// sealedProject returns a project in which seal wrote something under the
// project's master key, made on first use, and whose key file was then lost.
func sealedProject(t *testing.T, seal func(t *testing.T, dir string, key []byte)) string {
	t.Helper()
	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	seal(t, dir, key)
	if err := os.Remove(keyPath(dir)); err != nil {
		t.Fatal(err)
	}
	return dir
}

// sealers are every kind of thing a Crawl project seals under its master
// key, each written by its real writer, with the name a refusal must give.
var sealers = map[string]struct {
	seal  func(t *testing.T, dir string, key []byte)
	names string
}{
	"a device credential": {func(t *testing.T, dir string, key []byte) {
		if err := credential.SaveFileStore(dir, key, "web01", credential.Credential{Username: "u", Password: "s3cret-pw"}); err != nil {
			t.Fatal(err)
		}
	}, "credentials.yaml (1 device credential)"},
	"a named secret alone": {func(t *testing.T, dir string, key []byte) {
		if err := credential.SaveSecretField(dir, key, "ike", "psk", "s3cret-psk"); err != nil {
			t.Fatal(err)
		}
	}, "credentials.yaml (1 named secret)"},
	"a registry account": {func(t *testing.T, dir string, key []byte) {
		if err := credential.SaveRegistryCredential(dir, key, "registry.example.com", "u", "s3cret-reg"); err != nil {
			t.Fatal(err)
		}
	}, "credentials.yaml (1 registry account)"},
	"a database connection": {func(t *testing.T, dir string, key []byte) {
		if err := credential.SaveDatabaseConnection(dir, key, wire.DatabaseConnection{Name: "db", Host: "db.example.com", User: "u", Password: "s3cret-db"}); err != nil {
			t.Fatal(err)
		}
	}, "credentials.yaml (1 database connection)"},
	"the seal key": {func(t *testing.T, dir string, key []byte) {
		if _, _, err := credential.SealKey(dir, key); err != nil {
			t.Fatal(err)
		}
	}, "seal.key"},
	"stored facts": {func(t *testing.T, dir string, key []byte) {
		store, err := facts.NewFileStore(filepath.Join(dir, ".pleiades", "facts"), facts.NewLazyKeys(func() ([]byte, error) { return key, nil }, "project"))
		if err != nil {
			t.Fatal(err)
		}
		entries, _, _ := facts.Prepare(map[string]any{"os.family": "debian"}, facts.Guard{})
		if err := store.Record(context.Background(), "web1", facts.Observation{Entries: entries, At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}, "facts (2 fact files)"}, // the device's facts and its state
}

// TestResolveMasterKey_NeverMakesAKeyBesideSealedMaterial: for each kind of
// sealed material, a project that lost its key refuses ResolveMasterKey,
// naming the material, the key file and PLEIADES_MASTER_KEY, saying what to
// remove when the key is gone, and showing no secret; no key file appears,
// and the material is left as it was.
func TestResolveMasterKey_NeverMakesAKeyBesideSealedMaterial(t *testing.T) {
	withoutKeyEnv(t)
	for name, tc := range sealers {
		t.Run(name, func(t *testing.T) {
			dir := sealedProject(t, tc.seal)
			before := snapshot(t, dir)
			key, err := credential.ResolveMasterKey(dir)
			if err == nil || key != nil {
				// The length alone: a key is never printed, even a test's.
				t.Fatalf("ResolveMasterKey beside %s = %d key bytes, %v; want a refusal", name, len(key), err)
			}
			msg := err.Error()
			for _, want := range []string{tc.names, keyPath(dir), "PLEIADES_MASTER_KEY", "no new key is made", "remove"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal %q does not say %q", msg, want)
				}
			}
			if !errors.Is(err, credential.ErrSealedWithoutKey) {
				t.Errorf("the refusal is not ErrSealedWithoutKey: %v", err)
			}
			if strings.Contains(msg, "s3cret") {
				t.Errorf("the refusal shows a secret: %q", msg)
			}
			if _, statErr := os.Stat(keyPath(dir)); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("a new key was made beside %s: %v", name, statErr)
			}
			if after := snapshot(t, dir); !bytes.Equal(before, after) {
				t.Errorf("the refused call changed the project")
			}
		})
	}
}

// TestResolveMasterKey_AFreshProjectStillGetsItsKey: first use in a project
// holding nothing sealed makes the key, as always: an empty project, one whose
// credential file holds only a username, and one whose fact store holds no
// fact file. Breaking this would break every new user's first command.
func TestResolveMasterKey_AFreshProjectStillGetsItsKey(t *testing.T) {
	withoutKeyEnv(t)
	for name, setup := range map[string]func(t *testing.T, dir string){
		"an empty project": func(*testing.T, string) {},
		"a credential file with no secret in it": func(t *testing.T, dir string) {
			writePrivate(t, filepath.Join(dir, ".pleiades", "credentials.yaml"), "devices:\n  web01:\n    username: u\n")
		},
		"a fact store with no fact file": func(t *testing.T, dir string) {
			if err := os.MkdirAll(filepath.Join(dir, ".pleiades", "facts"), 0o700); err != nil {
				t.Fatal(err)
			}
			writePrivate(t, filepath.Join(dir, ".pleiades", "facts", "web1.lock"), "")
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			setup(t, dir)
			made, err := credential.ResolveMasterKey(dir)
			if err != nil || len(made) != 32 {
				t.Fatalf("first use = %d bytes, %v; want a new key", len(made), err)
			}
			again, err := credential.ExistingMasterKey(dir)
			if err != nil || !bytes.Equal(again, made) {
				t.Fatalf("the key made was not kept: %v", err)
			}
		})
	}
}

// TestResolveMasterKey_ABrokenKeyBesideSealedMaterialIsNeverReplaced: a key
// file another account may read, or one that is not a key, is refused as
// before, now naming what it sealed too, and is never replaced.
func TestResolveMasterKey_ABrokenKeyBesideSealedMaterialIsNeverReplaced(t *testing.T) {
	withoutKeyEnv(t)
	for name, tc := range map[string]struct {
		text string
		mode os.FileMode
		want string
	}{
		"a key file others may read":   {"", 0o644, "chmod 600"},
		"a key file that is not a key": {"not a key at all", 0o600, "not valid base64"},
	} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" && tc.mode != 0o600 {
				t.Skip("POSIX modes do not apply on Windows, where the check checks nothing by design")
			}
			dir := t.TempDir()
			key, err := credential.ResolveMasterKey(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := credential.SaveFileStore(dir, key, "web01", credential.Credential{Username: "u", Password: "s3cret-pw"}); err != nil {
				t.Fatal(err)
			}
			if tc.text != "" {
				writePrivate(t, keyPath(dir), tc.text)
			}
			if err := os.Chmod(keyPath(dir), tc.mode); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(keyPath(dir))
			_, err = credential.ResolveMasterKey(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "credentials.yaml (1 device credential)") {
				t.Fatalf("ResolveMasterKey = %v, want a refusal saying %q and naming the credential file", err, tc.want)
			}
			if after, _ := os.ReadFile(keyPath(dir)); !bytes.Equal(before, after) {
				t.Error("the key file was replaced")
			}
		})
	}
}

// TestResolveMasterKey_SealedMaterialThatCannotBeLookedAtIsRefused: when
// whether the project holds sealed material cannot be told (a credential file
// another account may read, one that does not parse, a fact store that is not
// a directory), no key is made.
func TestResolveMasterKey_SealedMaterialThatCannotBeLookedAtIsRefused(t *testing.T) {
	withoutKeyEnv(t)
	for name, setup := range map[string]func(t *testing.T, dir string){
		"a loose credential file": func(t *testing.T, dir string) {
			if runtime.GOOS == "windows" {
				t.Skip("POSIX modes do not apply on Windows, where the check checks nothing by design")
			}
			path := filepath.Join(dir, ".pleiades", "credentials.yaml")
			writePrivate(t, path, "devices: {}\n")
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"a credential file that does not parse": func(t *testing.T, dir string) {
			writePrivate(t, filepath.Join(dir, ".pleiades", "credentials.yaml"), "devices: [\n")
		},
		"a fact store that is a file": func(t *testing.T, dir string) {
			writePrivate(t, filepath.Join(dir, ".pleiades", "facts"), "")
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			setup(t, dir)
			if _, err := credential.ResolveMasterKey(dir); err == nil || !strings.Contains(err.Error(), "cannot be told") {
				t.Fatalf("ResolveMasterKey = %v, want a refusal saying what it cannot tell", err)
			}
			if _, statErr := os.Stat(keyPath(dir)); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("a key was made: %v", statErr)
			}
		})
	}
}

// TestReaders_NeverMakeAKeyBesideSealedMaterial: the readers that resolve the
// key on first use (a run's credential lookup, and the named secrets a run
// masks) refuse in a project that lost its key, rather than make a new one
// that opens nothing.
func TestReaders_NeverMakeAKeyBesideSealedMaterial(t *testing.T) {
	withoutKeyEnv(t)
	dir := sealedProject(t, func(t *testing.T, dir string, key []byte) {
		if err := credential.SaveSecretField(dir, key, "ike", "psk", "s3cret-psk"); err != nil {
			t.Fatal(err)
		}
		if err := credential.SaveFileStore(dir, key, "web01", credential.Credential{Username: "u", Password: "s3cret-pw"}); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := credential.NewLazyFileStore(dir).Lookup(context.Background(), "web01"); !errors.Is(err, credential.ErrSealedWithoutKey) {
		t.Errorf("Lookup = %v, want ErrSealedWithoutKey", err)
	}
	if _, err := credential.SecretValues(dir); !errors.Is(err, credential.ErrSealedWithoutKey) {
		t.Errorf("SecretValues = %v, want ErrSealedWithoutKey", err)
	}
	if _, statErr := os.Stat(keyPath(dir)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a reader made a key: %v", statErr)
	}
}

// writePrivate writes text to path, owner-only, making its directory.
func writePrivate(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// snapshot is every file under dir's .pleiades with its bytes, for telling
// whether a refused call changed anything.
func snapshot(t *testing.T, dir string) []byte {
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

// TestResolveMasterKey_ConcurrentFirstUseConverges: many callers making a new
// project's key at once, each sealing a credential as soon as it has the key,
// all succeed and all hold the one key. A caller that found no key, then found
// a credential another caller had sealed under the key it had just made, once
// refused that project as one whose key was lost.
func TestResolveMasterKey_ConcurrentFirstUseConverges(t *testing.T) {
	withoutKeyEnv(t)
	const callers = 16
	for round := range 20 {
		dir := t.TempDir()
		keys := make([][]byte, callers)
		errs := make([]error, callers)
		var start, done sync.WaitGroup
		start.Add(1)
		for i := range callers {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				key, err := credential.ResolveMasterKey(dir)
				if err == nil {
					err = credential.SaveSecretField(dir, key, fmt.Sprintf("s%d", i), "f", "s3cret-value")
				}
				keys[i], errs[i] = key, err
			}()
		}
		start.Done()
		done.Wait()
		for i := range callers {
			if errs[i] != nil {
				t.Fatalf("round %d, caller %d: %v", round, i, errs[i])
			}
			if !bytes.Equal(keys[i], keys[0]) {
				t.Fatalf("round %d: callers %d and 0 hold different keys", round, i)
			}
		}
	}
}

// TestLazyFileStore_TriesAgainOnceTheKeyIsRestored: a lookup refused because
// the key file was missing beside the credential it sealed is not remembered:
// once the file is restored the same store reads the credential, with no new
// store (a Controller with no restart).
func TestLazyFileStore_TriesAgainOnceTheKeyIsRestored(t *testing.T) {
	withoutKeyEnv(t)
	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveFileStore(dir, key, "web01", credential.Credential{Username: "u", Password: "s3cret-pw"}); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(keyPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(keyPath(dir)); err != nil {
		t.Fatal(err)
	}
	store := credential.NewLazyFileStore(dir)
	if _, err := store.Lookup(context.Background(), "web01"); !errors.Is(err, credential.ErrSealedWithoutKey) {
		t.Fatalf("Lookup with the key gone = %v, want ErrSealedWithoutKey", err)
	}
	writePrivate(t, keyPath(dir), string(saved))
	got, err := store.Lookup(context.Background(), "web01")
	if err != nil || got.Password != "s3cret-pw" {
		t.Fatalf("Lookup after the key was restored failed: %v", err)
	}
}

// TestResolveMasterKey_AnEnvironmentKeyIsUsedAsGiven pins what the rule does
// not cover, so a change to it is deliberate: a set, well-formed
// PLEIADES_MASTER_KEY is used as it is, with no look at what the project holds
// sealed, though it is not the key that sealed it (a key file holding another
// key, or one that is gone). What is stored meanwhile is sealed under it, and
// what the other key sealed fails only when it is read.
func TestResolveMasterKey_AnEnvironmentKeyIsUsedAsGiven(t *testing.T) {
	withoutKeyEnv(t)
	dir := sealedProject(t, func(t *testing.T, dir string, key []byte) {
		if err := credential.SaveFileStore(dir, key, "web01", credential.Credential{Username: "u", Password: "s3cret-pw"}); err != nil {
			t.Fatal(err)
		}
	})
	other := bytes.Repeat([]byte{7}, 32)
	t.Setenv("PLEIADES_MASTER_KEY", base64.StdEncoding.EncodeToString(other))
	got, err := credential.ResolveMasterKey(dir)
	if err != nil || !bytes.Equal(got, other) {
		t.Fatalf("ResolveMasterKey with an environment key = %v; want that key, used as given", err)
	}
	if _, statErr := os.Stat(keyPath(dir)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("an environment key wrote a key file: %v", statErr)
	}
	if err := credential.SaveFileStore(dir, got, "web02", credential.Credential{Username: "u", Password: "s3cret-two"}); err != nil {
		t.Fatalf("a save under the environment key: %v", err)
	}
	store, err := credential.NewFileStore(dir, got)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(context.Background(), "web01"); err == nil {
		t.Error("the credential the other key sealed opened under the environment key")
	}
}
