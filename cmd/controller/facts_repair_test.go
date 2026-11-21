// Tests of `controller facts` (facts_repair.go) through runFacts, the
// command's own entry point: a real SQLite file the controller's migrations
// made, a history stored through the real fact store under the facts key
// the environment names, and one edit with plain SQL, then the command read
// back as text and as JSON held to its published schema.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factsreport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/jsonschema"
)

// factsPlaintexts are what the seeded history holds that no output of the
// command may name: parts of its facts' names and its values, none of them
// hex.
var factsPlaintexts = []string{"openssh", "zq9operator", "zq9sh", "9.6p1", "ubuntu"}

// factsDB is a controller database for the command, seeded with web1's
// history stored under previousDigestSecret's keys (the facts key before a
// change), and the environment the command reads, with both keys set.
type factsDB struct {
	t    *testing.T
	path string
	db   *sql.DB
	// id is web1's device ID.
	id string
}

// newFactsDB makes the database and the environment.
func newFactsDB(t *testing.T) *factsDB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller.db")
	t.Setenv("DB_DSN", "")
	t.Setenv("DB_PATH", path)
	t.Setenv("MASTER_ENCRYPTION_KEY", crypto.EncodeKey(bytes.Repeat([]byte{5}, 32)))
	t.Setenv("MASTER_ENCRYPTION_KEY_PREVIOUS", "")
	t.Setenv("MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION", "")
	t.Setenv(factDigestKeyEnv, base64.StdEncoding.EncodeToString(digestSecret))
	t.Setenv(factDigestKeyPreviousEnv, base64.StdEncoding.EncodeToString(previousDigestSecret))
	envelope, err := loadEnvelopeService()
	if err != nil {
		t.Fatal(err)
	}
	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: "sqlite://" + path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	// The same hooks the command's client installs, so what it reads back
	// is what a controller wrote, and the master key recorded in the key
	// registry as a controller's start records it.
	installCryptoHooks(client, envelope)
	recordKeyFirstUse(ctx, client, quietLogger())
	dev := client.Device.Create().SetName("web1").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.1"}).SaveX(ctx)
	old, err := entstore.AtRestKeys(previousDigestSecret)
	if err != nil {
		t.Fatal(err)
	}
	store := entstore.New(client, old, envelope)
	at := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	for i, version := range []string{"1:9.6p1-3ubuntu13.5", "1:9.6p1-3ubuntu13.6", "1:9.6p1-3ubuntu13.7"} {
		entries, _, err := facts.Prepare(map[string]any{"package.openssh-server": version, "user.zq9operator": map[string]any{"shell": "/bin/zq9sh"}}, facts.Guard{})
		if err != nil {
			t.Fatal(err)
		}
		obs := facts.Observation{Entries: entries, At: at.Add(time.Duration(i) * time.Hour), By: facts.Provenance{Run: "run", Order: facts.Order{Source: facts.ClockSource, Token: uint64(i + 1)}}}
		if err := store.Record(ctx, dev.DeviceID, obs); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &factsDB{t: t, path: path, db: db, id: dev.DeviceID}
}

// breakChain moves the time of the fourth stored value, the one gather 3
// wrote (gather 1 wrote the package and the user, gathers 2 and 3 a new
// package each): it still opens, and the chain breaks at it, so a re-key
// refuses the device, and the last value verifying before it is gather 2's.
func (f *factsDB) breakChain() {
	f.t.Helper()
	if _, err := f.db.Exec("UPDATE facts SET created_at = ? WHERE id = (SELECT id FROM facts ORDER BY id LIMIT 1 OFFSET 3)", time.Unix(0, 0)); err != nil {
		f.t.Fatal(err)
	}
}

// count is one count(*) query's answer.
func (f *factsDB) count(query string) int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRow(query).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// runFactsCommand runs `controller facts args...` and returns its exit code
// and its two streams, failing the test when either names a stored fact.
func runFactsCommand(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runFacts(append([]string{factsCommand}, args...), &stdout, &stderr)
	for _, plain := range factsPlaintexts {
		if strings.Contains(stdout.String()+stderr.String(), plain) {
			t.Errorf("controller facts %v names %q:\n%s%s", args, plain, stdout.String(), stderr.String())
		}
	}
	return code, stdout.String(), stderr.String()
}

