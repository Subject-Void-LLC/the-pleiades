// Truncate (truncate.go) on SQLite and on PostgreSQL: it keeps exactly the
// history before the gather that wrote the first failing value, re-keys it
// when it was under the previous key, removes a failing volatile row alone,
// and refuses every state it cannot make sound, changing nothing.
package entstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
)

// gatherRotationHistory's rows, by the gather that wrote them: stable rows 0
// and 1 by gather 1, 2 and 3 by gather 3, 4 and 5 by gather 4 (the late
// change and the value resumed after it), and the one volatile row left by
// gather 3. Its coverages are gather 1's (gather 2 covered the same), gather
// 3's and gather 4's.

// truncatesAndRekeys is the proof both dialects run: a history under the
// previous key whose gather 4 was edited, so that the last value verifying
// before it is gather 3's, keeps gathers 1 and 2, re-keyed under the current
// key, and starts again from there.
func truncatesAndRekeys(t *testing.T, d database) {
	ctx := context.Background()
	h := newHistory(t, d)
	h.editTime(4)
	store := h.store("")
	raw := rawFactTables(t, h.db)
	shown, err := store.Truncate(ctx, "web1", entstore.RepairOptions{Actor: repairActor})
	if err != nil || shown.Committed || rawFactTables(t, h.db) != raw || len(h.activityOf()) != 0 {
		t.Fatalf("the shown truncate = %+v, %v; want nothing changed or recorded", shown, err)
	}
	done, err := store.Truncate(ctx, "web1", entstore.RepairOptions{Confirm: true, Actor: repairActor})
	if err != nil || !done.Committed || done.Facts != 5 || done.Observations != 2 || done.Coverages != 2 || done.Kept != 2 || done.Rekeyed != 2 ||
		done.Digest != shown.Digest || done.Before.TruncateFrom != 3 {
		t.Fatalf("the truncate = %+v, %v; want gathers 3 and 4 removed whole (five values, two observations, two coverages), two kept and re-keyed, as shown", done, err)
	}
	if got := rawQuery(t, h.db, "SELECT DISTINCT keyed_by FROM facts"); got != newFactKeys(t, 7).ID()+"|\n" {
		t.Errorf("the kept rows name %q, want the current keys alone", got)
	}
	if got := danglingObservations(t, h.db); got != "0|\n" {
		t.Errorf("%s observation(s) name a coverage the device does not hold", got)
	}
	kept, err := store.InspectDevice(ctx, "web1", entstore.InspectOptions{})
	if err != nil || kept.State != "healthy" || kept.Rows.Stable != 2 || kept.Observations != 2 || kept.Coverages != 1 {
		t.Fatalf("the device after the truncate = %+v, %v; want healthy, two stable rows, two gathers", kept, err)
	}
	if len(h.activityOf()) != 1 {
		t.Errorf("activity %+v, want the truncate's one entry", h.activityOf())
	}
	// Gather 9 changes the package and the user: two values more.
	h.startsAgain(store, 4)
}

func TestTruncate_KeepsTheVerifiedPrefixAndRekeysIt(t *testing.T) {
	truncatesAndRekeys(t, onSQLite)
}

func TestTruncate_KeepsTheVerifiedPrefixAndRekeysItOnPostgres(t *testing.T) {
	truncatesAndRekeys(t, onPostgres)
}

// gatherInOrder records one gather of device through store, its stable
// values written in the order pairs (key, value, key, value...) gives them:
// facts.Prepare's order is a map's, and this test needs a known last row.
func gatherInOrder(t *testing.T, store *entstore.Store, device, run string, token uint64, pairs ...any) {
	t.Helper()
	var entries []facts.Entry
	for i := 0; i < len(pairs); i += 2 {
		entries = append(entries, prepared(t, map[string]any{pairs[i].(string): pairs[i+1]})...)
	}
	obs := facts.Observation{Entries: entries, At: rotationT0.Add(time.Duration(token) * time.Minute), By: facts.Provenance{Run: run, Order: facts.Order{Source: facts.ClockSource, Token: token}}}
	if err := store.Record(context.Background(), device, obs); err != nil {
		t.Fatalf("%s: %v", run, err)
	}
}

