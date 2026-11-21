// Facts at rest on the Controller (sealed.go, internal/facts' atrest.go): a
// database written through the real store, on SQLite and on PostgreSQL
// through the real migrations, then read with plain SQL and searched for what
// it must not hold; and what the store does with a row that was moved, with
// no keys, and with other keys than the rows were written under.
package entstore_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
)

// plaintexts is what the gathers below store that no row may hold readable:
// each key, parts of it, each value, a field name inside a value, and the
// plain SHA-256 fact hash a build before this one stored.
func plaintexts() []string {
	plainHash, _ := facts.Hash("package.openssh-server", "1:9.6p1-3ubuntu13.5")
	return []string{"package", "openssh", "ubuntu13", "9.6p1", "zq7build", "zq9operator", "zq9sh", "zq9uptime", "424242", "shell", plainHash}
}

// sqliteClientDB is a client over a real SQLite file with one device in it,
// the database handle beside it, and the file's path.
func sqliteClientDB(t *testing.T) (*ent.Client, string, *sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "facts.db")
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", path)
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	dev := client.Device.Create().SetName("web1").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.1"}).SaveX(context.Background())
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return client, dev.DeviceID, db, path
}

// gatherThrice records three gathers of device through store: a package, a
// user and a volatile uptime; the same again; then the package changed.
func gatherThrice(t *testing.T, store *entstore.Store, device string) {
	t.Helper()
	at := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	for i, version := range []string{"1:9.6p1-3ubuntu13.5", "1:9.6p1-3ubuntu13.5", "1:9.9p2-zq7build"} {
		stable := prepared(t, map[string]any{"package.openssh-server": version, "user.zq9operator": map[string]any{"shell": "/bin/zq9sh"}})
		up := prepared(t, map[string]any{"host.zq9uptime": 424242 + i})
		up[0].Kind = facts.KindVolatile
		obs := facts.Observation{Entries: append(stable, up...), At: at.Add(time.Duration(i) * time.Hour),
			By: facts.Provenance{Run: fmt.Sprintf("run%d", i+1), Order: facts.Order{Source: facts.ClockSource, Token: uint64(i + 1)}}}
		if err := store.Record(context.Background(), device, obs); err != nil {
			t.Fatalf("gather %d: %v", i+1, err)
		}
	}
}

