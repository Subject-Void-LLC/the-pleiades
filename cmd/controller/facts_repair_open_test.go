// Tests of what `controller facts` refuses before it reads a device
// (facts_repair_open.go) and of naming a device by ID, through runFacts on
// a real SQLite file: a database it would have to create or migrate, a
// master key the database's key registry does not record, a wrong key with
// nothing to check it against, and a facts key under which no device's
// rows were stored.
package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// otherMasterKey is a MASTER_ENCRYPTION_KEY the seeded history was not
// sealed under.
var otherMasterKey = crypto.EncodeKey(bytes.Repeat([]byte{6}, 32))

func TestFacts_RefusesADatabaseItWouldHaveToCreateOrMigrate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DB_DSN", "")
	t.Setenv("MASTER_ENCRYPTION_KEY", otherMasterKey)
	t.Setenv("MASTER_ENCRYPTION_KEY_PREVIOUS", "")
	t.Setenv("MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION", "")
	t.Setenv(factDigestKeyEnv, base64.StdEncoding.EncodeToString(digestSecret))
	t.Setenv(factDigestKeyPreviousEnv, "")
	missing := filepath.Join(dir, "missing.db")
	t.Setenv("DB_PATH", missing)
	for _, args := range [][]string{{"inspect"}, {"discard", "--device", "web1", "--confirm"}} {
		if code, _, errs := runFactsCommand(t, args...); code != factsExitFailed || !strings.Contains(errs, "does not exist") {
			t.Errorf("%v against a missing database: exit %d, %s", args, code, errs)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*")); len(matches) != 0 {
		t.Fatalf("a refused command created %v", matches)
	}
	// A file with no migration history is not migrated, and stays empty.
	empty := filepath.Join(dir, "empty.db")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DB_PATH", empty)
	if code, _, errs := runFactsCommand(t, "inspect"); code != factsExitFailed || !strings.Contains(errs, "no migration history") {
		t.Errorf("an empty database: exit %d, %s", code, errs)
	}
	if info, err := os.Stat(empty); err != nil || info.Size() != 0 {
		t.Errorf("the empty database was written to: %v, %v", info, err)
	}
}

func TestFacts_RefusesAMasterKeyTheRegistryDoesNotRecord(t *testing.T) {
	f := newFactsDB(t)
	f.breakChain()
	t.Setenv("MASTER_ENCRYPTION_KEY", otherMasterKey)
	for _, args := range [][]string{{"inspect"}, {"inspect", "--device", "web1"}, {"truncate", "--device", "web1", "--confirm"},
		{"discard", "--device", "web1", "--confirm", "--discard-unverifiable-history"}} {
		code, _, errs := runFactsCommand(t, args...)
		if code != factsExitFailed || !strings.Contains(errs, "is not a key this database's registry records") {
			t.Errorf("%v with a master key the registry does not record: exit %d, %s", args, code, errs)
		}
	}
	if n := f.count("SELECT count(*) FROM facts"); n != 4 {
		t.Errorf("%d facts after a refused repair, want the four stored", n)
	}
}

// TestFacts_AWrongMasterKeyWithNoRegistryReadsAsUnopened: with a registry
// that records no key there is nothing to check the master key against, so
// the command warns, and a device no row of which opens says to check the
// key: truncate refuses it, and discard takes the flag that names it.
func TestFacts_AWrongMasterKeyWithNoRegistryReadsAsUnopened(t *testing.T) {
	f := newFactsDB(t)
	if _, err := f.db.Exec("DELETE FROM encryption_keys"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MASTER_ENCRYPTION_KEY", otherMasterKey)
	code, out, errs := runFactsCommand(t, "inspect", "--device", "web1")
	if code != factsExitNeedsRepair || !strings.Contains(out, ": unopened") || !strings.Contains(out, "MASTER_ENCRYPTION_KEY") || !strings.Contains(errs, "records no master key") {
		t.Fatalf("a wrong master key: exit %d\n%s%s", code, out, errs)
	}
	if code, _, errs := runFactsCommand(t, "truncate", "--device", "web1", "--confirm"); code != factsExitFailed || !strings.Contains(errs, "MASTER_ENCRYPTION_KEY") {
		t.Errorf("a truncate under a wrong master key: exit %d, %s", code, errs)
	}
	if code, _, errs := runFactsCommand(t, "discard", "--device", "web1", "--confirm"); code != factsExitFailed || !strings.Contains(errs, "cannot be verified") {
		t.Errorf("a discard under a wrong master key without its flag: exit %d, %s", code, errs)
	}
	if n := f.count("SELECT count(*) FROM facts"); n != 4 {
		t.Errorf("%d facts after refused repairs, want the four stored", n)
	}
}

// TestFactsInspect_SaysWhenEveryDeviceIsUnderAnotherKey: a facts key no
// device's rows were stored under makes every device other_keys, which the
// fleet's report says is what a wrong facts key looks like; discarding one
// takes the flag that names an unverifiable history.
func TestFactsInspect_SaysWhenEveryDeviceIsUnderAnotherKey(t *testing.T) {
	f := newFactsDB(t)
	t.Setenv(factDigestKeyEnv, base64.StdEncoding.EncodeToString([]byte("a third 32-byte facts key here!!")))
	t.Setenv(factDigestKeyPreviousEnv, "")
	code, out, _ := runFactsCommand(t, "inspect")
	if code != factsExitNeedsRepair || !strings.Contains(out, "1 device with stored facts read") || !strings.Contains(out, "wrong PLEIADES_FACTS_DIGEST_KEY") {
		t.Fatalf("a wrong facts key: exit %d\n%s", code, out)
	}
	if code, _, errs := runFactsCommand(t, "discard", "--device", "web1", "--confirm"); code != factsExitFailed || !strings.Contains(errs, "cannot be verified") {
		t.Errorf("a discard of an unverifiable history without its flag: exit %d, %s", code, errs)
	}
	if code, out, errs := runFactsCommand(t, "discard", "--device", "web1", "--confirm", "--discard-unverifiable-history"); code != factsExitOK || !strings.Contains(out, "Discarded") {
		t.Errorf("with the flag: exit %d\n%s%s", code, out, errs)
	}
	if n := f.count("SELECT count(*) FROM facts"); n != 0 {
		t.Errorf("%d facts left after the discard", n)
	}
}

func TestFacts_NamesADeviceByItsID(t *testing.T) {
	f := newFactsDB(t)
	if code, out, errs := runFactsCommand(t, "inspect", "--device-id", f.id); code != factsExitOK || !strings.Contains(out, "Device web1 (id "+f.id+")") {
		t.Errorf("inspect --device-id: exit %d\n%s%s", code, out, errs)
	}
	if code, _, errs := runFactsCommand(t, "discard", "--device-id", "no-such-id", "--confirm"); code != factsExitFailed || !strings.Contains(errs, "no device has the ID") {
		t.Errorf("an unknown ID: exit %d, %s", code, errs)
	}
	if code, _, errs := runFactsCommand(t, "inspect", "--device", "web1", "--device-id", f.id); code != factsExitUsage || !strings.Contains(errs, "not both") {
		t.Errorf("both: exit %d, %s", code, errs)
	}
}