// keepsNoGatherWhoseWritesAreMissing is the proof both dialects run: last
// held A from gather 1, and gather 3 changed it to B as the last stable value
// gather 3 wrote. That row is deleted, so the next stable row, gather 4's,
// names a predecessor that is not there. Nothing before the gap fails, so a
// cut at gather 4 would keep gather 3's observation, which covers last and
// would then confirm A at gather 3, though gather 3 saw B. The cut is at the
// gather of the last value that verified (gather 3's other row), so gather
// 3 goes whole and both values read as last confirmed by gather 2.
func keepsNoGatherWhoseWritesAreMissing(t *testing.T, d database) {
	ctx := context.Background()
	h := newHistory(t, d)
	web9 := h.client.Device.Create().SetName("web9").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.9"}).SaveX(ctx)
	old := entstore.New(h.client, h.old, factstest.Envelope())
	gatherInOrder(t, old, web9.DeviceID, "run1", 10, "aaa.first", "1", "zzz.last", "A")
	gatherInOrder(t, old, web9.DeviceID, "run2", 20, "aaa.first", "1", "zzz.last", "A")
	gatherInOrder(t, old, web9.DeviceID, "run3", 30, "aaa.first", "2", "zzz.last", "B")
	gatherInOrder(t, old, web9.DeviceID, "run4", 40, "aaa.first", "3")
	var lastOfGather3 int
	q := "SELECT max(id) FROM facts WHERE observation_seq = 3 AND device_facts = " + h.d.ph(1)
	if err := h.db.QueryRowContext(ctx, q, web9.ID).Scan(&lastOfGather3); err != nil {
		t.Fatal(err)
	}
	h.exec("DELETE FROM facts WHERE id = "+h.d.ph(1), lastOfGather3)
	store := h.store("")
	if _, err := store.Truncate(ctx, "web9", entstore.RepairOptions{Confirm: true, Actor: repairActor}); err != nil {
		t.Fatal(err)
	}
	current, err := store.Current(ctx, web9.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"aaa.first": "1", "zzz.last": "A"}
	for _, e := range current {
		if e.Value != want[e.Key] || e.LastBy.Run != "run2" {
			t.Errorf("after the truncate %s reads %v, last confirmed by %s; want %v, last confirmed by run2: run3 saw another value", e.Key, e.Value, e.LastBy.Run, want[e.Key])
		}
	}
	if len(current) != 2 {
		t.Errorf("after the truncate %d values, want both", len(current))
	}
	q = "SELECT max(seq) FROM fact_observations WHERE device_id = " + h.d.ph(1)
	if got := rawQuery(t, h.db, q, web9.ID); got != "2|\n" {
		t.Errorf("the newest gather kept is %s, want gather 2", got)
	}
}

func TestTruncate_KeepsNoGatherWhoseWritesAreMissing(t *testing.T) {
	keepsNoGatherWhoseWritesAreMissing(t, onSQLite)
}

func TestTruncate_KeepsNoGatherWhoseWritesAreMissingOnPostgres(t *testing.T) {
	keepsNoGatherWhoseWritesAreMissing(t, onPostgres)
}

// TestTruncate_UnderTheCurrentKey: a broken history under the current key,
// edited at gather 4, keeps gathers 1 and 2 and re-keys nothing; the
// volatile row gather 3 left goes with gather 3.
func TestTruncate_UnderTheCurrentKey(t *testing.T) {
	ctx := context.Background()
	h := newHistory(t, onSQLite)
	h.editTime(4)
	done, err := h.store("old").Truncate(ctx, "web1", entstore.RepairOptions{Confirm: true, Actor: repairActor})
	if err != nil || done.Facts != 5 || done.Observations != 2 || done.Coverages != 2 || done.Kept != 2 || done.Rekeyed != 0 {
		t.Fatalf("the truncate = %+v, %v; want four values and a volatile row removed, two kept", done, err)
	}
	h.startsAgain(h.store("old"), 4)
}