// rawRows is every column of every row of table, read with plain SQL, as
// text: what a copy of the database shows.
func rawRows(t *testing.T, db *sql.DB, table string) (string, int) {
	t.Helper()
	// The table name is a constant in every caller.
	rows, err := db.Query("SELECT * FROM " + table)
	if err != nil {
		t.Fatalf("reading %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	n := 0
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
			out.WriteByte('\n')
		}
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out.String(), n
}

// nothingStoredIsPlaintext is the proof both dialects run: after real
// gathers no row of any fact table holds a key, a value or a plain hash; the
// store reads all of it back, saw the one change and only that one, finds a
// key's history by its token, and its chain verifies.
func nothingStoredIsPlaintext(t *testing.T, client *ent.Client, device string, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	store := entstore.New(client, factstest.Keys(), factstest.Envelope())
	gatherThrice(t, store, device)
	counts := map[string]int{}
	for _, table := range []string{"facts", "fact_coverages", "fact_observations"} {
		text, n := rawRows(t, db, table)
		counts[table] = n
		for _, plain := range plaintexts() {
			if strings.Contains(text, plain) {
				t.Errorf("%s holds %q in plaintext", table, plain)
			}
		}
	}
	// The control, and drift: two stable keys once each, the package once
	// more when it changed, and the one volatile row; one coverage for three
	// gathers of the same keys; three observations.
	if counts["facts"] != 4 || counts["fact_coverages"] != 1 || counts["fact_observations"] != 3 {
		t.Fatalf("rows = %v; want 4 facts (a changed value writes a row, an unchanged one none), 1 coverage and 3 observations", counts)
	}
	token, _ := factstest.Keys().Token(device, "package.openssh-server")
	if text, _ := rawRows(t, db, "facts"); !strings.Contains(text, token) || !strings.Contains(text, factstest.Keys().ID()) {
		t.Error("the rows do not carry the key's token and the keys' identity")
	}
	if text, _ := rawRows(t, db, "fact_coverages"); !strings.Contains(text, token) {
		t.Error("the coverage does not list the key's token")
	}
	current, err := store.Current(ctx, device)
	if err != nil || len(current) != 3 {
		t.Fatalf("Current = %+v, %v", current, err)
	}
	for _, e := range current {
		if want, _ := factstest.Keys().Hash(device, e.Key, e.Value); e.Hash != want {
			t.Errorf("%s reads back with a hash that is not its keyed hash", e.Key)
		}
		if e.LastBy.Run != "run3" {
			t.Errorf("%s last confirmed by %q, want run3: a coverage of tokens still confirms", e.Key, e.LastBy.Run)
		}
	}
	if h, err := store.History(ctx, device, "package.openssh-server"); err != nil || len(h) != 2 || h[0].Value != "1:9.6p1-3ubuntu13.5" || h[1].Value != "1:9.9p2-zq7build" {
		t.Fatalf("History(package) = %+v, %v; want the two versions", h, err)
	}
	if h, err := store.History(ctx, device, "user.zq9operator"); err != nil || len(h) != 1 || h[0].By.Run != "run1" {
		t.Fatalf("History(user) = %+v, %v; want one value, first seen by run1", h, err)
	}
	if h, err := store.History(ctx, device, "package.never-stored"); err != nil || len(h) != 0 {
		t.Fatalf("History of a key nothing holds = %+v, %v", h, err)
	}
	if v, err := store.Verify(ctx, device); err != nil || v.Break != nil || v.Values != 3 {
		t.Fatalf("Verify = %+v, %v; want three chained values", v, err)
	}
}

// TestStore_NothingStoredIsPlaintext: on SQLite, in the rows and in the
// bytes of the database's own files.
func TestStore_NothingStoredIsPlaintext(t *testing.T) {
	client, device, db, path := sqliteClientDB(t)
	nothingStoredIsPlaintext(t, client, device, db)
	searched := 0
	for _, file := range []string{path, path + "-wal"} {
		raw, err := os.ReadFile(file) // #nosec G304 -- the test's own database
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		searched += len(raw)
		for _, plain := range plaintexts() {
			if bytes.Contains(raw, []byte(plain)) {
				t.Errorf("%s holds %q in plaintext", filepath.Base(file), plain)
			}
		}
	}
	if searched == 0 {
		t.Fatal("the control failed: no database file was there to search")
	}
}

// TestStore_NothingStoredIsPlaintextOnPostgres: the same rows on PostgreSQL,
// through the migrations a Controller applies.
func TestStore_NothingStoredIsPlaintextOnPostgres(t *testing.T) {
	client, device, db := postgresClientDB(t)
	nothingStoredIsPlaintext(t, client, device, db)
}

// aMovedRowDoesNotOpen is the proof both dialects run: a stable row copied
// to another device with plain SQL does not read as that device's fact and
// breaks its chain check, and a payload put on another row of the same
// device does not open there.
func aMovedRowDoesNotOpen(t *testing.T, client *ent.Client, device string, db *sql.DB, placeholder func(n int) string) {
	t.Helper()
	ctx := context.Background()
	store := entstore.New(client, factstest.Keys(), factstest.Envelope())
	gatherThrice(t, store, device)
	other := client.Device.Create().SetName("web2").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.2"}).SaveX(ctx)
	columns := "created_at, updated_at, key_token, payload, hash, keyed_by, kind, provenance, observation_seq, chain"
	move := "INSERT INTO facts (" + columns + ", device_facts) SELECT " + columns + ", " + placeholder(1) +
		" FROM facts WHERE id = (SELECT min(id) FROM facts WHERE kind = 'stable')"
	if _, err := db.ExecContext(ctx, move, other.ID); err != nil {
		t.Fatalf("copying a row to another device: %v", err)
	}
	if got, err := store.Current(ctx, other.DeviceID); !errors.Is(err, entstore.ErrNotThisDevices) {
		t.Errorf("another device's row read as web2's: %+v, %v; want ErrNotThisDevices", got, err)
	}
	if v, err := store.Verify(ctx, other.DeviceID); err != nil || v.Break == nil || v.Break.Index != 0 || !strings.Contains(v.Break.Reason, "does not open") {
		t.Errorf("another device's row verified as web2's: %+v, %v", v, err)
	}
	// The device it came from is untouched.
	if current, err := store.Current(ctx, device); err != nil || len(current) != 3 {
		t.Fatalf("the first device after the copy: %+v, %v", current, err)
	}
	// One row's sealed payload, put on another row of the same device: on a
	// row of another key it does not open at all, and on a row of the same
	// key (an older value put back as the newest) it opens and is refused
	// for the value its row's keyed hash does not describe.
	packageToken, _ := factstest.Keys().Token(device, "package.openssh-server")
	userToken, _ := factstest.Keys().Token(device, "user.zq9operator")
	rowOf := func(token, pick string) (int, string) {
		t.Helper()
		var id int
		var payload string
		if err := db.QueryRowContext(ctx, "SELECT id, payload FROM facts WHERE id = (SELECT "+pick+"(id) FROM facts WHERE key_token = "+placeholder(1)+" AND device_facts <> "+placeholder(2)+")", token, other.ID).Scan(&id, &payload); err != nil {
			t.Fatal(err)
		}
		return id, payload
	}
	put := func(id int, payload string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, "UPDATE facts SET payload = "+placeholder(1)+" WHERE id = "+placeholder(2), payload, id); err != nil {
			t.Fatal(err)
		}
	}
	oldID, oldPayload := rowOf(packageToken, "min")
	newID, newPayload := rowOf(packageToken, "max")
	userID, userPayload := rowOf(userToken, "min")
	if oldID == newID {
		t.Fatal("the package has one row, want its two versions")
	}
	put(newID, oldPayload)
	if _, err := store.Current(ctx, device); !errors.Is(err, entstore.ErrNotThisDevices) || !strings.Contains(err.Error(), "keyed hash") {
		t.Errorf("an older value's payload put on the newest row: %v; want it refused by its keyed hash", err)
	}
	if v, err := store.Verify(ctx, device); err != nil || v.Break == nil {
		t.Errorf("an older value's payload put on the newest row verified: %+v, %v", v, err)
	}
	put(newID, newPayload)
	if _, err := store.Current(ctx, device); err != nil {
		t.Fatalf("the control failed: the rows put back as they were do not read: %v", err)
	}
	put(userID, oldPayload)
	if _, err := store.History(ctx, device, "user.zq9operator"); !errors.Is(err, entstore.ErrNotThisDevices) || strings.Contains(err.Error(), "keyed hash") {
		t.Errorf("another key's payload put on a row: %v; want it not to open at all", err)
	}
	put(userID, userPayload)
	// A row that is not sealed at all, as a writer with no seal would leave.
	put(userID, `{"key":"user.zq9operator","json":"\"plain\""}`)
	if _, err := store.Current(ctx, device); !errors.Is(err, entstore.ErrNotThisDevices) || !errors.Is(err, crypto.ErrFactNotSealed) {
		t.Errorf("a plaintext row read: %v; want it refused as not sealed", err)
	}
}

