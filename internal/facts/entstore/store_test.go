// Tests of the Controller's fact store and consumer against a real SQLite
// database on a real file.
package entstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
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
	entries, problems, err := facts.Prepare(observed, nil)
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
		if err := store.Record(ctx, device, prepared(t, observed), t0.Add(time.Duration(i)*time.Hour)); err != nil {
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
	if m := current[1]; !m.ObservedAt.Equal(t0) || !m.LastSeenAt.Equal(t0.Add(time.Hour)) || m.Value != json.Number("2048") && m.Value != float64(2048) {
		t.Errorf("mem = %+v, want observed at t0 and last seen an hour later", m)
	}
	history, err := store.History(ctx, device, "kernel")
	if err != nil || len(history) != 2 || history[0].Value != "6.1" || history[1].Value != "6.2" {
		t.Errorf("History(kernel) = %+v, %v", history, err)
	}
	if err := store.Record(ctx, "no-such-device", prepared(t, map[string]any{"k": "v"}), t0); !errors.Is(err, entstore.ErrUnknownDevice) {
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
