// Tests that a Walk rollback's planned steps carry the Collection identity
// and undo digest their journal entries recorded (Phase 136b).
package api_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestRollback_AStepCarriesItsJournalIdentity: the rollback job's stored
// plan, which each device's dispatch carries to its Runner, holds for a
// recorded undo the identity and undo digest the forward entry recorded.
func TestRollback_AStepCarriesItsJournalIdentity(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	identity := wire.CollectionIdentity{Group: "file", Version: "0.4.0", Manifests: wire.ManifestDigestPrefix + strings.Repeat("a", 64)}
	undo := wire.ManifestDigestPrefix + strings.Repeat("b", 64)
	made := &f.journal.entries[f.jobID][0]
	made.Collection, made.InverseSpecDigest = identity, undo

	id, err := f.dispatcher.Rollback(t.Context(), "grace@example.com", f.jobID, api.RollbackRequest{Mode: collection.ModeExecute, Leave: []string{"tasks[1]"}})
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := f.jobs.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if job.Rollback == nil || len(job.Rollback.Devices) != 1 || len(job.Rollback.Devices[0].Steps) != 1 {
		t.Fatalf("the rollback plan %+v", job.Rollback)
	}
	if step := job.Rollback.Devices[0].Steps[0]; step.Collection != identity || step.UndoDigest != undo {
		t.Errorf("the step carries %+v and %q, want %+v and %q as the journal recorded them", step.Collection, step.UndoDigest, identity, undo)
	}
}
