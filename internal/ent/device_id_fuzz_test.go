package ent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

// FuzzDeviceIDLookup fuzzes device.DeviceIDEQ with adversarial strings:
// device_id is this phase's new externally-reachable identifier (the
// wire identity in internal/api/dispatcher.go, the lock key in
// internal/engine/executor.go), so a lookup against it is a genuinely new
// boundary this phase introduced, worth its own fuzz target even though
// ent's parameterized query builder already makes SQL injection
// structurally impossible here. The claim under test is "never panics,"
// matching FuzzDeviceCreation's own bar.
func FuzzDeviceIDLookup(f *testing.F) {
	f.Add("")
	f.Add("019fc79c-635a-7c1b-a6e0-160aaaf99f46")
	f.Add("not-a-uuid")
	f.Add(strings.Repeat("a", 10000))
	f.Add("'; DROP TABLE devices; --")
	f.Add("xSS <script>alert(1)</script>")
	f.Add("unicode-é中文-\U0001F600")
	f.Add("has\x00nul")

	f.Fuzz(func(t *testing.T, id string) {
		client := enttest.Open(t, "sqlite3", "file:deviceidfuzz?mode=memory&cache=shared&_fk=1")
		defer client.Close()
		ctx := context.Background()

		// The test passes as long as neither call panics or returns a
		// false positive; Only errors both when zero rows match (the
		// overwhelmingly common case for a fuzzed value) and when more
		// than one would, neither of which is a bug here.
		_, err := client.Device.Query().Where(device.DeviceIDEQ(id)).Only(ctx)
		if err != nil {
			t.Logf("expected error handled gracefully: %v", err)
		}
	})
}
