package ent_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

// deviceIDLookupBenchN is how many devices are pre-loaded before either
// benchmark below measures a single-row lookup by device_id, matching
// BenchmarkDeviceQuery's own 10,000-row scale (client_bench_test.go) so
// the two are comparable.
const deviceIDLookupBenchN = 10000

// BenchmarkDeviceIDLookup_Ent and BenchmarkDeviceIDLookup_RawSQL measure
// the same operation, a single indexed lookup by device_id, through ent's
// generated query builder versus a hand-written database/sql query
// against the identical schema and data. This is the honest "industry
// alternative" baseline for a data-layer phase specifically (BenchmarkDeviceQuery's
// own AWX/Django comparison is for a different operation, a full-table
// fetch): raw database/sql is the floor ent's own generated code is
// layered on top of, so this measures ent's real marshaling/query-builder
// overhead rather than a different system's overhead entirely.
func BenchmarkDeviceIDLookup_Ent(b *testing.B) {
	client := enttest.Open(b, "sqlite3", "file:deviceidbenchent?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	targetID := seedDeviceIDLookupBench(b, client, ctx)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dev, err := client.Device.Query().Where(device.DeviceIDEQ(targetID)).Only(ctx)
		if err != nil {
			b.Fatalf("ent lookup failed: %v", err)
		}
		if dev.DeviceID != targetID {
			b.Fatalf("looked up wrong device: got %s, want %s", dev.DeviceID, targetID)
		}
	}
}

func BenchmarkDeviceIDLookup_RawSQL(b *testing.B) {
	client := enttest.Open(b, "sqlite3", "file:deviceidbenchraw?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	targetID := seedDeviceIDLookupBench(b, client, ctx)

	// A second, independent connection to the same shared-cache in-memory
	// database, bypassing ent entirely, mirroring the raw-DB-bypass
	// pattern internal/crypto/ent_hook_test.go already uses to check
	// ent's own encryption hook against ground truth.
	rawDB, err := sql.Open("sqlite3", "file:deviceidbenchraw?mode=memory&cache=shared&_fk=1")
	if err != nil {
		b.Fatalf("opening raw connection: %v", err)
	}
	defer rawDB.Close()

	const query = `SELECT name FROM devices WHERE device_id = ?`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var name string
		if err := rawDB.QueryRowContext(ctx, query, targetID).Scan(&name); err != nil {
			b.Fatalf("raw SQL lookup failed: %v", err)
		}
	}
}

// deviceIDLookupBenchBatch is the CreateBulk chunk size, kept well under
// SQLite's per-statement bound-variable limit now that each Device row
// binds around ten columns (device_id, name, properties, version, state,
// created_at, updated_at, plus whichever of source/source_synced_at/tags
// are set).
const deviceIDLookupBenchBatch = 1000

// seedDeviceIDLookupBench populates client with deviceIDLookupBenchN
// devices and returns the device_id of one near the middle of the table,
// so neither benchmark is measuring a best-case first-row or worst-case
// last-row lookup.
func seedDeviceIDLookupBench(tb testing.TB, client *ent.Client, ctx context.Context) string {
	tb.Helper()

	for start := 0; start < deviceIDLookupBenchN; start += deviceIDLookupBenchBatch {
		end := start + deviceIDLookupBenchBatch
		if end > deviceIDLookupBenchN {
			end = deviceIDLookupBenchN
		}
		builders := make([]*ent.DeviceCreate, 0, end-start)
		for i := start; i < end; i++ {
			builders = append(builders, client.Device.Create().
				SetName(fmt.Sprintf("bench-lookup-device-%d", i)).
				SetType("cisco_router"))
		}
		if err := client.Device.CreateBulk(builders...).Exec(ctx); err != nil {
			tb.Fatalf("seeding device batch [%d,%d): %v", start, end, err)
		}
	}

	mid, err := client.Device.Query().
		Where(device.NameEQ(fmt.Sprintf("bench-lookup-device-%d", deviceIDLookupBenchN/2))).
		Only(ctx)
	if err != nil {
		tb.Fatalf("finding seeded target device: %v", err)
	}
	return mid.DeviceID
}