func TestFactsInspect_ReportsARefusedDevice(t *testing.T) {
	f := newFactsDB(t)
	f.breakChain()
	code, out, errs := runFactsCommand(t, "inspect", "--device", "web1")
	if code != factsExitNeedsRepair || errs != "" {
		t.Fatalf("exit %d, stderr %q; want %d", code, errs, factsExitNeedsRepair)
	}
	for _, want := range []string{"Device web1", ": refused", "chain_hash_mismatch", "chain index 3", "Under the previous key", "Repairs:    truncate, discard", "Truncate:   would cut at gather 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
	// The same as JSON, held to the published schema.
	code, out, _ = runFactsCommand(t, "inspect", "--json")
	if code != factsExitNeedsRepair {
		t.Fatalf("--json exit %d", code)
	}
	var report factsreport.Report
	if err := json.Unmarshal([]byte(out), &report); err != nil || report.Scope != factsreport.ScopeFleet || report.NeedsRepair != 1 || len(report.Devices) != 1 {
		t.Fatalf("the JSON report %+v, %v:\n%s", report, err, out)
	}
	fitsTheSchema(t, out)
}

// fitsTheSchema validates out against the schema tools/gendocs publishes.
func fitsTheSchema(t *testing.T, out string) {
	t.Helper()
	built, err := factsreport.Schema(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(built)
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	for _, problem := range jsonschema.NewValidator(schema).Check(factsreport.SchemaFile, doc, schema) {
		t.Error(problem)
	}
}

func TestFactsRepair_ShowsThenTruncatesAndRecordsIt(t *testing.T) {
	f := newFactsDB(t)
	f.breakChain()
	code, out, errs := runFactsCommand(t, "truncate", "--device", "web1")
	if code != factsExitOK || !strings.Contains(out, "Would truncate the stored facts of web1") || !strings.Contains(out, "Nothing was changed") || errs != "" {
		t.Fatalf("the shown truncate: exit %d\n%s%s", code, out, errs)
	}
	if n := f.count("SELECT count(*) FROM facts"); n != 4 {
		t.Fatalf("%d facts after a truncate without --confirm, want the four stored", n)
	}
	code, out, errs = runFactsCommand(t, "truncate", "--device", "web1", "--confirm")
	if code != factsExitOK || !strings.Contains(out, "Truncated the stored facts of web1") || !strings.Contains(out, "Kept:       2 stored facts, 2 of them re-keyed") {
		t.Fatalf("the truncate: exit %d\n%s%s", code, out, errs)
	}
	current, _ := entstore.AtRestKeys(digestSecret)
	if n := f.count("SELECT count(*) FROM facts WHERE keyed_by <> '" + current.ID() + "'"); n != 0 {
		t.Errorf("%d facts are not under the current key after the truncate", n)
	}
	if code, out, _ := runFactsCommand(t, "inspect", "--device", "web1"); code != factsExitOK || !strings.Contains(out, ": healthy") {
		t.Errorf("after the truncate: exit %d\n%s", code, out)
	}
	if n := f.count("SELECT count(*) FROM activity_entries WHERE actor = '" + factsActor + "' AND object_kind = '" + activity.KindDeviceFacts + "'"); n != 1 {
		t.Errorf("%d activity entries for the truncate, want one", n)
	}
}

// TestFacts_WorksWithOnlyThePreviousKey: with the new key not yet set, the
// history is inspected and discarded under the old key alone; a truncate,
// which re-keys what it keeps, says it needs the new key.
func TestFacts_WorksWithOnlyThePreviousKey(t *testing.T) {
	f := newFactsDB(t)
	t.Setenv(factDigestKeyEnv, "")
	if code, out, _ := runFactsCommand(t, "inspect", "--device", "web1"); code != factsExitOK || !strings.Contains(out, ": rekey_pending") || strings.Contains(out, "current ") {
		t.Fatalf("an intact history under the previous key alone: exit %d\n%s", code, out)
	}
	f.breakChain()
	code, out, _ := runFactsCommand(t, "inspect", "--device", "web1")
	if code != factsExitNeedsRepair || !strings.Contains(out, ": refused") || !strings.Contains(out, "Repairs:    discard\n") {
		t.Fatalf("a broken history under the previous key alone: exit %d\n%s", code, out)
	}
	if code, _, errs := runFactsCommand(t, "truncate", "--device", "web1", "--confirm"); code != factsExitFailed || !strings.Contains(errs, factDigestKeyEnv) {
		t.Errorf("a truncate with no current key: exit %d, %s", code, errs)
	}
	if code, out, errs := runFactsCommand(t, "discard", "--device", "web1", "--confirm"); code != factsExitOK || !strings.Contains(out, "Discarded") {
		t.Fatalf("the discard: exit %d\n%s%s", code, out, errs)
	}
	if n := f.count("SELECT count(*) FROM facts"); n != 0 {
		t.Errorf("%d facts left after the discard", n)
	}
}

func TestFacts_RefusesBadCommandLinesBeforeOpeningTheDatabase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DB_DSN", "")
	t.Setenv("DB_PATH", filepath.Join(dir, "never.db"))
	t.Setenv(factDigestKeyEnv, "")
	t.Setenv(factDigestKeyPreviousEnv, "")
	for _, c := range []struct {
		args []string
		code int
		says string
	}{
		{nil, factsExitUsage, "Usage:"},
		{[]string{"repair"}, factsExitUsage, "name one of inspect"},
		{[]string{"-h"}, factsExitOK, "controller facts inspect [--device <name> | --device-id <id>]"},
		{[]string{"inspect", "-h"}, factsExitOK, "--discard-verified-history"},
		{[]string{"discard"}, factsExitUsage, "needs --device"},
		{[]string{"truncate", "--device", "web1", "extra"}, factsExitUsage, "unexpected argument"},
		{[]string{"inspect", "--all", "--device", "web1"}, factsExitUsage, "--all"},
		{[]string{"discard", "--device", "web1", "--json"}, factsExitUsage, "flag provided but not defined"},
		{[]string{"inspect"}, factsExitFailed, factDigestKeyPreviousEnv},
	} {
		code, _, errs := runFactsCommand(t, c.args...)
		if code != c.code || !strings.Contains(errs, c.says) {
			t.Errorf("controller facts %v: exit %d, stderr %q; want %d saying %q", c.args, code, errs, c.code, c.says)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*")); len(matches) != 0 {
		t.Errorf("a refused command line created %v", matches)
	}
}

func TestFactsRepair_RefusesAHealthyOrUnknownDevice(t *testing.T) {
	newFactsDB(t)
	t.Setenv(factDigestKeyPreviousEnv, "")
	t.Setenv(factDigestKeyEnv, base64.StdEncoding.EncodeToString(previousDigestSecret))
	if code, _, errs := runFactsCommand(t, "discard", "--device", "web1", "--confirm"); code != factsExitFailed || !strings.Contains(errs, "need no repair") {
		t.Errorf("a healthy device: exit %d, %s", code, errs)
	}
	if code, _, errs := runFactsCommand(t, "discard", "--device", "nope", "--confirm"); code != factsExitFailed || !strings.Contains(errs, "no such device") {
		t.Errorf("an unknown device: exit %d, %s", code, errs)
	}
}