// TestTruncate_RemovesAFailingVolatileRowAlone: a volatile row is not
// chained, so one that fails is removed by itself and the whole stable
// history is kept and re-keyed.
func TestTruncate_RemovesAFailingVolatileRowAlone(t *testing.T) {
	ctx := context.Background()
	h := newHistory(t, onSQLite)
	h.copyPayload(h.stable(0), h.ids("volatile")[0])
	done, err := h.store("").Truncate(ctx, "web1", entstore.RepairOptions{Confirm: true, Actor: repairActor})
	if err != nil || done.Facts != 1 || done.Observations != 0 || done.Coverages != 0 || done.Kept != 6 || done.Rekeyed != 6 {
		t.Fatalf("the truncate = %+v, %v; want the volatile row alone removed", done, err)
	}
	h.startsAgain(h.store(""), 8)
}

// TestTruncate_RefusesWhatItCannotMakeSound: a history that verifies, one
// under both keys or a key it was not given, one under the previous key
// with no current key to re-key into, and one whose gather numbers and
// storage order disagree are each refused and change nothing.
func TestTruncate_RefusesWhatItCannotMakeSound(t *testing.T) {
	ctx := context.Background()
	confirm := entstore.RepairOptions{Confirm: true, Actor: repairActor}
	for _, c := range []struct {
		name, store string
		edit        func(h *history)
		want        error
	}{
		{"a history that verifies", "old", func(*history) {}, entstore.ErrNothingToRepair},
		{"a key it was not given", "third", func(*history) {}, entstore.ErrRepairDoesNotApply},
		{"both keys", "", func(h *history) {
			if _, err := h.store("").Current(ctx, h.device); err != nil {
				h.t.Fatal(err)
			}
			h.exec("UPDATE facts SET keyed_by = ? WHERE id = ?", h.old.ID(), h.ids("")[0])
		}, entstore.ErrRepairDoesNotApply},
		{"no current key to re-key into", "previous only", func(h *history) { h.editTime(4) }, entstore.ErrRepairDoesNotApply},
		{"nothing before the first failure verifies", "", func(h *history) { h.editTime(0) }, entstore.ErrRepairDoesNotApply},
		{"no row that opens: a wrong master key", "other master key", func(*history) {}, entstore.ErrRepairDoesNotApply},
		// The last value verifying before the edit says gather 5 wrote it,
		// so the cut at gather 5 would keep the edited value of gather 4.
		{"gather numbers that disagree with the storage order", "", func(h *history) {
			h.editTime(4)
			h.exec("UPDATE facts SET observation_seq = 5 WHERE id = ?", h.stable(3))
		}, entstore.ErrTruncateUnsound},
		// Under the current key nothing is re-keyed after the cut, so the
		// check of what is kept is all that refuses it.
		{"gather numbers that disagree, under the current key", "old", func(h *history) {
			h.editTime(4)
			h.exec("UPDATE facts SET observation_seq = 5 WHERE id = ?", h.stable(3))
		}, entstore.ErrTruncateUnsound},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHistory(t, onSQLite)
			c.edit(h)
			raw := rawFactTables(t, h.db)
			if _, err := h.store(c.store).Truncate(ctx, "web1", confirm); !errors.Is(err, c.want) {
				t.Errorf("%v, want %v", err, c.want)
			}
			if rawFactTables(t, h.db) != raw || len(h.activityOf()) != 0 {
				t.Error("a refused truncate changed the database or recorded itself")
			}
		})
	}
}
