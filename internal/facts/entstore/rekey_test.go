// A change of the facts key with both keys live (rekey.go): a database
// written through the real store under one key, then read and written by a
// store holding a new key with the old one as previous, on SQLite and on
// PostgreSQL through the real migrations; what the database holds after,
// read with plain SQL; a re-key a database failure stops part-way; a third
// key; and a history that does not verify.
package entstore_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
)

// newFactKeys is the at-rest keys a Controller derives from a secret of 32
// bytes of fill: the keys a change of the facts key moves to.
func newFactKeys(t *testing.T, fill byte) *facts.Keys {
	t.Helper()
	keys, err := entstore.AtRestKeys(bytes.Repeat([]byte{fill}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// rotationKeys names every fact gatherRotationHistory stores.
var rotationKeys = []string{"package.openssh-server", "user.zq9operator", "host.zq9uptime", "file.zq9shadow"}

// shadowValue is a private file's fact as the digest keyer leaves it: a
// keyed digest and the keyer's identity, whose raw digest is not stored.
func shadowValue() map[string]any {
	return map[string]any{"state": "present", "world_readable": false, "hmac_sha256": strings.Repeat("ab", 32), "hmac_key": "controller:00112233aabbccdd"}
}

// rotationT0 is when gatherRotationHistory's first gather was made.
var rotationT0 = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

// recordGather records one gather of device through store: stable's facts,
// and the volatile uptime when uptime is not nil, made by run at at with
// run order token.
func recordGather(t *testing.T, store *entstore.Store, device, run string, token uint64, at time.Time, stable map[string]any, uptime any) error {
	t.Helper()
	entries := prepared(t, stable)
	if uptime != nil {
		up := prepared(t, map[string]any{"host.zq9uptime": uptime})
		up[0].Kind = facts.KindVolatile
		entries = append(entries, up...)
	}
	obs := facts.Observation{Entries: entries, At: at, By: facts.Provenance{Run: run, Order: facts.Order{Source: facts.ClockSource, Token: token}}}
	return store.Record(context.Background(), device, obs)
}

// gatherRotationHistory records, through store, the history every re-key
// test reads back: a package that changes, a user, a volatile uptime, a
// private file holding a keyed digest, and a late change of the user, which
// writes the user's value again after it (facts.Resumed), so the history's
// order is not its run order. That is seven rows, six of them chained,
// three coverages and four observations.
func gatherRotationHistory(t *testing.T, store *entstore.Store, device string) {
	t.Helper()
	a := map[string]any{"shell": "/bin/zq9sh", "uid": 1001}
	for _, g := range []struct {
		run    string
		token  uint64
		at     time.Time
		stable map[string]any
		uptime any
	}{
		{"run1", 10, rotationT0, map[string]any{"package.openssh-server": "1:9.6p1-3ubuntu13.5", "user.zq9operator": a}, uptime(0)},
		{"run2", 20, rotationT0.Add(time.Hour), map[string]any{"package.openssh-server": "1:9.6p1-3ubuntu13.5", "user.zq9operator": a}, uptime(1)},
		{"run3", 30, rotationT0.Add(2 * time.Hour), map[string]any{"package.openssh-server": "1:9.9p2-zq7build", "user.zq9operator": a, "file.zq9shadow": shadowValue()}, uptime(2)},
		// Observed between run2 and run3, stored after them.
		{"run4", 25, rotationT0.Add(90 * time.Minute), map[string]any{"user.zq9operator": map[string]any{"shell": "/bin/zq9late"}}, nil},
	} {
		if err := recordGather(t, store, device, g.run, g.token, g.at, g.stable, g.uptime); err != nil {
			t.Fatalf("%s: %v", g.run, err)
		}
	}
}

// rotationSnapshot is everything a store reads back of a device: its current
// facts and each key's history, every hash left out (a re-key changes the
// keyed hash and nothing else an entry holds), and how many chained values
// its chain check verified.
type rotationSnapshot struct {
	Current []facts.Entry
	History map[string][]facts.Entry
	Values  int
}

// snapshotOf reads device back through store; a read that fails, or a chain
// that does not verify, fails the test.
func snapshotOf(t *testing.T, store *entstore.Store, device string) rotationSnapshot {
	t.Helper()
	ctx := context.Background()
	current, err := store.Current(ctx, device)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	snap := rotationSnapshot{Current: withoutHashes(current), History: map[string][]facts.Entry{}}
	for _, key := range rotationKeys {
		history, err := store.History(ctx, device, key)
		if err != nil {
			t.Fatalf("History(%s): %v", key, err)
		}
		snap.History[key] = withoutHashes(history)
	}
	verdict, err := store.Verify(ctx, device)
	if err != nil || verdict.Break != nil {
		t.Fatalf("Verify = %+v, %v", verdict, err)
	}
	snap.Values = verdict.Values
	return snap
}

// withoutHashes is entries with each hash cleared, as a new slice.
func withoutHashes(entries []facts.Entry) []facts.Entry {
	out := make([]facts.Entry, len(entries))
	for i, e := range entries {
		e.Hash = ""
		out[i] = e
	}
	return out
}

// sameSnapshot fails the test when got is not want.
func sameSnapshot(t *testing.T, what string, got, want rotationSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s reads back otherwise than before:\n got %+v\nwant %+v", what, got, want)
	}
}

// rawQuery is every column of every row query returns, read with plain SQL,
// as text.
func rawQuery(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for rows.Next() {
		values := make([]any, len(cols))
		targets := make([]any, len(cols))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for _, v := range values {
			if b, ok := v.([]byte); ok {
				out.Write(b)
			} else {
				fmt.Fprint(&out, v)
			}
			out.WriteByte('|')
		}
		out.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// rawFactTables is every row of the three fact tables, in id order: what a
// copy of the database shows.
func rawFactTables(t *testing.T, db *sql.DB) string {
	t.Helper()
	var out strings.Builder
	for _, table := range []string{"facts", "fact_coverages", "fact_observations"} {
		// The table names are this function's constants.
		out.WriteString(rawQuery(t, db, "SELECT * FROM "+table+" ORDER BY id"))
	}
	return out.String()
}

// unkeyedColumns is what a re-key must leave exactly as it was: each fact's
// id, device, first sighting, kind, provenance and observation, and each
// observation's whole row but the coverage digest it names.
func unkeyedColumns(t *testing.T, db *sql.DB) string {
	t.Helper()
	return rawQuery(t, db, "SELECT id, device_facts, created_at, kind, provenance, observation_seq FROM facts ORDER BY id") +
		rawQuery(t, db, "SELECT id, device_id, seq, observed_at, order_source, order_token, provenance FROM fact_observations ORDER BY id") +
		rawQuery(t, db, "SELECT count(*) FROM fact_coverages")
}

// keyedColumns is every value the database holds that the facts key made:
// each fact's token, keyed hash and chain link, and each coverage's tokens
// and digest.
func keyedColumns(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(rawQuery(t, db, "SELECT key_token, hash, chain, digest FROM facts, fact_coverages"), "\n") {
		for _, v := range strings.Split(line, "|") {
			// A volatile row's chain is null, and the empty first link is "".
			if len(v) == 64 {
				out = append(out, v)
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("the control failed: the database holds nothing the facts key made")
	}
	return out
}

// aRekeyKeepsTheHistory is the proof both dialects run: a device's history
// stored under one key reads back exactly as it was through a store holding
// a new key and the old one as previous, which re-keys it on that first read;
// the database then holds no token, keyed hash, chain link or digest of the
// old key, no plaintext, and nothing that opens under the old key, with every
// row, time and provenance as it was; and a gather after the re-key, with the
// database refusing any UPDATE of a stored fact, confirms the values it
// re-keyed without a new history row.
func aRekeyKeepsTheHistory(t *testing.T, client *ent.Client, device string, db *sql.DB, refusals []string) {
	t.Helper()
	ctx := context.Background()
	old := factstest.Keys()
	gatherRotationHistory(t, entstore.New(client, old, factstest.Envelope()), device)
	before := snapshotOf(t, entstore.New(client, old, factstest.Envelope()), device)
	if before.Values != 6 || len(before.History["user.zq9operator"]) != 3 {
		t.Fatalf("the control failed: the history before the re-key is %+v", before)
	}
	unkeyed, keyed := unkeyedColumns(t, db), keyedColumns(t, db)

	rotated := entstore.New(client, newFactKeys(t, 7), factstest.Envelope(), entstore.WithPreviousKeys(old))
	sameSnapshot(t, "the device after the re-key", snapshotOf(t, rotated, device), before)

	if got := unkeyedColumns(t, db); got != unkeyed {
		t.Errorf("the re-key changed a time, a provenance or a row count:\nbefore\n%s\nafter\n%s", unkeyed, got)
	}
	tables := rawFactTables(t, db)
	for _, v := range keyed {
		if strings.Contains(tables, v) {
			t.Errorf("the database still holds %s..., which the previous key made", v[:12])
		}
	}
	for _, plain := range append(plaintexts(), "zq9late", "zq9shadow") {
		if strings.Contains(tables, plain) {
			t.Errorf("the database holds %q in plaintext", plain)
		}
	}
	if got := rawQuery(t, db, "SELECT DISTINCT keyed_by FROM facts"); got != newFactKeys(t, 7).ID()+"|\n" {
		t.Errorf("the rows name the keys %q, want the new keys alone", got)
	}
	// Nothing opens under the previous keys: the old store refuses the
	// device, and no payload opens under its key's old token.
	if _, err := entstore.New(client, old, factstest.Envelope()).Current(ctx, device); !errors.Is(err, entstore.ErrKeysChanged) {
		t.Errorf("the previous keys alone read the re-keyed device: %v", err)
	}
	opened := 0
	for _, line := range strings.Split(strings.TrimSpace(rawQuery(t, db, "SELECT key_token, payload FROM facts")), "\n") {
		token, text, _ := strings.Cut(line, "|")
		var payload map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSuffix(text, "|")), &payload); err != nil {
			t.Fatalf("a payload is not JSON: %v", err)
		}
		inside, err := crypto.OpenFactPayload(factstest.Envelope(), token, payload)
		if err != nil {
			t.Fatalf("a re-keyed payload does not open under its own token: %v", err)
		}
		key, _ := inside["key"].(string)
		oldToken, _ := old.Token(device, key)
		if _, err := crypto.OpenFactPayload(factstest.Envelope(), oldToken, payload); err == nil {
			t.Errorf("a re-keyed payload of %s still opens under its old token", key)
		}
		opened++
	}
	if opened != 7 {
		t.Fatalf("opened %d payloads, want the seven rows", opened)
	}

	// After the re-key nothing is updated again: with the database refusing
	// any UPDATE of a stored fact, a gather confirming every value stores.
	for _, stmt := range refusals {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	a := map[string]any{"shell": "/bin/zq9sh", "uid": 1001}
	if err := recordGather(t, rotated, device, "run5", 40, rotationT0.Add(3*time.Hour),
		map[string]any{"package.openssh-server": "1:9.9p2-zq7build", "user.zq9operator": a, "file.zq9shadow": shadowValue()}, uptime(3)); err != nil {
		t.Fatalf("a gather after the re-key: %v", err)
	}
	after := snapshotOf(t, rotated, device)
	for key, n := range map[string]int{"package.openssh-server": 2, "user.zq9operator": 3, "file.zq9shadow": 1} {
		h := after.History[key]
		if len(h) != n || h[len(h)-1].LastBy.Run != "run5" {
			t.Errorf("History(%s) = %+v; want %d rows, the newest confirmed by run5 across the re-key", key, h, n)
		}
	}
	if after.Values != 6 {
		t.Errorf("Verify after the gather checked %d values, want the same six", after.Values)
	}
}

// TestRekey_KeepsTheHistoryUnderTheNewKey: on SQLite.
func TestRekey_KeepsTheHistoryUnderTheNewKey(t *testing.T) {
	client, device, db, _ := sqliteClientDB(t)
	aRekeyKeepsTheHistory(t, client, device, db, sqliteRefusals)
}

// TestRekey_KeepsTheHistoryUnderTheNewKeyOnPostgres: on PostgreSQL, through
// the migrations a Controller applies.
func TestRekey_KeepsTheHistoryUnderTheNewKeyOnPostgres(t *testing.T) {
	client, device, db := postgresClientDB(t)
	aRekeyKeepsTheHistory(t, client, device, db, postgresRefusals)
}

// TestRekey_AWriteRekeysFirst: a device's first touch after the change is a
// gather. The device is re-keyed first, in a transaction of its own, the
// gather then confirms what was stored under the previous key, and nothing
// reads as changed.
func TestRekey_AWriteRekeysFirst(t *testing.T) {
	client, device, db, _ := sqliteClientDB(t)
	old := factstest.Keys()
	gatherRotationHistory(t, entstore.New(client, old, factstest.Envelope()), device)
	before := snapshotOf(t, entstore.New(client, old, factstest.Envelope()), device)
	rotated := entstore.New(client, newFactKeys(t, 7), factstest.Envelope(), entstore.WithPreviousKeys(old))
	a := map[string]any{"shell": "/bin/zq9sh", "uid": 1001}
	if err := recordGather(t, rotated, device, "run5", 40, rotationT0.Add(3*time.Hour),
		map[string]any{"package.openssh-server": "1:9.9p2-zq7build", "user.zq9operator": a}, nil); err != nil {
		t.Fatalf("the first gather after the change: %v", err)
	}
	if got := rawQuery(t, db, "SELECT count(*) FROM facts"); got != "7|\n" {
		t.Errorf("the gather wrote a row: %s facts, want the seven it confirmed", got)
	}
	after := snapshotOf(t, rotated, device)
	if h := after.History["package.openssh-server"]; len(h) != 2 || h[1].LastBy.Run != "run5" {
		t.Errorf("History(package) = %+v; want two values, the newest confirmed by run5", h)
	}
	// Everything but the two confirmations reads as it did.
	after.History["package.openssh-server"], after.History["user.zq9operator"] = before.History["package.openssh-server"], before.History["user.zq9operator"]
	after.Current = before.Current
	sameSnapshot(t, "the device after a gather re-keyed it", after, before)
}

// aRekeyInterruptedLeavesTheOldKeys is the proof both dialects run: a re-key
// the database fails part-way, after every fact row was rewritten and at the
// first coverage, leaves every byte of every fact table as it was, and the
// previous key still reads the device whole. Once the failure is gone, the
// next read re-keys it.
func aRekeyInterruptedLeavesTheOldKeys(t *testing.T, client *ent.Client, device string, db *sql.DB, interrupt, resume []string) {
	t.Helper()
	ctx := context.Background()
	old := factstest.Keys()
	gatherRotationHistory(t, entstore.New(client, old, factstest.Envelope()), device)
	before := snapshotOf(t, entstore.New(client, old, factstest.Envelope()), device)
	raw := rawFactTables(t, db)
	for _, stmt := range interrupt {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	rotated := entstore.New(client, newFactKeys(t, 7), factstest.Envelope(), entstore.WithPreviousKeys(old))
	if _, err := rotated.Current(ctx, device); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("Current with the re-key interrupted = %v, want the database's refusal", err)
	}
	if err := recordGather(t, rotated, device, "run5", 40, rotationT0.Add(3*time.Hour), map[string]any{"package.openssh-server": "9.9"}, nil); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("a gather with the re-key interrupted = %v, want the database's refusal", err)
	}
	if got := rawFactTables(t, db); got != raw {
		t.Fatalf("an interrupted re-key changed the database:\nbefore\n%s\nafter\n%s", raw, got)
	}
	sameSnapshot(t, "the device read under the previous key after an interrupted re-key", snapshotOf(t, entstore.New(client, old, factstest.Envelope()), device), before)
	for _, stmt := range resume {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	sameSnapshot(t, "the device re-keyed once the failure was gone", snapshotOf(t, rotated, device), before)
}

// TestRekey_AnInterruptedRekeyLeavesTheOldKeys: on SQLite, where a trigger
// aborts the first coverage UPDATE and the transaction stays open, so only a
// rollback keeps the fact rows already rewritten from being committed.
func TestRekey_AnInterruptedRekeyLeavesTheOldKeys(t *testing.T) {
	client, device, db, _ := sqliteClientDB(t)
	aRekeyInterruptedLeavesTheOldKeys(t, client, device, db,
		[]string{"CREATE TRIGGER rekey_interrupted BEFORE UPDATE ON fact_coverages BEGIN SELECT RAISE(ABORT, 'the re-key was interrupted'); END"},
		[]string{"DROP TRIGGER rekey_interrupted"})
}

// TestRekey_AnInterruptedRekeyLeavesTheOldKeysOnPostgres: on PostgreSQL.
func TestRekey_AnInterruptedRekeyLeavesTheOldKeysOnPostgres(t *testing.T) {
	client, device, db := postgresClientDB(t)
	aRekeyInterruptedLeavesTheOldKeys(t, client, device, db,
		[]string{
			"CREATE FUNCTION interrupt_rekey() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'the re-key was interrupted'; END $$",
			"CREATE TRIGGER rekey_interrupted BEFORE UPDATE ON fact_coverages FOR EACH ROW EXECUTE FUNCTION interrupt_rekey()",
		},
		[]string{"DROP TRIGGER rekey_interrupted ON fact_coverages"})
}

// TestRekey_AThirdKeyIsRefused: a store holding neither the key a device's
// facts were stored under nor that key as previous refuses the device, read
// and write, and changes nothing.
func TestRekey_AThirdKeyIsRefused(t *testing.T) {
	ctx := context.Background()
	client, device, db, _ := sqliteClientDB(t)
	gatherRotationHistory(t, entstore.New(client, factstest.Keys(), factstest.Envelope()), device)
	raw := rawFactTables(t, db)
	third := entstore.New(client, newFactKeys(t, 3), factstest.Envelope(), entstore.WithPreviousKeys(newFactKeys(t, 2)))
	if _, err := third.Current(ctx, device); !errors.Is(err, entstore.ErrKeysChanged) {
		t.Errorf("Current = %v, want ErrKeysChanged", err)
	}
	if _, err := third.History(ctx, device, "package.openssh-server"); !errors.Is(err, entstore.ErrKeysChanged) {
		t.Errorf("History = %v, want ErrKeysChanged", err)
	}
	if _, err := third.Verify(ctx, device); !errors.Is(err, entstore.ErrKeysChanged) {
		t.Errorf("Verify = %v, want ErrKeysChanged", err)
	}
	if err := recordGather(t, third, device, "run5", 40, rotationT0.Add(3*time.Hour), map[string]any{"package.openssh-server": "9.9"}, nil); !errors.Is(err, entstore.ErrKeysChanged) {
		t.Errorf("Record = %v, want ErrKeysChanged", err)
	}
	if count, err := third.RekeyAll(ctx); err != nil || count.Devices != 0 || count.Remaining != 0 {
		t.Errorf("RekeyAll = %+v, %v; want nothing found under its previous keys", count, err)
	}
	if got := rawFactTables(t, db); got != raw {
		t.Error("a store holding a third key changed the database")
	}
}

// TestRekey_AHistoryThatDoesNotVerifyIsNotRekeyed: a stored fact whose time
// was edited still opens (its keyed hash covers its key and value) and
// breaks the chain under the previous key. Re-keying it would make the edit
// read as whole under the new key, so the device is refused, read and
// write, and left exactly as it was; so is one whose rows name both keys.
func TestRekey_AHistoryThatDoesNotVerifyIsNotRekeyed(t *testing.T) {
	ctx := context.Background()
	client, device, db, _ := sqliteClientDB(t)
	old := factstest.Keys()
	gatherRotationHistory(t, entstore.New(client, old, factstest.Envelope()), device)
	if _, err := db.ExecContext(ctx, "UPDATE facts SET created_at = ? WHERE id = (SELECT min(id) FROM facts WHERE kind = 'stable')", rotationT0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if v, err := entstore.New(client, old, factstest.Envelope()).Verify(ctx, device); err != nil || v.Break == nil {
		t.Fatalf("the control failed: the edited history verifies under its own key: %+v, %v", v, err)
	}
	raw := rawFactTables(t, db)
	rotated := entstore.New(client, newFactKeys(t, 7), factstest.Envelope(), entstore.WithPreviousKeys(old))
	if _, err := rotated.Current(ctx, device); !errors.Is(err, entstore.ErrRekeyRefused) || !strings.Contains(err.Error(), "chain breaks at chain index 0") {
		t.Errorf("Current = %v, want ErrRekeyRefused naming the break", err)
	}
	if err := recordGather(t, rotated, device, "run5", 40, rotationT0.Add(3*time.Hour), map[string]any{"package.openssh-server": "9.9"}, nil); !errors.Is(err, entstore.ErrRekeyRefused) {
		t.Errorf("Record = %v, want ErrRekeyRefused", err)
	}
	// The consumer acknowledges such a batch and says why: a redelivery
	// would be refused the same way.
	var logged bytes.Buffer
	sub := entstore.NewSubscriber(rotated, slog.New(slog.NewTextHandler(&logged, nil)), nil)
	batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(), Entries: prepared(t, map[string]any{"package.openssh-server": "9.9"})}
	if err := handle(t, sub, batch); err != nil {
		t.Errorf("the consumer returned %v, want the batch acknowledged", err)
	}
	if !strings.Contains(logged.String(), "level=ERROR") || !strings.Contains(logged.String(), "do not verify") {
		t.Errorf("the consumer did not say what it dropped and why:\n%s", logged.String())
	}
	if got := rawFactTables(t, db); got != raw {
		t.Error("a refused re-key changed the database")
	}

	// A device whose rows name both keys: one row put back under the
	// previous key's name after a re-key.
	fresh := client.Device.Create().SetName("web2").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.2"}).SaveX(ctx)
	gatherRotationHistory(t, entstore.New(client, old, factstest.Envelope()), fresh.DeviceID)
	if _, err := rotated.Current(ctx, fresh.DeviceID); err != nil {
		t.Fatalf("re-keying the second device: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE facts SET keyed_by = ? WHERE id = (SELECT max(id) FROM facts WHERE device_facts = ?)", old.ID(), fresh.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.Current(ctx, fresh.DeviceID); !errors.Is(err, entstore.ErrRekeyRefused) || !strings.Contains(err.Error(), "name the current key") {
		t.Errorf("Current of a device under both keys = %v, want ErrRekeyRefused", err)
	}
}
