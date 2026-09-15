// This file proves internal/ent's own generated SyncRun code.
//
// It is the newest entity in the schema (internal/ent/schema/syncrun.go) and
// gets the same RULE 0 proof every other one here does: a real SQLite-backed
// *ent.Client through enttest.Open, never a fake standing in for generated
// code. The pattern is project_test.go's.
//
// The properties worth proving are the ones that live in the schema rather
// than in the store above it: the status enum that admits only terminal
// outcomes, the required project edge that makes a run meaningless without
// the project it belongs to, and that a project's runs come back as its own
// rather than another project's.
package ent_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	entsyncrun "github.com/Subject-Void-LLC/the-pleiades/internal/ent/syncrun"
	_ "github.com/mattn/go-sqlite3"
)

// TestSyncRunLifecycle walks one attempt from creation through the edge it
// hangs off.
func TestSyncRunLifecycle(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:syncrunlifecycle?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	org := client.Organization.Create().SetName("network").SaveX(ctx)
	proj := client.Project.Create().
		SetName("automation").
		SetScmURL("https://example.invalid/automation.git").
		SetOrganizationID(org.ID).
		SaveX(ctx)

	started := time.Now().Add(-30 * time.Second)
	run := client.SyncRun.Create().
		SetStatus(entsyncrun.StatusSucceeded).
		SetRevision("3b9f1c2d4e5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c").
		SetStartedAt(started).
		SetFinishedAt(started.Add(5 * time.Second)).
		SetProjectID(proj.ID).
		SaveX(ctx)

	if run.Status != entsyncrun.StatusSucceeded {
		t.Errorf("status = %q, want succeeded", run.Status)
	}
	// The error column defaults to empty rather than null, so a reader never
	// has to distinguish "no reason" from "no value".
	if run.Error != "" {
		t.Errorf("error = %q, want empty for a succeeded run", run.Error)
	}
	if !run.FinishedAt.After(run.StartedAt) {
		t.Errorf("finished_at %v is not after started_at %v, so a duration cannot be read", run.FinishedAt, run.StartedAt)
	}

	// The edge resolves back to the project that owns it.
	owner, err := run.QueryProject().Only(ctx)
	if err != nil {
		t.Fatalf("QueryProject() = %v", err)
	}
	if owner.ID != proj.ID {
		t.Errorf("run belongs to project %d, want %d", owner.ID, proj.ID)
	}

	// And the project lists it, which is what a history section reads.
	runs, err := proj.QuerySyncRuns().All(ctx)
	if err != nil {
		t.Fatalf("QuerySyncRuns() = %v", err)
	}
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Errorf("the project lists %d runs, want the one just recorded", len(runs))
	}
}

// TestSyncRunRequiresAProject proves a run cannot exist unattached. A run
// with no project is a record of an attempt at nothing, and it would be
// invisible to every read, which all go through a project.
func TestSyncRunRequiresAProject(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:syncrunnoproject?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	now := time.Now()
	_, err := client.SyncRun.Create().
		SetStatus(entsyncrun.StatusFailed).
		SetError("host unreachable").
		SetStartedAt(now).
		SetFinishedAt(now).
		Save(ctx)
	if err == nil {
		t.Fatal("a sync run saved with no project, so a run can exist attached to nothing")
	}
}

// TestSyncRunStatusRefusesANonTerminalOutcome pins the enum. A row is
// written when an attempt finishes, so "running" belongs on the project's
// own status and never here; admitting it would strand a row on every crash.
func TestSyncRunStatusRefusesANonTerminalOutcome(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:syncrunstatus?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	org := client.Organization.Create().SetName("network").SaveX(ctx)
	proj := client.Project.Create().
		SetName("automation").
		SetScmURL("https://example.invalid/automation.git").
		SetOrganizationID(org.ID).
		SaveX(ctx)

	now := time.Now()
	_, err := client.SyncRun.Create().
		SetStatus(entsyncrun.Status("running")).
		SetStartedAt(now).
		SetFinishedAt(now).
		SetProjectID(proj.ID).
		Save(ctx)
	if err == nil {
		t.Fatal("a run saved with a running status, so the history can hold an unfinished attempt")
	}
	if !ent.IsValidationError(err) {
		t.Errorf("err = %v, want a validation error from the enum", err)
	}
}
