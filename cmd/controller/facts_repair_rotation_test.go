// Tests of `controller facts` against a database partway through a master
// key rotation (FAILURE_PATTERNS 1341), through runFacts on a real SQLite
// file: the first four values sealed as v1 under one key, two more as v2
// under the next by a controller running with both, the registry recording
// both keys. Run with only one of the two, the rows sealed under the other do
// not open, and the command must neither call the device broken nor remove
// any of its history without the flag that names an unverifiable one.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
)

// firstMasterKey is newFactsDB's MASTER_ENCRYPTION_KEY, which sealed its
// history as v1; nextMasterKey is the key a rotation moves to, as v2.
var (
	firstMasterKey = crypto.EncodeKey(bytes.Repeat([]byte{5}, 32))
	nextMasterKey  = crypto.EncodeKey(bytes.Repeat([]byte{8}, 32))
)

// rotateMidway stores two gathers more of web1, each a new package, through
// a controller started partway through a rotation: nextMasterKey current as
// v2 and firstMasterKey beside it as v1, recorded in the key registry as a
// start records it. The environment is left as that controller's.
func (f *factsDB) rotateMidway() {
	f.t.Helper()
	ctx := context.Background()
	f.t.Setenv("MASTER_ENCRYPTION_KEY", nextMasterKey)
	f.t.Setenv("MASTER_ENCRYPTION_KEY_VERSION", "v2")
	f.t.Setenv("MASTER_ENCRYPTION_KEY_PREVIOUS", firstMasterKey)
	f.t.Setenv("MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION", "v1")
	envelope, err := loadEnvelopeService()
	if err != nil {
		f.t.Fatal(err)
	}
	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: "sqlite://" + f.path})
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	installCryptoHooks(client, envelope)
	recordKeyFirstUse(ctx, client, quietLogger())
	old, err := entstore.AtRestKeys(previousDigestSecret)
	if err != nil {
		f.t.Fatal(err)
	}
	store := entstore.New(client, old, envelope)
	at := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	for i, version := range []string{"1:9.9p1-zq7build", "1:9.9p2-zq7build"} {
		entries, _, err := facts.Prepare(map[string]any{"package.openssh-server": version}, facts.Guard{})
		if err != nil {
			f.t.Fatal(err)
		}
		obs := facts.Observation{Entries: entries, At: at.Add(time.Duration(i) * time.Hour), By: facts.Provenance{Run: "run", Order: facts.Order{Source: facts.ClockSource, Token: uint64(i + 10)}}}
		if err := store.Record(ctx, f.id, obs); err != nil {
			f.t.Fatal(err)
		}
	}
	// The history's own facts key as the current one: under the right master
	// keys it is healthy.
	f.t.Setenv(factDigestKeyEnv, base64.StdEncoding.EncodeToString(previousDigestSecret))
	f.t.Setenv(factDigestKeyPreviousEnv, "")
}

// masterKeys sets the command's master key settings: current as version,
// and no previous key.
func masterKeys(t *testing.T, current, version string) {
	t.Setenv("MASTER_ENCRYPTION_KEY", current)
	t.Setenv("MASTER_ENCRYPTION_KEY_VERSION", version)
	t.Setenv("MASTER_ENCRYPTION_KEY_PREVIOUS", "")
	t.Setenv("MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION", "")
}

// TestFacts_AStaleMasterKeyIsNotDeviceDamage is case (a): the environment
// from before the rotation, the old key alone as v1. It is a key the
// registry records, so nothing refuses it, and the command warns that the
// registry records a newer one; the two v2 values do not open and the four
// v1 values verify. A truncate would remove the last v1 gather and both v2
// ones, every one intact, so neither repair runs without its flag.
func TestFacts_AStaleMasterKeyIsNotDeviceDamage(t *testing.T) {
	f := newFactsDB(t)
	f.rotateMidway()
	if code, out, errs := runFactsCommand(t, "inspect", "--device", "web1"); code != factsExitOK || !strings.Contains(out, ": healthy") || errs != "" {
		t.Fatalf("the control failed: with the server's own master keys, exit %d\n%s%s", code, out, errs)
	}
	masterKeys(t, firstMasterKey, "")
	code, out, errs := runFactsCommand(t, "inspect", "--device", "web1")
	if code != factsExitNeedsRepair || strings.Contains(out, ": broken") {
		t.Errorf("inspected with the old master key alone: exit %d, want %d and not broken\n%s%s", code, factsExitNeedsRepair, out, errs)
	}
	for _, want := range []string{": partly_unopened", "Sealed as:  master key version v1 (MASTER_ENCRYPTION_KEY_VERSION), 4 rows",
		"Sealed as:  master key version v2 (not given), 2 rows", "Unopened:   2 rows do not open", "sealed as master key version v2",
		"each only with the flag that names an unverifiable history", "--truncate-unverifiable-history"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errs, "records a master key newer than the ones given") || !strings.Contains(errs, "version v2") {
		t.Errorf("no warning that the registry records a newer master key:\n%s", errs)
	}
	code, out, _ = runFactsCommand(t, "inspect", "--json")
	if code != factsExitNeedsRepair || !strings.Contains(out, `"unverifiable": true`) || !strings.Contains(out, `"sealed_version": "v2"`) {
		t.Errorf("--json: exit %d\n%s", code, out)
	}
	fitsTheSchema(t, out)
	if code, out, errs := runFactsCommand(t, "truncate", "--device", "web1", "--confirm"); code != factsExitFailed || !strings.Contains(errs, "--truncate-unverifiable-history") {
		t.Errorf("a truncate with the old master key alone: exit %d, want %d naming its flag\n%s%s", code, factsExitFailed, out, errs)
	}
	if code, out, errs := runFactsCommand(t, "discard", "--device", "web1", "--confirm"); code != factsExitFailed || !strings.Contains(errs, "--discard-unverifiable-history") {
		t.Errorf("a discard with the old master key alone: exit %d, want %d naming its flag\n%s%s", code, factsExitFailed, out, errs)
	}
	if n := f.count("SELECT count(*) FROM facts"); n != 6 {
		t.Errorf("%d facts after refused repairs, want the six stored", n)
	}
	// Told by name, the truncate shows what it would cut, and changes nothing
	// until --confirm.
	if code, out, errs := runFactsCommand(t, "truncate", "--device", "web1", "--truncate-unverifiable-history"); code != factsExitOK || !strings.Contains(out, "Removes:    3 stored facts") {
		t.Errorf("a shown truncate with its flag: exit %d\n%s%s", code, out, errs)
	}
	if n := f.count("SELECT count(*) FROM facts"); n != 6 {
		t.Errorf("%d facts after a shown truncate, want the six stored", n)
	}
}

