// This file runs internal/storage/rowlocktest's project scenario on a real
// SQLite file opened through ent.OpenEmbedded, the Controller's own path: an
// edit keeping a legacy URL and an edit replacing it never interleave, so the
// first cannot write the legacy URL back over the second's fix. The same
// scenario runs on PostgreSQL in rowlocktest's own tests.
package project_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
	"github.com/Subject-Void-LLC/the-pleiades/internal/storage/rowlocktest"
)

// TestStore_EditsOfOneProjectNeverInterleave_SQLite is the scenario on SQLite.
func TestStore_EditsOfOneProjectNeverInterleave_SQLite(t *testing.T) {
	client, err := ent.OpenEmbedded(context.Background(), filepath.Join(t.TempDir(), "projects.db"))
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	rowlocktest.ProjectEditsNeverInterleave(t, client, project.NewEntStore(client, project.SourcePolicy{}), secretSourceURL)
}
