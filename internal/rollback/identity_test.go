// Tests that a rollback plan carries the Collection identity and undo digest
// the journal recorded for each recorded undo (Phase 136b).
package rollback_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestBuild_ARecordedStepCarriesItsJournalIdentity: a recorded undo's step
// carries the identity and undo digest its entry recorded, as recorded, for
// the Runner that runs it; an entry that recorded neither (one written
// before the journal did) gives a step with neither, and an authored step,
// which the runbook rather than a manifest describes, carries neither. The
// identity is one no build of the fixture computes, so the step is planned
// only because allow_skew names it (skew_test.go covers that).
func TestBuild_ARecordedStepCarriesItsJournalIdentity(t *testing.T) {
	registerShapes(t)
	identity := wire.CollectionIdentity{Group: "rb", Version: "0.4.0", Manifests: wire.ManifestDigestPrefix + strings.Repeat("e", 64)}
	undoDigest := wire.ManifestDigestPrefix + strings.Repeat("f", 64)
	made := undo(entry("run-A", "tasks[0]", "h1", 1, "rb.make", engine.OutcomeChanged), "rb.delete", map[string]any{"name": "bsd", "uuid": "u-1"})
	made.Collection, made.InverseSpecDigest = identity, undoDigest
	started := undo(entry("run-A", "tasks[1]", "h1", 2, "rb.start", engine.OutcomeChanged), "rb.stop", map[string]any{"name": "bsd"})
	run := rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{made, started}}

	plan, err := rollback.Build(rollback.Request{Target: run, Devices: inventoryOf("h1"), AllowSkew: map[string]bool{"tasks[0]": true}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	byNode := map[string]rollback.Step{}
	for _, s := range plan.Steps() {
		byNode[s.Node] = s
	}
	if s := byNode["tasks[0]"]; s.Collection != identity || s.UndoDigest != undoDigest || !s.SkewAccepted {
		t.Errorf("the recorded step carries %+v and %q, want %+v and %q as the journal recorded them", s.Collection, s.UndoDigest, identity, undoDigest)
	}
	if s := byNode["tasks[1]"]; !s.Collection.IsZero() || s.UndoDigest != "" {
		t.Errorf("a step from an entry that recorded no identity carries %+v and %q", s.Collection, s.UndoDigest)
	}

	authored := entry("run-A", "tasks[0]", "h1", 1, "rb.make", engine.OutcomeChanged)
	authored.AuthoredRollback = true
	authored.Collection, authored.InverseSpecDigest = identity, undoDigest
	plan, err = rollback.Build(rollback.Request{
		Target: rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{authored}}, Devices: inventoryOf("h1"),
		Authored: func(string) ([]engine.Task, error) {
			return []engine.Task{{Name: "remove it", FQCN: "rb.delete", Params: map[string]any{"name": "bsd"}}}, nil
		},
	})
	if err != nil {
		t.Fatalf("Build with an authored undo: %v", err)
	}
	for _, s := range plan.Steps() {
		if !s.Collection.IsZero() || s.UndoDigest != "" {
			t.Errorf("an authored step carries %+v and %q", s.Collection, s.UndoDigest)
		}
	}
}
