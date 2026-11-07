// A read of the Controller's store that a record commits in the middle of
// (Phase 41h).
package entstore

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	entfact "github.com/Subject-Void-LLC/the-pleiades/internal/ent/fact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// TestSighted_ARecordBetweenReadsCreditsNoReplacedValue: Current takes its
// bound, reads the rows, and a record that changes a value commits before it
// reads the observations. Folding only the observations up to the bound keeps
// the old value confirmed by the run that saw it; folding every observation,
// as the first version did, credits it to the run that replaced it.
func TestSighted_ARecordBetweenReadsCreditsNoReplacedValue(t *testing.T) {
	ctx := context.Background()
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", filepath.Join(t.TempDir(), "facts.db"))
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	device := client.Device.Create().SetName("web1").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.1"}).SaveX(ctx).DeviceID
	s := New(client)
	t0 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	gather := func(run string, token uint64, kernel string) {
		t.Helper()
		entries, _, err := facts.Prepare(map[string]any{"os.kernel": kernel}, facts.Guard{})
		if err != nil {
			t.Fatal(err)
		}
		obs := facts.Observation{Entries: entries, At: t0.Add(time.Duration(token) * time.Minute), By: facts.Provenance{Run: run, Order: facts.Order{Source: facts.ClockSource, Token: token}}}
		if err := s.Record(ctx, device, obs); err != nil {
			t.Fatal(err)
		}
	}
	gather("run1", 1, "6.1")
	dev, upTo, err := s.bound(ctx, device)
	if err != nil {
		t.Fatal(err)
	}
	rows := client.Fact.Query().Where(entfact.HasDeviceWith(entdevice.DeviceIDEQ(device))).Order(ent.Asc(entfact.FieldID)).AllX(ctx)
	gather("run2", 2, "6.2")
	credited := func(upTo int64) string {
		t.Helper()
		got, err := s.sighted(ctx, dev, upTo, rows)
		if err != nil || len(got) != 1 {
			t.Fatalf("sighted = %+v, %v", got, err)
		}
		return got[0].LastBy.Run
	}
	if run := credited(upTo); run != "run1" {
		t.Errorf("6.1 reads as last confirmed by %s, want run1: run2 replaced it", run)
	}
	// The read the first version made, to show the test tells them apart.
	if run := credited(math.MaxInt64); run != "run2" {
		t.Errorf("an unbounded fold credited %s; the test no longer reproduces the torn read", run)
	}
}
