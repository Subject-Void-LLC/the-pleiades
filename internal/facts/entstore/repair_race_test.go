// A gather of a device while a repair of it runs (repair.go): the repair
// holds the device's facts exclusively from before it reads them, so the
// gather lands wholly after it, never beside it.
package entstore_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
)

// aGatherDuringARepair is the proof both dialects run: a discard is held
// after it has read and inspected the device and before it removes
// anything, for a second, long enough for a gather nothing holds back to
// commit, while a gather of the device runs. The discard then removes
// exactly what was stored before the gather began, and the gather stores
// after it. refused says whether a gather error is one its dialect may give
// for a gather refused without writing, which a later delivery retries.
func aGatherDuringARepair(t *testing.T, d database, refused func(error) bool) {
	t.Helper()
	ctx := context.Background()
	h := newHistory(t, d)
	h.editTime(1)
	store := h.store("")
	paused, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	entstore.SetBeforeRemove(store, func() {
		once.Do(func() {
			close(paused)
			<-release
		})
	})
	removed := make(chan entstore.Removal, 1)
	repairErr := make(chan error, 1)
	go func() {
		done, err := store.Discard(ctx, "web1", entstore.RepairOptions{Confirm: true, Actor: repairActor})
		removed <- done
		repairErr <- err
	}()
	<-paused
	obs := facts.Observation{Entries: prepared(t, map[string]any{"package.openssh-server": "1:9.9p3-zq7build", "user.zq9operator": "zq9other"}),
		At: rotationT0.Add(5 * time.Hour), By: facts.Provenance{Run: "run5", Order: facts.Order{Source: facts.ClockSource, Token: 50}}}
	gathered := make(chan error, 1)
	go func() { gathered <- store.Record(ctx, h.device, obs) }()
	select {
	case err := <-gathered:
		t.Fatalf("the gather finished while the discard held the device: %v", err)
	case <-time.After(time.Second):
	}
	close(release)
	if err := <-repairErr; err != nil {
		t.Fatalf("the discard: %v", err)
	}
	if done := <-removed; done.Facts != 7 || done.Observations != 4 {
		t.Errorf("the discard removed %+v; want the seven rows and four gathers stored before the gather began", done)
	}
	gatherErr := <-gathered
	switch {
	case gatherErr == nil:
		// It waited, and stored on the device the discard left: a history of
		// its own two values, which verifies.
		v, err := store.Verify(ctx, h.device)
		if err != nil || v.Break != nil || v.Values != 2 {
			t.Errorf("after the gather landed: %+v, %v; want its two values, whole", v, err)
		}
		if got := rawQuery(t, h.db, "SELECT count(*) FROM fact_observations"); got != "1|\n" {
			t.Errorf("%s observations, want the gather's one", got)
		}
	case refused(gatherErr):
		if got := h.rowsOf(h.dev); got != "" {
			t.Errorf("a refused gather left rows:\n%s", got)
		}
	default:
		t.Errorf("the gather: %v; want it stored after the discard, or refused", gatherErr)
	}
}

// TestRepair_AGatherLandsWhollyAfterOnPostgres: on PostgreSQL the gather
// waits on the device's hold, and stores after the discard commits.
func TestRepair_AGatherLandsWhollyAfterOnPostgres(t *testing.T) {
	aGatherDuringARepair(t, onPostgres, func(error) bool { return false })
}

// TestRepair_AGatherIsRefusedOrLandsAfterOnSQLite: on SQLite the gather
// finds the device under the previous key and re-keys it first, in a
// transaction of its own whose first statement waits for the discard's
// write lock; it then finds nothing left to re-key, and the gather stores a
// new history after the discard, which is what a run here shows. A gather
// whose wait outlasted the busy timeout would fail with "database is locked"
// having written nothing, and its batch would come again; that is accepted
// too.
func TestRepair_AGatherIsRefusedOrLandsAfterOnSQLite(t *testing.T) {
	aGatherDuringARepair(t, onSQLite, func(err error) bool { return strings.Contains(err.Error(), "database is locked") })
}
