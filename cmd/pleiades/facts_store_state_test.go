// Tests that validation tells a project with no master key from one whose
// key is there and cannot be used, saying which, and never shows a key.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// TestProjectFactStore_TellsAnUnusableKeyFromNone: no key at all is no key;
// a key file another account may read, a key file that is not a key and an
// environment value that is not a key are each unusable, naming why and
// never the value; a good key file is readable.
func TestProjectFactStore_TellsAnUnusableKeyFromNone(t *testing.T) {
	good := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	for name, tc := range map[string]struct {
		file   string
		mode   os.FileMode
		env    string
		want   validate.FactStore
		reason string
	}{
		"no key":                              {want: validate.FactStoreNoKey},
		"a good key file":                     {file: good, mode: 0o600, want: validate.FactStoreReadable},
		"a key file others read":              {file: good, mode: 0o644, want: validate.FactStoreKeyUnreadable, reason: "master.key"},
		"a key file that is no key":           {file: "not a key at all", mode: 0o600, want: validate.FactStoreKeyUnreadable, reason: "not valid base64"},
		"an environment value that is no key": {env: "c2hvcnQ=", want: validate.FactStoreKeyUnreadable, reason: "PLEIADES_MASTER_KEY"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PLEIADES_MASTER_KEY", tc.env)
			if tc.env == "" {
				if err := os.Unsetenv("PLEIADES_MASTER_KEY"); err != nil {
					t.Fatal(err)
				}
			}
			dir := t.TempDir()
			if tc.file != "" {
				path := filepath.Join(vaultDir(dir), ".pleiades", "master.key")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.file), tc.mode); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, tc.mode); err != nil {
					t.Fatal(err)
				}
			}
			got, reason := projectFactStore(dir)
			if got != tc.want || !strings.Contains(reason, tc.reason) {
				t.Fatalf("projectFactStore = %v, %q; want %v naming %q", got, reason, tc.want, tc.reason)
			}
			if strings.Contains(reason, good) || (tc.env != "" && strings.Contains(reason, tc.env)) {
				t.Errorf("the reason %q shows the key", reason)
			}
		})
	}
}

// TestFactKeysToWrite_ALostKeyIsNeverReplacedBesideSealedFacts: a project
// that stored facts and lost its master key refuses a new fact, naming the
// key to restore, and makes no new key beside files sealed under the old one;
// a project that never stored a fact makes its key on the first write, as its
// credentials would.
func TestFactKeysToWrite_ALostKeyIsNeverReplacedBesideSealedFacts(t *testing.T) {
	withoutMasterKeyEnv(t)
	record := func(dir, device string) error {
		store, err := facts.NewFileStore(factsDir(dir), factKeysToWrite(dir))
		if err != nil {
			t.Fatal(err)
		}
		entries, _, _ := facts.Prepare(map[string]any{"os.family": "debian"}, facts.Guard{})
		return store.Record(context.Background(), device, facts.Observation{Entries: entries, At: time.Now()})
	}
	fresh := t.TempDir()
	if err := record(fresh, "web1"); err != nil {
		t.Fatalf("a first fact in a project with no key: %v", err)
	}
	if _, err := os.Stat(masterKeyFile(fresh)); err != nil {
		t.Fatalf("the first write made no key: %v", err)
	}
	if err := os.Remove(masterKeyFile(fresh)); err != nil {
		t.Fatal(err)
	}
	err := record(fresh, "web2")
	if err == nil || !strings.Contains(err.Error(), "restore .pleiades/master.key or set PLEIADES_MASTER_KEY") {
		t.Fatalf("a write after the key was lost = %v, want it refused naming the key", err)
	}
	if _, statErr := os.Stat(masterKeyFile(fresh)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a write after the key was lost made a new key beside the sealed facts: %v", statErr)
	}
}
