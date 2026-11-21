// Discard (repair.go) on SQLite and on PostgreSQL: it removes exactly the
// device's rows and no other device's, shows first exactly what it then
// does, records itself in the activity stream with a keyed digest and no
// value, and leaves the device to start a new history that verifies; and
// what it refuses.
package entstore_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	entactivity "github.com/Subject-Void-LLC/the-pleiades/internal/ent/activityentry"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
)

// repairActor is who the repair tests' records name.
const repairActor = "repair-test-actor"

// rowsOf is every row the three fact tables hold for the device whose
// database ID is dev, as text.
func (h *history) rowsOf(dev int) string {
	h.t.Helper()
	return rawQuery(h.t, h.db, "SELECT * FROM facts WHERE device_facts = "+h.d.ph(1)+" ORDER BY id", dev) +
		rawQuery(h.t, h.db, "SELECT * FROM fact_coverages WHERE device_fact_coverages = "+h.d.ph(1)+" ORDER BY id", dev) +
		rawQuery(h.t, h.db, "SELECT * FROM fact_observations WHERE device_id = "+h.d.ph(1)+" ORDER BY id", dev)
}

// editTime moves the first-observed time of web1's nth stable row back an
// hour: the row still opens, and the chain breaks at it.
func (h *history) editTime(n int) {
	h.t.Helper()
	h.exec("UPDATE facts SET created_at = "+h.d.ph(1)+" WHERE id = "+h.d.ph(2), rotationT0.Add(-time.Hour), h.stable(n))
}

// activityOf is every activity entry recorded about device facts.
func (h *history) activityOf() []activity.Entry {
	h.t.Helper()
	rows := h.client.ActivityEntry.Query().Where(entactivity.ObjectKindEQ(activity.KindDeviceFacts)).AllX(context.Background())
	out := make([]activity.Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, activity.Entry{Actor: r.Actor, Action: activity.Action(r.Action), ObjectKind: r.ObjectKind, ObjectID: r.ObjectID, ObjectName: r.ObjectName})
	}
	return out
}

// startsAgain records one gather of web1 through store after a repair, a
// package and a user each unlike any value gatherRotationHistory stored, so
// two values more, and checks it stored and the chain verifies over values
// values.
func (h *history) startsAgain(store *entstore.Store, values int) {
	h.t.Helper()
	ctx := context.Background()
	if err := recordGather(h.t, store, h.device, "run9", 90, rotationT0.Add(9*time.Hour),
		map[string]any{"package.openssh-server": "1:9.9p9-zq7build", "user.zq9operator": map[string]any{"shell": "/bin/zq9sh"}}, nil); err != nil {
		h.t.Fatalf("the gather after the repair: %v", err)
	}
	v, err := store.Verify(ctx, h.device)
	if err != nil || v.Break != nil || v.Values != values {
		h.t.Fatalf("Verify after the repair and a gather = %+v, %v; want %d values, whole", v, err, values)
	}
	if current, err := store.Current(ctx, h.device); err != nil || len(current) < 2 {
		h.t.Fatalf("Current after the repair and a gather = %+v, %v", current, err)
	}
}

// discardsExactlyThatDevice is the proof both dialects run.
func discardsExactlyThatDevice(t *testing.T, d database) {
	ctx := context.Background()
	h := newHistory(t, d)
	other := h.client.Device.Create().SetName("web2").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.2"}).SaveX(ctx)
	gatherRotationHistory(t, entstore.New(h.client, h.old, factstest.Envelope()), other.DeviceID)
	h.editTime(1)
	store := h.store("")
	untouched, raw := h.rowsOf(other.ID), rawFactTables(t, h.db)

	// Without the confirmation: what it would do, and nothing done.
	shown, err := store.Discard(ctx, "web1", entstore.RepairOptions{Actor: repairActor})
	if err != nil || shown.Committed || shown.Facts != 7 || shown.Coverages != 3 || shown.Observations != 4 || shown.Before.State != "refused" {
		t.Fatalf("the shown discard = %+v, %v; want 7 facts, 3 coverages and 4 observations, not committed", shown, err)
	}
	if rawFactTables(t, h.db) != raw || len(h.activityOf()) != 0 {
		t.Fatal("a discard without its confirmation changed the database or recorded itself")
	}
	// Keyed: the same rows digest otherwise under another key.
	elsewhere, err := entstore.New(h.client, newFactKeys(t, 8), factstest.Envelope(), entstore.WithPreviousKeys(h.old)).Discard(ctx, "web1", entstore.RepairOptions{Actor: repairActor})
	if err != nil || elsewhere.Digest == shown.Digest || elsewhere.DigestKey == shown.DigestKey {
		t.Errorf("the digest under another key = %+v, %v; want another digest", elsewhere, err)
	}

	done, err := store.Discard(ctx, "web1", entstore.RepairOptions{Confirm: true, Actor: repairActor})
	if err != nil || !done.Committed || done.Facts != shown.Facts || done.Coverages != shown.Coverages || done.Observations != shown.Observations || done.Digest != shown.Digest {
		t.Fatalf("the discard = %+v, %v; want what it showed, committed", done, err)
	}
	if got := h.rowsOf(h.dev); got != "" {
		t.Errorf("web1 still holds rows after the discard:\n%s", got)
	}
	if h.rowsOf(other.ID) != untouched {
		t.Error("the discard of web1 changed web2's rows")
	}
	records := h.activityOf()
	if len(records) != 1 {
		t.Fatalf("%d activity entries, want the discard's one: %+v", len(records), records)
	}
	r := records[0]
	if r.Actor != repairActor || r.Action != activity.ActionDeleted || r.ObjectID != h.dev ||
		!strings.Contains(r.ObjectName, "web1: discard removed 7 stored facts, 3 coverages and 4 observations") || !strings.Contains(r.ObjectName, done.Digest) {
		t.Errorf("the activity entry %+v does not record the discard", r)
	}
	for _, plain := range nonHexPlaintexts() {
		if strings.Contains(r.ObjectName, plain) {
			t.Errorf("the activity entry names %q: %s", plain, r.ObjectName)
		}
	}
	// A new history, under the current key, that verifies.
	h.startsAgain(store, 2)
}

