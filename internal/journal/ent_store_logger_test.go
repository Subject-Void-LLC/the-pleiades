// Package journal_test: the store's own warnings reach the logger a
// composition root hands it.
package journal_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// TestEntStoreLogsToTheLoggerItIsGiven: a composition root's logger takes
// the store's warnings, and a nil one leaves the logger before it in place.
func TestEntStoreLogsToTheLoggerItIsGiven(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", filepath.Join(t.TempDir(), "journal.db"))
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	var logged bytes.Buffer
	store := journal.NewEntStore(client, journal.WithStoreLogger(slog.New(slog.NewTextHandler(&logged, nil))), journal.WithStoreLogger(nil))
	bad := walkEntry("job-3", "device-1", 1, 1, "tasks[0]")
	bad.InverseSpecDigest = "sha256:short"
	if _, err := store.Save(context.Background(), []engine.JournalEntry{bad}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !strings.Contains(logged.String(), "node=tasks[0]") || !strings.Contains(logged.String(), "undo digest") {
		t.Errorf("the given logger holds %q, want the cleared undo digest named", logged.String())
	}
}
