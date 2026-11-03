// Tests of the Controller's fact store and consumer against a real SQLite
// database on a real file.
package entstore_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// open returns a client over a real file and one device in it.
func open(t *testing.T) (*ent.Client, string) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", filepath.Join(t.TempDir(), "facts.db"))
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	dev := client.Device.Create().SetName("web1").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.1"}).SaveX(context.Background())
	return client, dev.DeviceID
}

func prepared(t *testing.T, observed map[string]any) []facts.Entry {
	t.Helper()
	entries, problems, err := facts.Prepare(observed, facts.Guard{})
	if err != nil || len(problems) != 0 {
		t.Fatalf("Prepare: %v, %v", problems, err)
	}
	return entries
}

func TestStore_WritesOnChangeAndKeepsHistory(t *testing.T) {
	client, device := open(t)
	store := entstore.New(client)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	for i, observed := range []map[string]any{
		{"kernel": "6.1", "mem": 2048},
		{"kernel": "6.1", "mem": 2048},
		{"kernel": "6.2"},
	} {
		if err := store.Record(ctx, device, facts.Observation{Entries: prepared(t, observed), At: t0.Add(time.Duration(i) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if n := client.Fact.Query().CountX(ctx); n != 3 {
		t.Errorf("%d rows, want 3 (kernel twice, mem once)", n)
	}
	current, err := store.Current(ctx, device)
	if err != nil || len(current) != 2 || current[0].Key != "kernel" || current[0].Value != "6.2" || current[1].Key != "mem" {
		t.Fatalf("Current = %+v, %v", current, err)
	}
	if m := current[1]; !m.ObservedAt.Equal(t0) || !m.LastSeenAt.Equal(t0.Add(time.Hour)) || m.Value != json.Number("2048") {
		t.Errorf("mem = %+v, want observed at t0 and last seen an hour later", m)
	}
	history, err := store.History(ctx, device, "kernel")
	if err != nil || len(history) != 2 || history[0].Value != "6.1" || history[1].Value != "6.2" {
		t.Errorf("History(kernel) = %+v, %v", history, err)
	}
	if err := store.Record(ctx, "no-such-device", facts.Observation{Entries: prepared(t, map[string]any{"k": "v"}), At: t0}); !errors.Is(err, entstore.ErrUnknownDevice) {
		t.Errorf("an unknown device: %v, want ErrUnknownDevice", err)
	}
}

// TestFact_TheSchemaRefusesEditingAValue: a stored value is immutable in the
// schema itself: the generated update has no setter for the key, the value
// or the hash, so no caller can build an in-place edit, and only last_seen_at
// moves.
func TestFact_TheSchemaRefusesEditingAValue(t *testing.T) {
	update := reflect.TypeOf(&ent.FactUpdateOne{})
	for _, setter := range []string{"SetKey", "SetPayload", "SetHash"} {
		if _, ok := update.MethodByName(setter); ok {
			t.Errorf("ent.FactUpdateOne has %s: a stored fact could be edited in place", setter)
		}
	}
	if _, ok := update.MethodByName("SetLastSeenAt"); !ok {
		t.Error("ent.FactUpdateOne cannot move last_seen_at")
	}
}

// bus is an event.Bus that keeps nothing: the consumer's Handle is called
// directly.
func handle(t *testing.T, sub *entstore.Subscriber, batch any) error {
	t.Helper()
	data, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	return sub.Handle(event.Event{ID: "e1", Type: facts.EventType, Data: data})
}

// TestSubscriber_StoresOnlyWhatPassesAndWasDispatched: what a Runner publishes
// is re-checked: an entry whose hash does not describe its value is dropped,
// a batch for a device this Controller never dispatched is dropped, one not
// recorded yet is asked for again, and a malformed one is acknowledged.
func TestSubscriber_StoresOnlyWhatPassesAndWasDispatched(t *testing.T) {
	client, device := open(t)
	store := entstore.New(client)
	ctx := context.Background()
	entries := prepared(t, map[string]any{"kernel": "6.1", "forged": "x"})
	for i := range entries {
		if entries[i].Key == "forged" {
			entries[i].Hash = "0000000000000000000000000000000000000000000000000000000000000000"
		}
	}
	batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(), Entries: entries}

	admission := journal.AdmitNotDispatched
	sub := entstore.NewSubscriber(store, nil, func(context.Context, string, string) (journal.Admission, error) { return admission, nil })
	if err := handle(t, sub, batch); err != nil || client.Fact.Query().CountX(ctx) != 0 {
		t.Fatalf("a batch for a device never dispatched: %v, stored %d", err, client.Fact.Query().CountX(ctx))
	}
	admission = journal.AdmitUnrecorded
	if err := handle(t, sub, batch); err == nil {
		t.Error("a batch for a dispatch not recorded yet was acknowledged")
	}
	admission = journal.AdmitDispatched
	if err := handle(t, sub, batch); err != nil {
		t.Fatal(err)
	}
	current, _ := store.Current(ctx, device)
	if len(current) != 1 || current[0].Key != "kernel" {
		t.Errorf("stored %+v, want only kernel, the forged hash dropped", current)
	}
	if err := sub.Handle(event.Event{ID: "e2", Data: []byte("not json")}); err != nil {
		t.Errorf("a malformed batch was not acknowledged: %v", err)
	}
	unknown := batch
	unknown.DeviceID = "no-such-device"
	if err := handle(t, sub, unknown); err != nil {
		t.Errorf("a batch for a device the database lacks was not acknowledged: %v", err)
	}
}

// TestStore_ALateBatchDoesNotReplaceANewerValue: a value observed before the
// one confirmed after it, stored late (a redelivery, a Runner whose clock is
// behind), joins the history without becoming current, and the same batch
// stored twice writes one row.
func TestStore_ALateBatchDoesNotReplaceANewerValue(t *testing.T) {
	client, device := open(t)
	store := entstore.New(client)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	_ = store.Record(ctx, device, facts.Observation{Entries: prepared(t, map[string]any{"k": "A"}), At: t0})
	_ = store.Record(ctx, device, facts.Observation{Entries: prepared(t, map[string]any{"k": "A"}), At: t0.Add(5 * time.Hour)})
	for range 2 {
		if err := store.Record(ctx, device, facts.Observation{Entries: prepared(t, map[string]any{"k": "B"}), At: t0.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	current, _ := store.Current(ctx, device)
	if len(current) != 1 || current[0].Value != "A" {
		t.Errorf("current = %+v, want A, confirmed after B was observed", current)
	}
	if n := client.Fact.Query().CountX(ctx); n != 2 {
		t.Errorf("%d rows, want A and B once each", n)
	}
}

// TestSubscriber_ABigIntegerCrossesTheWireWhole: an integer past 2^53, which
// float64 would round, hashes on the Controller as it did on the Runner, so
// the fact is stored rather than refused as forged.
func TestSubscriber_ABigIntegerCrossesTheWireWhole(t *testing.T) {
	client, device := open(t)
	store := entstore.New(client)
	var big any
	_ = json.Unmarshal([]byte("9007199254740993"), &big) // a float64, as a careless decode would make it
	dec := json.NewDecoder(strings.NewReader("9007199254740993"))
	dec.UseNumber()
	_ = dec.Decode(&big)
	entries := prepared(t, map[string]any{"serial_counter": big})
	sub := entstore.NewSubscriber(store, nil, nil)
	if err := handle(t, sub, facts.Batch{JobID: "job-1", DeviceID: device, ObservedAt: time.Now(), Entries: entries}); err != nil {
		t.Fatal(err)
	}
	current, _ := store.Current(context.Background(), device)
	if len(current) != 1 || fmt.Sprint(current[0].Value) != "9007199254740993" {
		t.Errorf("stored %+v, want the big integer", current)
	}
}

// TestStore_Conformance holds the Controller's store to the rules both
// stores keep (factstest), over a real SQLite file.
func TestStore_Conformance(t *testing.T) {
	factstest.Run(t, func(t *testing.T) (facts.Store, string) {
		client, device := open(t)
		return entstore.New(client), device
	})
}

// TestStore_VerifyFindsADeletedOrRewrittenRow tampers with the table the way
// someone with the database would, past the generated client: a row deleted,
// and a payload rewritten in SQL.
func TestStore_VerifyFindsADeletedOrRewrittenRow(t *testing.T) {
	for name, tamper := range map[string]func(t *testing.T, client *ent.Client, db *sql.DB, ids []int){
		"a deleted row": func(t *testing.T, client *ent.Client, _ *sql.DB, ids []int) {
			client.Fact.DeleteOneID(ids[1]).ExecX(context.Background())
		},
		"a rewritten payload": func(t *testing.T, _ *ent.Client, db *sql.DB, ids []int) {
			if _, err := db.Exec(`UPDATE facts SET payload = ? WHERE id = ?`, `{"json":"\"9.9\""}`, ids[1]); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "facts.db")
			dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", path)
			client := enttest.Open(t, "sqlite3", dsn)
			t.Cleanup(func() { _ = client.Close() })
			dev := client.Device.Create().SetName("web1").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.1"}).SaveX(context.Background())
			store := entstore.New(client)
			at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
			for i, kernel := range []string{"6.1", "6.2", "6.3"} {
				obs := facts.Observation{Entries: prepared(t, map[string]any{"ansible_kernel": kernel}), At: at.Add(time.Duration(i) * time.Minute),
					By: facts.Provenance{Run: "r", Order: facts.Order{Source: facts.ClockSource, Token: uint64(i + 1)}}}
				if err := store.Record(context.Background(), dev.DeviceID, obs); err != nil {
					t.Fatal(err)
				}
			}
			if v, err := store.Verify(context.Background(), dev.DeviceID); err != nil || v.Break != nil || v.Values != 3 {
				t.Fatalf("before tampering: %+v, %v", v, err)
			}
			ids := client.Fact.Query().Order(ent.Asc("id")).IDsX(context.Background())
			db, err := sql.Open("sqlite3", dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			tamper(t, client, db, ids)
			v, err := store.Verify(context.Background(), dev.DeviceID)
			if err != nil || v.Break == nil {
				t.Fatalf("after tampering: %+v, %v; want a break", v, err)
			}
		})
	}
}

// TestStore_TheChainIndexRefusesASecondSuccessor: two writers that both read
// the same head cannot both extend it; the unique index refuses the second,
// whose batch the bus then redelivers against the new head.
func TestStore_TheChainIndexRefusesASecondSuccessor(t *testing.T) {
	client, device := open(t)
	store := entstore.New(client)
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	if err := store.Record(context.Background(), device, facts.Observation{Entries: prepared(t, map[string]any{"a": "1"}), At: at}); err != nil {
		t.Fatal(err)
	}
	head := client.Fact.Query().OnlyX(context.Background())
	dev := client.Device.Query().OnlyX(context.Background())
	// A second row naming the same predecessor as the first (the genesis "").
	err := client.Fact.Create().SetKey("b").SetPayload(map[string]any{"json": `"1"`}).SetHash("h").SetChainPrev("").SetChain("x").SetDevice(dev).Exec(context.Background())
	if err == nil || !ent.IsConstraintError(err) {
		t.Fatalf("a second genesis row: %v, want a constraint error", err)
	}
	err = client.Fact.Create().SetKey("c").SetPayload(map[string]any{"json": `"1"`}).SetHash("h").SetChainPrev(head.Chain).SetChain("y").SetDevice(dev).Exec(context.Background())
	if err != nil {
		t.Fatalf("the successor: %v", err)
	}
	err = client.Fact.Create().SetKey("d").SetPayload(map[string]any{"json": `"1"`}).SetHash("h").SetChainPrev(head.Chain).SetChain("z").SetDevice(dev).Exec(context.Background())
	if err == nil || !ent.IsConstraintError(err) {
		t.Fatalf("a second successor: %v, want a constraint error", err)
	}
}

// TestSubscriber_HoldsABuiltInMethodsFactsToItsDeclaration: for a method this
// Controller knows, a batch cannot store a fact under another kind or type
// than the method declares, and a key it does not declare at all; the job
// and attempt stored are the batch's own, whatever its provenance says.
func TestSubscriber_HoldsABuiltInMethodsFactsToItsDeclaration(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	invoke := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{Name: "entstoretest.gather", Invoke: invoke, Manifest: collection.Manifest{
		Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture"},
		Facts: []collection.FactSpec{
			{Key: "kernel", Kind: collection.FactStable, Type: collection.FactString, Description: "the kernel"},
			{Key: "uptime", Kind: collection.FactVolatile, Type: collection.FactInteger, Description: "the uptime"},
		}}}); err != nil {
		t.Fatal(err)
	}
	client, device := open(t)
	store := entstore.New(client)
	ctx := context.Background()
	kinds := func(string) (facts.Spec, bool) { return facts.Spec{Kind: facts.KindVolatile}, true }
	entries, _, _ := facts.Prepare(map[string]any{"kernel": "6.1", "uptime": 5, "extra": "x"}, facts.Guard{Declared: kinds})
	batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 2, ObservedAt: time.Now(), Entries: entries,
		By: facts.Provenance{Job: "forged", Attempt: 9, Method: "entstoretest.gather"}}
	sub := entstore.NewSubscriber(store, nil, nil)
	if err := handle(t, sub, batch); err != nil {
		t.Fatal(err)
	}
	current, _ := store.Current(ctx, device)
	if len(current) != 1 || current[0].Key != "uptime" || current[0].Kind != facts.KindVolatile {
		t.Fatalf("stored %+v, want only uptime: kernel claimed volatile and extra undeclared are refused", current)
	}
	if by := current[0].By; by.Job != "job-1" || by.Attempt != 2 || by.Method != "entstoretest.gather" {
		t.Errorf("provenance = %+v, want the batch's own job and attempt", by)
	}
}

// TestStore_AClosedDatabaseIsReported: a store whose database is gone says
// so on every call rather than storing or reading nothing quietly.
func TestStore_AClosedDatabaseIsReported(t *testing.T) {
	client, device := open(t)
	store := entstore.New(client)
	_ = client.Close()
	ctx := context.Background()
	if err := store.Record(ctx, device, facts.Observation{Entries: prepared(t, map[string]any{"k": "v"}), At: time.Now()}); err == nil {
		t.Error("Record on a closed database")
	}
	if _, err := store.Current(ctx, device); err == nil {
		t.Error("Current on a closed database")
	}
	if _, err := store.History(ctx, device, "k"); err == nil {
		t.Error("History on a closed database")
	}
	if _, err := store.Verify(ctx, device); err == nil {
		t.Error("Verify on a closed database")
	}
}