// TestStore_AMovedRowDoesNotOpen: on SQLite.
func TestStore_AMovedRowDoesNotOpen(t *testing.T) {
	client, device, db, _ := sqliteClientDB(t)
	aMovedRowDoesNotOpen(t, client, device, db, func(int) string { return "?" })
}

// TestStore_AMovedRowDoesNotOpenOnPostgres: on PostgreSQL.
func TestStore_AMovedRowDoesNotOpenOnPostgres(t *testing.T) {
	client, device, db := postgresClientDB(t)
	aMovedRowDoesNotOpen(t, client, device, db, func(n int) string { return fmt.Sprintf("$%d", n) })
}

// TestStore_WithoutItsKeysStoresAndReadsNothing: a store missing its keys or
// its envelope service refuses every record and every read with ErrNoKeys
// and writes no row, and a consumer over such a store acknowledges a batch
// and stores nothing rather than redeliver it forever or keep it readable.
func TestStore_WithoutItsKeysStoresAndReadsNothing(t *testing.T) {
	ctx := context.Background()
	client, device, db, _ := sqliteClientDB(t)
	obs := facts.Observation{Entries: prepared(t, map[string]any{"package.openssh-server": "9.6"}), At: time.Now()}
	unreadable := facts.NewLazyKeys(func() ([]byte, error) { return nil, errors.New("no secret") }, "controller")
	for name, store := range map[string]*entstore.Store{
		"no keys":             entstore.New(client, nil, factstest.Envelope()),
		"keys with no secret": entstore.New(client, unreadable, factstest.Envelope()),
		"no envelope":         entstore.New(client, factstest.Keys(), nil),
	} {
		if err := store.Record(ctx, device, obs); !errors.Is(err, facts.ErrNoKeys) {
			t.Errorf("%s: Record = %v, want ErrNoKeys", name, err)
		}
		if _, err := store.Current(ctx, device); !errors.Is(err, facts.ErrNoKeys) {
			t.Errorf("%s: Current = %v, want ErrNoKeys", name, err)
		}
		if _, err := store.History(ctx, device, "package.openssh-server"); !errors.Is(err, facts.ErrNoKeys) {
			t.Errorf("%s: History = %v, want ErrNoKeys", name, err)
		}
		if _, err := store.Verify(ctx, device); !errors.Is(err, facts.ErrNoKeys) {
			t.Errorf("%s: Verify = %v, want ErrNoKeys", name, err)
		}
		var logged bytes.Buffer
		sub := entstore.NewSubscriber(store, slog.New(slog.NewTextHandler(&logged, nil)), nil)
		if err := handle(t, sub, facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(), Entries: obs.Entries}); err != nil {
			t.Errorf("%s: the consumer returned %v, want the batch acknowledged", name, err)
		}
		if !strings.Contains(logged.String(), "level=ERROR") || !strings.Contains(logged.String(), "PLEIADES_FACTS_DIGEST_KEY") {
			t.Errorf("%s: the consumer did not say what it dropped and why:\n%s", name, logged.String())
		}
	}
	for _, table := range []string{"facts", "fact_coverages", "fact_observations"} {
		if _, n := rawRows(t, db, table); n != 0 {
			t.Errorf("%s holds %d row(s) written with no key", table, n)
		}
	}
}