// TestFacts_AMissingPreviousMasterKeyIsNotDeviceDamage is case (b): the new
// key as v2 with the previous one left out. The four oldest values do not
// open, so no truncate applies, and a discard would remove a history every
// row of which is intact: it takes its flag.
func TestFacts_AMissingPreviousMasterKeyIsNotDeviceDamage(t *testing.T) {
	f := newFactsDB(t)
	f.rotateMidway()
	masterKeys(t, nextMasterKey, "v2")
	code, out, errs := runFactsCommand(t, "inspect", "--device", "web1")
	if code != factsExitNeedsRepair || strings.Contains(out, ": broken") || !strings.Contains(out, ": partly_unopened") || !strings.Contains(out, "Repairs:    discard, each only with the flag") {
		t.Errorf("inspected without the previous master key: exit %d, want %d, partly_unopened, discard alone\n%s%s", code, factsExitNeedsRepair, out, errs)
	}
	if code, out, errs := runFactsCommand(t, "discard", "--device", "web1", "--confirm"); code != factsExitFailed || !strings.Contains(errs, "master key version v1") {
		t.Errorf("a discard without the previous master key: exit %d, want %d naming v1\n%s%s", code, factsExitFailed, out, errs)
	}
	if n := f.count("SELECT count(*) FROM facts"); n != 6 {
		t.Errorf("%d facts after a refused discard, want the six stored", n)
	}
}

// TestFacts_WarnsOfMasterKeySettingsTheRegistryContradicts: a key given as
// another version than the registry records it under, and a previous key the
// registry does not record, are each warned of before anything is read.
func TestFacts_WarnsOfMasterKeySettingsTheRegistryContradicts(t *testing.T) {
	f := newFactsDB(t)
	f.rotateMidway()
	// The new key given as v3, which it never sealed as.
	t.Setenv("MASTER_ENCRYPTION_KEY_VERSION", "v3")
	_, out, errs := runFactsCommand(t, "inspect", "--device", "web1")
	if !strings.Contains(errs, "MASTER_ENCRYPTION_KEY (") || !strings.Contains(errs, "recorded in the database's key registry as version v2 and was given as version v3") || !strings.Contains(out, ": partly_unopened") {
		t.Errorf("a key given as another version:\n%s%s", out, errs)
	}
	// A previous key no controller ran this database with.
	t.Setenv("MASTER_ENCRYPTION_KEY_VERSION", "v2")
	t.Setenv("MASTER_ENCRYPTION_KEY_PREVIOUS", otherMasterKey)
	_, out, errs = runFactsCommand(t, "inspect", "--device", "web1")
	if !strings.Contains(errs, "MASTER_ENCRYPTION_KEY_PREVIOUS (") || !strings.Contains(errs, "is not a key this database's registry records") {
		t.Errorf("an unrecorded previous key:\n%s%s", out, errs)
	}
	// The previous key given as another version than it sealed as.
	t.Setenv("MASTER_ENCRYPTION_KEY_PREVIOUS", firstMasterKey)
	t.Setenv("MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION", "v0")
	_, out, errs = runFactsCommand(t, "inspect", "--device", "web1")
	if !strings.Contains(errs, "MASTER_ENCRYPTION_KEY_PREVIOUS (") || !strings.Contains(errs, "as version v1 and was given as version v0") {
		t.Errorf("a previous key given as another version:\n%s%s", out, errs)
	}
}