func TestDiscard_RemovesExactlyThatDevicesHistory(t *testing.T) {
	discardsExactlyThatDevice(t, onSQLite)
}

func TestDiscard_RemovesExactlyThatDevicesHistoryOnPostgres(t *testing.T) {
	discardsExactlyThatDevice(t, onPostgres)
}

// TestDiscard_RefusesWhatNeedsNoRepair: a history that verifies, one a
// re-key is pending for, a device with nothing stored, a device the
// database does not hold, and a repair with nobody to record it as are each
// refused and change nothing; a verified history goes only with the second
// flag that names it.
func TestDiscard_RefusesWhatNeedsNoRepair(t *testing.T) {
	ctx := context.Background()
	h := newHistory(t, onSQLite)
	h.client.Device.Create().SetName("web3").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.3"}).SaveX(ctx)
	raw := rawFactTables(t, h.db)
	confirm := entstore.RepairOptions{Confirm: true, Actor: repairActor}
	for _, c := range []struct {
		name, store, device string
		opts                entstore.RepairOptions
		want                error
	}{
		{"a history that verifies", "old", "web1", confirm, entstore.ErrNothingToRepair},
		{"a history pending a re-key", "", "web1", confirm, entstore.ErrNothingToRepair},
		{"nothing stored", "", "web3", entstore.RepairOptions{Confirm: true, Actor: repairActor, DiscardVerified: true}, entstore.ErrNothingToRepair},
		{"an unknown device", "", "no-such-device", confirm, entstore.ErrUnknownDevice},
		{"nobody to record it as", "", "web1", entstore.RepairOptions{Confirm: true, DiscardVerified: true}, nil},
		{"a history under a facts key not given", "third", "web1", confirm, entstore.ErrUnverifiable},
		{"a history no row of which opens", "other master key", "web1", confirm, entstore.ErrUnverifiable},
		{"the flag for a verified history is not the flag for an unverifiable one", "third", "web1",
			entstore.RepairOptions{Confirm: true, Actor: repairActor, DiscardVerified: true}, entstore.ErrUnverifiable},
	} {
		_, err := h.store(c.store).Discard(ctx, c.device, c.opts)
		if err == nil || (c.want != nil && !errors.Is(err, c.want)) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	if rawFactTables(t, h.db) != raw || len(h.activityOf()) != 0 {
		t.Fatal("a refused discard changed the database or recorded itself")
	}
	done, err := h.store("old").Discard(ctx, "web1", entstore.RepairOptions{Confirm: true, Actor: repairActor, DiscardVerified: true})
	if err != nil || done.Facts != 7 || done.Before.State != "healthy" {
		t.Fatalf("a verified history with the flag that names it = %+v, %v", done, err)
	}
	h.startsAgain(h.store("old"), 2)
}

// TestDiscard_AnUnverifiableHistoryTakesItsFlag: a history under both keys,
// under a facts key not given, with no row that opens, or with one row that
// does not open is discarded only with the flag that names it, and then
// removed whole.
func TestDiscard_AnUnverifiableHistoryTakesItsFlag(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, store string
		edit        func(h *history)
	}{
		{"both keys", "", func(h *history) {
			if _, err := h.store("").Current(ctx, h.device); err != nil {
				h.t.Fatal(err)
			}
			h.exec("UPDATE facts SET keyed_by = ? WHERE id = ?", h.old.ID(), h.ids("")[0])
		}},
		{"a facts key not given", "third", func(*history) {}},
		{"no row that opens", "other master key", func(*history) {}},
		// One row that does not open, the rest verifying: a history partly
		// sealed under a key the command was not given looks just so.
		{"a row that does not open", "", func(h *history) { h.copyPayload(h.stable(0), h.stable(1)) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHistory(t, onSQLite)
			c.edit(h)
			if _, err := h.store(c.store).Discard(ctx, "web1", entstore.RepairOptions{Confirm: true, Actor: repairActor}); !errors.Is(err, entstore.ErrUnverifiable) {
				t.Fatalf("without the flag: %v, want ErrUnverifiable", err)
			}
			done, err := h.store(c.store).Discard(ctx, "web1", entstore.RepairOptions{Confirm: true, Actor: repairActor, DiscardUnverifiable: true})
			if err != nil || done.Facts != 7 || h.rowsOf(h.dev) != "" {
				t.Fatalf("with the flag: %+v, %v; want the whole history removed", done, err)
			}
		})
	}
}
