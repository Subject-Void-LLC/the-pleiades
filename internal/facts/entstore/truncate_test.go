// Truncate (truncate.go) on SQLite and on PostgreSQL: it keeps exactly the
// history before the gather that wrote the first failing value, re-keys it
// when it was under the previous key, removes a failing volatile row alone,
// and refuses every state it cannot make sound, changing nothing.
package entstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
)

// gatherRotationHistory's rows, by the gather that wrote them: stable rows 0
// and 1 by gather 1, 2 and 3 by gather 3, 4 and 5 by gather 4 (the late
// change and the value resumed after it), and the one volatile row left by
// gather 3. Its coverages are gather 1's (gather 2 covered the same), gather
// 3's and gather 4's.

// truncatesAndRekeys is the proof both dialects run: a history under the
// previous key whose gather 4 was edited keeps gathers 1 to 3, re-keyed
// under the current key, and starts again from there.
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
	if err != nil || !done.Committed || done.Facts != 2 || done.Observations != 1 || done.Coverages != 1 || done.Kept != 5 || done.Rekeyed != 5 ||
		done.Digest != shown.Digest || done.Before.TruncateFrom != 4 {
		t.Fatalf("the truncate = %+v, %v; want gather 4's two values, its observation and coverage removed, five kept and re-keyed, as shown", done, err)
	}
	if got := rawQuery(t, h.db, "SELECT DISTINCT keyed_by FROM facts"); got != newFactKeys(t, 7).ID()+"|\n" {
		t.Errorf("the kept rows name %q, want the current keys alone", got)
	}
	if got := danglingObservations(t, h.db); got != "0|\n" {
		t.Errorf("%s observation(s) name a coverage the device does not hold", got)
	}
	kept, err := store.InspectDevice(ctx, "web1", entstore.InspectOptions{})
	if err != nil || kept.State != "healthy" || kept.Rows.Stable != 4 || kept.Observations != 3 || kept.Coverages != 2 {
		t.Fatalf("the device after the truncate = %+v, %v; want healthy, four stable rows, three gathers", kept, err)
	}
	if len(h.activityOf()) != 1 {
		t.Errorf("activity %+v, want the truncate's one entry", h.activityOf())
	}
	// Gather 9 changes the package and the user: two values more.
	h.startsAgain(store, 6)
}

func TestTruncate_KeepsTheVerifiedPrefixAndRekeysIt(t *testing.T) {
	truncatesAndRekeys(t, onSQLite)
}

func TestTruncate_KeepsTheVerifiedPrefixAndRekeysItOnPostgres(t *testing.T) {
	truncatesAndRekeys(t, onPostgres)
}

// TestTruncate_UnderTheCurrentKey: a broken history under the current key,
// edited at gather 3, keeps gather 1 alone; the volatile row gather 3 left
// goes with it.
func TestTruncate_UnderTheCurrentKey(t *testing.T) {
	ctx := context.Background()
	h := newHistory(t, onSQLite)
	h.editTime(2)
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
		// The second stable row says gather 4 wrote it, so the cut at gather
		// 4 would take it and leave a gap.
		{"gather numbers that disagree with the storage order", "", func(h *history) {
			h.editTime(4)
			h.exec("UPDATE facts SET observation_seq = 4 WHERE id = ?", h.stable(1))
		}, entstore.ErrTruncateUnsound},
		// Under the current key nothing is re-keyed after the cut, so the
		// check of what is kept is all that refuses it.
		{"gather numbers that disagree, under the current key", "old", func(h *history) {
			h.editTime(4)
			h.exec("UPDATE facts SET observation_seq = 4 WHERE id = ?", h.stable(1))
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