// TestStore_RefusesADeviceSealedUnderOtherKeys: a change of the facts secret
// with no previous key kept beside it (rekey.go) is refused. A store holding
// other keys than a device's rows were written under refuses the device,
// read and write, and writes nothing beside the history it cannot join; the
// consumer drops the batch. A device with no rows yet is served.
func TestStore_RefusesADeviceSealedUnderOtherKeys(t *testing.T) {
	ctx := context.Background()
	client, device, db, _ := sqliteClientDB(t)
	gatherThrice(t, entstore.New(client, factstest.Keys(), factstest.Envelope()), device)
	_, before := rawRows(t, db, "facts")
	otherKeys, err := entstore.AtRestKeys(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if otherKeys.ID() == factstest.Keys().ID() || !strings.HasPrefix(otherKeys.ID(), "controller:") {
		t.Fatalf("the other keys' identity: %q", otherKeys.ID())
	}
	changed := entstore.New(client, otherKeys, factstest.Envelope())
	obs := facts.Observation{Entries: prepared(t, map[string]any{"package.openssh-server": "9.9"}), At: time.Now()}
	if err := changed.Record(ctx, device, obs); !errors.Is(err, entstore.ErrKeysChanged) {
		t.Errorf("Record under other keys = %v, want ErrKeysChanged", err)
	}
	if _, err := changed.Current(ctx, device); !errors.Is(err, entstore.ErrKeysChanged) {
		t.Errorf("Current under other keys = %v, want ErrKeysChanged", err)
	}
	if _, err := changed.History(ctx, device, "package.openssh-server"); !errors.Is(err, entstore.ErrKeysChanged) {
		t.Errorf("History under other keys = %v, want ErrKeysChanged", err)
	}
	if _, err := changed.Verify(ctx, device); !errors.Is(err, entstore.ErrKeysChanged) {
		t.Errorf("Verify under other keys = %v, want ErrKeysChanged", err)
	}
	sub := entstore.NewSubscriber(changed, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := handle(t, sub, facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(), Entries: obs.Entries}); err != nil {
		t.Errorf("the consumer returned %v, want the batch acknowledged", err)
	}
	if _, after := rawRows(t, db, "facts"); after != before {
		t.Errorf("%d row(s) were written under other keys beside the device's history", after-before)
	}
	// A device with no history has nothing to disagree with.
	fresh := client.Device.Create().SetName("web9").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.9"}).SaveX(ctx)
	if err := changed.Record(ctx, fresh.DeviceID, obs); err != nil {
		t.Errorf("a device with no rows, under the other keys: %v", err)
	}
	// A row sealed under another master key does not open, and says so.
	strangerKey, err := crypto.NewEnvelopeService(bytes.Repeat([]byte{5}, 32), "v1", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entstore.New(client, factstest.Keys(), strangerKey).Current(ctx, device); !errors.Is(err, entstore.ErrNotThisDevices) {
		t.Errorf("Current under another master key = %v, want ErrNotThisDevices", err)
	}
}

// TestStore_ALateChangeOpensOnlyTheValueItResumes: a record compares rows
// without opening them, and opens one only to write a resumed value again
// (FAILURE_PATTERNS 923): the value comes back whole through the seal.
func TestStore_ALateChangeOpensOnlyTheValueItResumes(t *testing.T) {
	ctx := context.Background()
	client, device, _, _ := sqliteClientDB(t)
	store := entstore.New(client, factstest.Keys(), factstest.Envelope())
	t0 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	record := func(value any, token uint64, at time.Time) {
		t.Helper()
		obs := facts.Observation{Entries: prepared(t, map[string]any{"user.zq9operator": value}), At: at,
			By: facts.Provenance{Run: fmt.Sprintf("run%d", token), Order: facts.Order{Source: facts.ClockSource, Token: token}}}
		if err := store.Record(ctx, device, obs); err != nil {
			t.Fatal(err)
		}
	}
	a := map[string]any{"shell": "/bin/zq9sh", "uid": 1001}
	record(a, 1, t0)
	record(a, 11, t0.Add(5*time.Hour))
	// Observed between the two, stored after them.
	record(map[string]any{"shell": "/bin/other"}, 10, t0.Add(time.Hour))
	history, err := store.History(ctx, device, "user.zq9operator")
	if err != nil || len(history) != 3 {
		t.Fatalf("History = %+v, %v; want A, the late B, then A resumed", history, err)
	}
	resumed, _ := history[2].Value.(map[string]any)
	if resumed["shell"] != "/bin/zq9sh" || fmt.Sprint(resumed["uid"]) != "1001" || history[2].By.Run != "run11" {
		t.Errorf("the resumed value = %+v by %q, want A whole, as run11 confirmed it", history[2].Value, history[2].By.Run)
	}
	if v, err := store.Verify(ctx, device); err != nil || v.Break != nil || v.Values != 3 {
		t.Errorf("Verify = %+v, %v", v, err)
	}
}
