package inventory_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// sqliteBulkInsertBatch is the row count per CreateBulk call these
// iterator tests use to stay under SQLite's per-statement bound-variable
// limit (32766 in the mattn/go-sqlite3 build this repo uses). Each Device
// row now binds around ten columns (device_id, name, properties, version,
// state, created_at, updated_at, plus whichever of source/
// source_synced_at/tags are set), so a batch size that was safe before
// this phase's schema growth is not necessarily safe after it. This
// constant leaves comfortable headroom rather than tracking the exact
// column count as the schema keeps growing.
const sqliteBulkInsertBatch = 1000

// bulkCreateDevices executes builders through client.Device.CreateBulk,
// chunked into sqliteBulkInsertBatch-sized calls, so a large slice never
// exceeds SQLite's per-statement bound-variable limit in one call.
func bulkCreateDevices(tb testing.TB, ctx context.Context, client *ent.Client, builders []*ent.DeviceCreate) {
	tb.Helper()
	for start := 0; start < len(builders); start += sqliteBulkInsertBatch {
		end := start + sqliteBulkInsertBatch
		if end > len(builders) {
			end = len(builders)
		}
		if err := client.Device.CreateBulk(builders[start:end]...).Exec(ctx); err != nil {
			tb.Fatalf("failed to insert device batch [%d,%d): %v", start, end, err)
		}
	}
}
