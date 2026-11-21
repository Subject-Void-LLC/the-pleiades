// Tests that the single-fact route (GET /inventory/devices/{name}/facts/{key},
// device_facts.go) reads the sealed store the Controller serves
// (internal/facts/entstore, on a real SQLite file) by the key's token: the
// stored key comes back with its values in plaintext, a key nothing stored
// comes back empty, one device's key never answers with another's values,
// and the facts table itself holds neither the key's name nor any value.
package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// sealedHistoryKey is the fact both devices hold: a name the raw table must
// never show.
const sealedHistoryKey = "package.zq7velvetpkg"

// recordSealedValue stores value under key on device as one observation at
// at, through the store's own Record.
func recordSealedValue(t *testing.T, store facts.Store, device, key, value string, at time.Time) {
	t.Helper()
	hash, err := facts.Hash(key, value)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record(context.Background(), device, facts.Observation{
		Entries: []facts.Entry{{Key: key, Value: value, Hash: hash, Kind: facts.KindStable}}, At: at}); err != nil {
		t.Fatal(err)
	}
}

// historyAnswer is the single-fact route's body, decoded.
type historyAnswer struct {
	Device string        `json:"device"`
	Key    string        `json:"key"`
	Facts  []facts.Entry `json:"facts"`
}

// getHistory asks router for key's history on device and decodes the answer,
// failing the test on anything but a 200 that decodes.
func getHistory(t *testing.T, router http.Handler, device, key string) (historyAnswer, string) {
	t.Helper()
	rr := factsGet(router, "/inventory/devices/"+device+"/facts/"+url.PathEscape(key))
	var body historyAnswer
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &body) != nil {
		t.Fatalf("history of %s on %s: %d %s", key, device, rr.Code, rr.Body.String())
	}
	return body, rr.Body.String()
}

// valuesOf is the values of entries, in their order, as text.
func valuesOf(entries []facts.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = fmt.Sprint(e.Value)
	}
	return out
}

// TestDeviceFactHistory_TokenizesTheKeyAndReadsTheSealedStore: through the
// real handler over a real SQLite entstore.Store holding sealed facts, a
// stored key's history comes back under its plaintext name with its values
// oldest first and no other key's; a key nothing stored answers the
// documented empty history and names no stored fact; the same key on a second
// device answers that device's value alone; and the facts table, read with
// plain SQL, holds neither the key's name nor any value.
func TestDeviceFactHistory_TokenizesTheKeyAndReadsTheSealedStore(t *testing.T) {
	ctx := context.Background()
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", filepath.Join(t.TempDir(), "facts.db"))
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	web1 := client.Device.Create().SetName("web1").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.1"}).SaveX(ctx)
	web2 := client.Device.Create().SetName("web2").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.2"}).SaveX(ctx)
	store := entstore.New(client, factstest.Keys(), factstest.Envelope())

	// web1's key changes once, and web1 holds a second key beside it; web2
	// holds the same key with a value of its own.
	at := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	recordSealedValue(t, store, web1.DeviceID, sealedHistoryKey, "Juniper6Cascade", at)
	recordSealedValue(t, store, web1.DeviceID, "os.family", "Hemlock4Prairie", at.Add(time.Minute))
	recordSealedValue(t, store, web1.DeviceID, sealedHistoryKey, "Saffron2Beacon", at.Add(2*time.Minute))
	recordSealedValue(t, store, web2.DeviceID, sealedHistoryKey, "Cobalt8Thistle", at.Add(3*time.Minute))
	allValues := []string{"Juniper6Cascade", "Hemlock4Prairie", "Saffron2Beacon", "Cobalt8Thistle"}

	// The facts table, read with plain SQL: every column of every row, as
	// text, before any read, so a store whose reads would then fail still
	// has its rows searched. The control is that the search reads the rows
	// the store wrote, four of them, each with a sealed payload, so a key or
	// value in any column would be in what is searched.
	table := rawFactRows(t, db)
	if len(table) != 4 {
		t.Fatalf("the facts table holds %d rows, want the 4 values recorded", len(table))
	}
	for i, row := range table {
		if !strings.Contains(row, crypto.EncryptedKeyMarker) {
			t.Errorf("row %d holds no sealed payload, so the search below is not of what the store wrote: %s", i, row)
		}
		for _, never := range append([]string{sealedHistoryKey, "zq7velvet", "os.family"}, allValues...) {
			if strings.Contains(row, never) {
				t.Errorf("row %d of the facts table holds %q in plaintext: %s", i, never, row)
			}
		}
	}

	router1 := factsRouter(t, &stubDeviceRepo{item: &inventorytest.Stub{StubID: pkginventory.DeviceID(web1.DeviceID), StubName: "web1"}}, store)
	router2 := factsRouter(t, &stubDeviceRepo{item: &inventorytest.Stub{StubID: pkginventory.DeviceID(web2.DeviceID), StubName: "web2"}}, store)

	// A stored key: its two values, oldest first, each under the key in
	// plaintext, and never the device's other key.
	got, raw := getHistory(t, router1, "web1", sealedHistoryKey)
	if got.Key != sealedHistoryKey || !slices.Equal(valuesOf(got.Facts), []string{"Juniper6Cascade", "Saffron2Beacon"}) {
		t.Errorf("history of %s on web1: key %q values %v, want both of web1's values oldest first", sealedHistoryKey, got.Key, valuesOf(got.Facts))
	}
	for _, e := range got.Facts {
		if e.Key != sealedHistoryKey {
			t.Errorf("history of %s on web1 holds an entry for %q", sealedHistoryKey, e.Key)
		}
	}
	for _, never := range []string{"Hemlock4Prairie", "os.family", "Cobalt8Thistle"} {
		if strings.Contains(raw, never) {
			t.Errorf("history of %s on web1 holds %q: %s", sealedHistoryKey, never, raw)
		}
	}

	// A key nothing stored: the empty history the route documents ("empty
	// when the device never had it"), naming no stored fact or value.
	const absent = "package.absentpkg"
	got, raw = getHistory(t, router1, "web1", absent)
	if len(got.Facts) != 0 || !strings.Contains(raw, `"facts":[]`) {
		t.Errorf("history of a key nothing stored: %s; want an empty history", raw)
	}
	for _, never := range append([]string{sealedHistoryKey, "os.family"}, allValues...) {
		if strings.Contains(raw, never) {
			t.Errorf("history of a key nothing stored holds %q: %s", never, raw)
		}
	}

	// The same key on the second device: that device's value alone.
	got, raw = getHistory(t, router2, "web2", sealedHistoryKey)
	if got.Key != sealedHistoryKey || !slices.Equal(valuesOf(got.Facts), []string{"Cobalt8Thistle"}) {
		t.Errorf("history of %s on web2: key %q values %v, want web2's value alone", sealedHistoryKey, got.Key, valuesOf(got.Facts))
	}
	for _, never := range []string{"Juniper6Cascade", "Saffron2Beacon", "Hemlock4Prairie"} {
		if strings.Contains(raw, never) {
			t.Errorf("history of %s on web2 holds web1's %q: %s", sealedHistoryKey, never, raw)
		}
	}
}

// rawFactRows is every row of the facts table, each one's columns rendered as
// text and joined, read through database/sql rather than the ent client.
func rawFactRows(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query("SELECT * FROM facts")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for i, v := range values {
			// A text or blob column scans as bytes; anything else prints.
			if raw, ok := v.([]byte); ok {
				v = string(raw)
			}
			fmt.Fprintf(&b, "%s=%v\x00", cols[i], v)
		}
		out = append(out, b.String())
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
