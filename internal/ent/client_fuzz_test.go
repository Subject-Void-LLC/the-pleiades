package ent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

// FuzzDeviceCreation tests the resilience of the SQLite driver and ent framework
// against malformed, exceedingly long, or garbage input for device names and properties.
func FuzzDeviceCreation(f *testing.F) {
	// Provide seed corpus
	f.Add("core-router-01", `{"vendor":"cisco","role":"core"}`)
	f.Add("", `{}`)
	f.Add("a", `{"key": ""}`)
	f.Add(strings.Repeat("long-name-", 100), `malformed-json`)
	f.Add("xSS <script>alert(1)</script>", `{"injection": "'; DROP TABLE devices; --"}`)

	f.Fuzz(func(t *testing.T, name string, rawProperties string) {
		client := enttest.Open(t, "sqlite3", "file:entfuzz?mode=memory&cache=shared&_fk=1")
		defer client.Close()

		ctx := context.Background()

		var props map[string]interface{}
		if err := json.Unmarshal([]byte(rawProperties), &props); err != nil {
			return // Ignore malformed JSON inputs for this fuzz run
		}

		// Attempt to create the device
		// We expect errors for constraint violations, but we NEVER expect a panic.
		_, err := client.Device.Create().
			SetName(name).
			SetType("fuzz").
			SetProperties(props).
			Save(ctx)

		// The test passes as long as there is no panic.
		if err != nil {
			t.Logf("Expected error handled gracefully: %v", err)
		}
	})
}
