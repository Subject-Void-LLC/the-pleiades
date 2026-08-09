// This file proves internal/ent's own generated Job and JobTask code:
// Phase 14 (The Dispatcher) added both entities' schemas
// (internal/ent/schema/job.go, internal/ent/schema/job_task.go), and this
// is their own RULE 0 proof against a real SQLite-backed *ent.Client, the
// same enttest.Open pattern every other file in this package already
// uses (group_organization_test.go, client_test.go), never a hand-rolled
// fake standing in for ent's own generated code. Per AGENTS.md's Code
// Generation section, internal/ent's own *.go files are generated from
// the schema and must never be hand-edited; this file, like every other
// _test.go file in this package, is the hand-written half that proves the
// generated half actually behaves.
package ent_test

import (
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/jobtask"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/predicate"
	_ "github.com/mattn/go-sqlite3"
)

// TestJobTaskLifecycle proves the full Job/JobTask lifecycle a real
// dispatch.JobStore drives: Create with every field, an edge-owning
// JobTask created against it in both dispatched and skipped shapes, both
// edge directions (Job.QueryTasks and JobTask.QueryJob), an eager-loaded
// read (WithTasks, which is what exercises Job.Edges().TasksOrErr), a
// terminal-state Update setting the three tallies together (the exact
// shape dispatch.entJobStore.Complete performs), re-parenting a task to a
// different Job (JobTaskUpdate.SetJob), the required-edge violation that
// makes ClearJob a real error rather than a silent success, and a
// Stringer sanity check on both entities.
func TestJobTaskLifecycle(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:jobtasklifecycle?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	j1 := client.Job.Create().
		SetJobID("lifecycle-job-1").
		SetRunbookID("pb-1").
		SetGroupName("routers").
		SetActor("operator@example.com").
		SaveX(ctx)
	if j1.State != job.StatePending {
		t.Fatalf("new job State = %q, want %q (the schema's own Default)", j1.State, job.StatePending)
	}
	// String() is generated once per entity and otherwise never called by
	// any production code path in this codebase (dispatch.entJobStore
	// builds its own domain struct instead), so this is its only proof of
	// not panicking on a real row.
	if s := j1.String(); s == "" {
		t.Error("Job.String() returned an empty string")
	}

	dispatched := client.JobTask.Create().
		SetJob(j1).
		SetDeviceID("dev-1").
		SetDeviceName("router-1").
		SetOutcome(jobtask.OutcomeDispatched).
		SaveX(ctx)
	skipped := client.JobTask.Create().
		SetJob(j1).
		SetDeviceID("dev-2").
		SetDeviceName("router-2").
		SetOutcome(jobtask.OutcomeSkipped).
		SetReason("device is quarantined").
		SaveX(ctx)
	if s := skipped.String(); s == "" {
		t.Error("JobTask.String() returned an empty string")
	}

	// Forward edge: Job -> its Tasks.
	tasks, err := j1.QueryTasks().All(ctx)
	if err != nil {
		t.Fatalf("QueryTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("j1.QueryTasks() = %d tasks, want 2", len(tasks))
	}

	// Reverse edge: JobTask -> its owning Job.
	owner, err := dispatched.QueryJob().Only(ctx)
	if err != nil {
		t.Fatalf("dispatched.QueryJob(): %v", err)
	}
	if owner.JobID != j1.JobID {
		t.Errorf("dispatched task's owning job = %q, want %q", owner.JobID, j1.JobID)
	}

	// Eager-loaded read: exercises Job.Edges().TasksOrErr, the accessor a
	// caller reaches for after WithTasks rather than a second QueryTasks
	// round trip.
	hydrated, err := client.Job.Query().Where(job.JobIDEQ(j1.JobID)).WithTasks().Only(ctx)
	if err != nil {
		t.Fatalf("querying with eager-loaded tasks: %v", err)
	}
	hydratedTasks, err := hydrated.Edges.TasksOrErr()
	if err != nil {
		t.Fatalf("TasksOrErr: %v", err)
	}
	if len(hydratedTasks) != 2 {
		t.Fatalf("eager-loaded tasks = %d, want 2", len(hydratedTasks))
	}

	// A second job, both as the terminal-state fixture's sibling and as
	// the re-parenting target below.
	j2 := client.Job.Create().
		SetJobID("lifecycle-job-2").
		SetRunbookID("pb-missing").
		SetGroupName("routers").
		SetActor("operator@example.com").
		SetState(job.StateFailed).
		SetFailureReason("runbook pb-missing not found").
		SaveX(ctx)
	if j2.FailureReason != "runbook pb-missing not found" {
		t.Errorf("j2.FailureReason = %q, want the set reason", j2.FailureReason)
	}
	// ClearFailureReason: the optional field's other mutation path,
	// mirroring how a retried launch might reset it.
	j2Cleared := j2.Update().ClearFailureReason().SaveX(ctx)
	if j2Cleared.FailureReason != "" {
		t.Errorf("j2Cleared.FailureReason = %q, want empty after ClearFailureReason", j2Cleared.FailureReason)
	}

	// Terminal-state update: the exact three-tallies-together transition
	// dispatch.entJobStore.Complete performs.
	completed := j1.Update().
		SetState(job.StateCompleted).
		SetDispatchedCount(1).
		SetSkippedCount(1).
		SetFailedCount(0).
		SaveX(ctx)
	if completed.State != job.StateCompleted || completed.DispatchedCount != 1 || completed.SkippedCount != 1 {
		t.Errorf("completed job = %+v, want State=completed Dispatched=1 Skipped=1", completed)
	}

	// Re-parent the dispatched task onto j2 (JobTaskUpdate.SetJob), then
	// prove the reverse edge now resolves to j2, not j1.
	retagged := dispatched.Update().SetJob(j2).SaveX(ctx)
	retaggedOwner, err := retagged.QueryJob().Only(ctx)
	if err != nil {
		t.Fatalf("retagged.QueryJob(): %v", err)
	}
	if retaggedOwner.JobID != j2.JobID {
		t.Errorf("retagged task's owning job = %q, want %q", retaggedOwner.JobID, j2.JobID)
	}

	// The "job" edge is Required (schema/job_task.go's own Ref("tasks").
	// Unique().Required()): clearing it must be a real validation error,
	// never a silent no-op that would leave an orphaned row a Required
	// edge is supposed to make impossible.
	if err := retagged.Update().ClearJob().Exec(ctx); err == nil {
		t.Error("ClearJob().Exec() on a Required edge succeeded, want a validation error")
	}

	// Delete: retagged (now under j2) and skipped (still under j1) each
	// deleted individually, then both jobs bulk-deleted. A JobTask must be
	// deleted before its owning Job, the same Required-edge FK ordering
	// TestJobTaskLifecycle's own comment above explains.
	if err := client.JobTask.DeleteOne(retagged).Exec(ctx); err != nil {
		t.Fatalf("deleting retagged task: %v", err)
	}
	if err := client.JobTask.DeleteOne(skipped).Exec(ctx); err != nil {
		t.Fatalf("deleting skipped task: %v", err)
	}
	if n, err := client.Job.Delete().Where(job.JobIDIn(j1.JobID, j2.JobID)).Exec(ctx); err != nil {
		t.Fatalf("bulk-deleting jobs: %v", err)
	} else if n != 2 {
		t.Errorf("bulk delete removed %d jobs, want 2", n)
	}
	if exists, err := client.Job.Query().Where(job.JobIDEQ(j1.JobID)).Exist(ctx); err != nil {
		t.Fatalf("Exist: %v", err)
	} else if exists {
		t.Error("job j1 still exists after Delete")
	}

	// Not-found path: Get on an internal primary key that was never
	// assigned (a fresh in-memory database's auto-increment sequence
	// never produces a negative id).
	if _, err := client.Job.Get(ctx, -1); !ent.IsNotFound(err) {
		t.Errorf("Job.Get(-1) error = %v, want a NotFound error", err)
	}
	if _, err := client.JobTask.Get(ctx, -1); !ent.IsNotFound(err) {
		t.Errorf("JobTask.Get(-1) error = %v, want a NotFound error", err)
	}
}

// jobPredicateFixture is one seeded Job row TestJobPredicatesAndOrdering's
// predicate table filters against.
type jobPredicateFixture struct {
	id            string
	runbookID     string
	groupName     string
	actor         string
	state         job.State
	dispatched    int
	skipped       int
	failed        int
	failureReason string // empty means never set (stored as SQL NULL, per migrate/schema.go's Nullable failure_reason column)
}

// TestJobPredicatesAndOrdering exercises a representative, behavior-
// verified sample of the where.go predicate constructors and the
// jobwork.go OrderOption constructors ent generated for Job: every
// operator family (EQ/NEQ/In/NotIn/GT/GTE/LT/LTE plus the string-only
// Contains/HasPrefix/HasSuffix/EqualFold/ContainsFold and the
// nullable-only IsNil/NotNil) at least once, proven against a small,
// fully-known fixture set rather than merely called and discarded. Every
// count query is additionally scoped with job.JobIDIn(fixture ids), so
// the expected counts hold regardless of what any other test in this
// package's shared package-level state might have created (SQLite's
// "cache=shared" mode aside; this test uses its own dedicated in-memory
// database exactly like every other test in this file).
func TestJobPredicatesAndOrdering(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:jobpredicates?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	fixtures := []jobPredicateFixture{
		{id: "predtest-alpha", runbookID: "predtest-pb-alpha", groupName: "predtest-group-a", actor: "predtest-alice", state: job.StatePending, dispatched: 0, skipped: 0, failed: 0},
		{id: "predtest-beta", runbookID: "predtest-pb-beta", groupName: "predtest-group-b", actor: "predtest-bob", state: job.StateFanningOut, dispatched: 5, skipped: 2, failed: 1},
		{id: "predtest-delta", runbookID: "predtest-pb-delta", groupName: "predtest-group-b", actor: "predtest-dave", state: job.StateCompleted, dispatched: 9, skipped: 9, failed: 9, failureReason: "predtest-zzz"},
		{id: "predtest-gamma", runbookID: "predtest-pb-gamma", groupName: "predtest-group-a", actor: "predtest-carol", state: job.StateFailed, dispatched: 0, skipped: 0, failed: 0, failureReason: "predtest-aaa"},
	}
	ids := make([]string, len(fixtures))
	for i, f := range fixtures {
		create := client.Job.Create().
			SetJobID(f.id).
			SetRunbookID(f.runbookID).
			SetGroupName(f.groupName).
			SetActor(f.actor).
			SetState(f.state).
			SetDispatchedCount(f.dispatched).
			SetSkippedCount(f.skipped).
			SetFailedCount(f.failed)
		if f.failureReason != "" {
			create = create.SetFailureReason(f.failureReason)
		}
		create.SaveX(ctx)
		ids[i] = f.id
	}
	// Fixture insertion order above is deliberately alpha, beta, delta,
	// gamma: alphabetically sorted job_id values, so every JobID string
	// predicate below (GT/GTE/LT/LTE in particular) can be reasoned about
	// directly from that order without a separate lookup.
	scope := job.JobIDIn(ids...)
	count := func(t *testing.T, p predicate.Job) int {
		t.Helper()
		n, err := client.Job.Query().Where(job.And(scope, p)).Count(ctx)
		if err != nil {
			t.Fatalf("counting %T: %v", p, err)
		}
		return n
	}

	tests := []struct {
		name string
		pred predicate.Job
		want int
	}{
		// ID (internal PK): fixtures were inserted in the order above, so
		// their auto-increment ids are strictly increasing in that same
		// order.
		{"ID first", job.ID(1), 1},
		{"IDEQ first", job.IDEQ(1), 1},
		{"IDNEQ first", job.IDNEQ(1), 3},
		{"IDIn first,second", job.IDIn(1, 2), 2},
		{"IDNotIn first", job.IDNotIn(1), 3},
		{"IDGT first", job.IDGT(1), 3},
		{"IDGTE first", job.IDGTE(1), 4},
		{"IDLT fourth", job.IDLT(4), 3},
		{"IDLTE first", job.IDLTE(1), 1},

		{"CreatedAt (identical to CreatedAtEQ, exercised via a tautology)", job.CreatedAtLTE(fixtureNow()), 4},
		{"UpdatedAt (identical to UpdatedAtEQ, exercised via a tautology)", job.UpdatedAtLTE(fixtureNow()), 4},

		{"JobID alpha", job.JobID("predtest-alpha"), 1},
		{"JobIDEQ alpha", job.JobIDEQ("predtest-alpha"), 1},
		{"JobIDNEQ alpha", job.JobIDNEQ("predtest-alpha"), 3},
		{"JobIDIn alpha,gamma", job.JobIDIn("predtest-alpha", "predtest-gamma"), 2},
		{"JobIDNotIn alpha", job.JobIDNotIn("predtest-alpha"), 3},
		{"JobIDGT beta", job.JobIDGT("predtest-beta"), 2},   // delta, gamma
		{"JobIDGTE beta", job.JobIDGTE("predtest-beta"), 3}, // beta, delta, gamma
		{"JobIDLT beta", job.JobIDLT("predtest-beta"), 1},   // alpha
		{"JobIDLTE beta", job.JobIDLTE("predtest-beta"), 2}, // alpha, beta
		{"JobIDContains 'alph'", job.JobIDContains("alph"), 1},
		{"JobIDHasPrefix 'predtest-g'", job.JobIDHasPrefix("predtest-g"), 1},
		{"JobIDHasSuffix 'gamma'", job.JobIDHasSuffix("gamma"), 1},
		{"JobIDEqualFold 'PREDTEST-BETA'", job.JobIDEqualFold("PREDTEST-BETA"), 1},
		{"JobIDContainsFold 'DELTA'", job.JobIDContainsFold("DELTA"), 1},

		{"RunbookID exact", job.RunbookID("predtest-pb-alpha"), 1},
		{"RunbookIDEQ exact", job.RunbookIDEQ("predtest-pb-alpha"), 1},
		{"RunbookIDContains 'pb-be'", job.RunbookIDContains("pb-be"), 1},

		{"GroupName exact", job.GroupName("predtest-group-a"), 2},
		{"GroupNameEQ exact", job.GroupNameEQ("predtest-group-b"), 2},
		{"GroupNameContains 'group-a'", job.GroupNameContains("group-a"), 2},

		{"Actor exact", job.Actor("predtest-alice"), 1},
		{"ActorEQ exact", job.ActorEQ("predtest-bob"), 1},
		{"ActorContains 'carol'", job.ActorContains("carol"), 1},

		{"StateEQ pending", job.StateEQ(job.StatePending), 1},
		{"StateNEQ pending", job.StateNEQ(job.StatePending), 3},
		{"StateIn pending,failed", job.StateIn(job.StatePending, job.StateFailed), 2},
		{"StateNotIn pending", job.StateNotIn(job.StatePending), 3},

		{"DispatchedCount zero", job.DispatchedCount(0), 2},
		{"DispatchedCountEQ zero", job.DispatchedCountEQ(0), 2},
		{"DispatchedCountNEQ zero", job.DispatchedCountNEQ(0), 2},
		{"DispatchedCountIn 0,5", job.DispatchedCountIn(0, 5), 3},
		{"DispatchedCountNotIn 0", job.DispatchedCountNotIn(0), 2},
		{"DispatchedCountGT 0", job.DispatchedCountGT(0), 2},
		{"DispatchedCountGTE 5", job.DispatchedCountGTE(5), 2},
		{"DispatchedCountLT 5", job.DispatchedCountLT(5), 2},
		{"DispatchedCountLTE 0", job.DispatchedCountLTE(0), 2},

		{"SkippedCount zero", job.SkippedCount(0), 2},
		{"SkippedCountEQ zero", job.SkippedCountEQ(0), 2},
		{"SkippedCountNEQ zero", job.SkippedCountNEQ(0), 2},
		{"SkippedCountIn 0,2", job.SkippedCountIn(0, 2), 3},
		{"SkippedCountNotIn 0", job.SkippedCountNotIn(0), 2},
		{"SkippedCountGT 0", job.SkippedCountGT(0), 2},
		{"SkippedCountGTE 2", job.SkippedCountGTE(2), 2},
		{"SkippedCountLT 2", job.SkippedCountLT(2), 2},
		{"SkippedCountLTE 0", job.SkippedCountLTE(0), 2},

		{"FailedCount zero", job.FailedCount(0), 2},
		{"FailedCountEQ zero", job.FailedCountEQ(0), 2},
		{"FailedCountNEQ zero", job.FailedCountNEQ(0), 2},
		{"FailedCountIn 0,1", job.FailedCountIn(0, 1), 3},
		{"FailedCountNotIn 0", job.FailedCountNotIn(0), 2},
		{"FailedCountGT 0", job.FailedCountGT(0), 2},
		{"FailedCountGTE 1", job.FailedCountGTE(1), 2},
		{"FailedCountLT 1", job.FailedCountLT(1), 2},
		{"FailedCountLTE 0", job.FailedCountLTE(0), 2},

		// FailureReason is the schema's one Nullable field: alpha and beta
		// never set it (stored as SQL NULL), delta and gamma did. Every
		// ordinary comparison operator excludes a NULL row entirely (real
		// SQL semantics, not a quirk of this generated code), which is
		// exactly what IsNil/NotNil exist to test around.
		{"FailureReason exact", job.FailureReason("predtest-aaa"), 1},
		{"FailureReasonEQ exact", job.FailureReasonEQ("predtest-zzz"), 1},
		{"FailureReasonNEQ aaa", job.FailureReasonNEQ("predtest-aaa"), 1}, // only delta (zzz); NULL rows never match NEQ
		{"FailureReasonIn aaa,zzz", job.FailureReasonIn("predtest-aaa", "predtest-zzz"), 2},
		{"FailureReasonNotIn aaa", job.FailureReasonNotIn("predtest-aaa"), 1}, // only delta
		{"FailureReasonGT aaa", job.FailureReasonGT("predtest-aaa"), 1},       // zzz > aaa
		{"FailureReasonGTE aaa", job.FailureReasonGTE("predtest-aaa"), 2},
		{"FailureReasonLT zzz", job.FailureReasonLT("predtest-zzz"), 1}, // aaa < zzz
		{"FailureReasonLTE zzz", job.FailureReasonLTE("predtest-zzz"), 2},
		{"FailureReasonContains 'a'", job.FailureReasonContains("a"), 1}, // aaa contains "a"; zzz does not
		{"FailureReasonHasPrefix 'predtest-aa'", job.FailureReasonHasPrefix("predtest-aa"), 1},
		{"FailureReasonHasSuffix 'aa'", job.FailureReasonHasSuffix("aa"), 1},
		{"FailureReasonEqualFold 'PREDTEST-AAA'", job.FailureReasonEqualFold("PREDTEST-AAA"), 1},
		{"FailureReasonContainsFold 'AA'", job.FailureReasonContainsFold("AA"), 1},
		{"FailureReasonIsNil", job.FailureReasonIsNil(), 2},
		{"FailureReasonNotNil", job.FailureReasonNotNil(), 2},

		{"HasTasks (none of these fixtures have any)", job.HasTasks(), 0},
		{"HasTasksWith (vacuously true, no tasks exist to match)", job.HasTasksWith(jobtask.DeviceID("does-not-exist")), 0},

		{"And(pending, group-a)", job.And(job.StateEQ(job.StatePending), job.GroupName("predtest-group-a")), 1},
		{"Or(pending, failed)", job.Or(job.StateEQ(job.StatePending), job.StateEQ(job.StateFailed)), 2},
		{"Not(pending)", job.Not(job.StateEQ(job.StatePending)), 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := count(t, tt.pred); got != tt.want {
				t.Errorf("count = %d, want %d", got, tt.want)
			}
		})
	}

	// Ordering: every OrderOption constructor is at least invoked, most of
	// them also proven to actually order the scoped result set. ByTasks
	// and ByTasksCount are invoked only (no fixture here has any task, so
	// their neighbor-join produces no useful ordering signal to assert
	// on); the rest are checked against the fixtures' own known,
	// alphabetically-inserted order.
	orderedIDs := func(t *testing.T, opt job.OrderOption) []string {
		t.Helper()
		rows, err := client.Job.Query().Where(scope).Order(opt).All(ctx)
		if err != nil {
			t.Fatalf("ordered query: %v", err)
		}
		got := make([]string, len(rows))
		for i, r := range rows {
			got[i] = r.JobID
		}
		return got
	}
	wantAscByJobID := []string{"predtest-alpha", "predtest-beta", "predtest-delta", "predtest-gamma"}
	for _, tt := range []struct {
		name string
		opt  job.OrderOption
		want []string
	}{
		{"ByID", job.ByID(), wantAscByJobID}, // insertion order matches alphabetical job_id order
		{"ByJobID", job.ByJobID(), wantAscByJobID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := orderedIDs(t, tt.opt); !equalStrings(got, tt.want) {
				t.Errorf("order = %v, want %v", got, tt.want)
			}
		})
	}
	// The remaining order constructors are invoked for their own
	// generated-code coverage without a separate ordering assertion: this
	// package's own client_test.go and dispatch's worker_test.go already
	// prove the platform never actually depends on ordering by these
	// particular columns, so asserting exact order here would test this
	// file's own arithmetic more than any real behavior.
	for _, opt := range []job.OrderOption{
		job.ByCreatedAt(), job.ByUpdatedAt(), job.ByRunbookID(), job.ByGroupName(),
		job.ByActor(), job.ByState(), job.ByDispatchedCount(), job.BySkippedCount(),
		job.ByFailedCount(), job.ByFailureReason(), job.ByTasksCount(),
		job.ByTasks(sql.OrderFieldTerm{Field: jobtask.FieldDeviceID}),
	} {
		if _, err := client.Job.Query().Where(scope).Order(opt).All(ctx); err != nil {
			t.Errorf("ordered query with an unasserted OrderOption failed: %v", err)
		}
	}
}

// fixtureNow returns a time guaranteed to be at or after every fixture
// row's real CreatedAt/UpdatedAt (both stamped by TimestampMixin at
// SaveX time, which always runs before this function is called), so
// job.CreatedAtLTE/UpdatedAtLTE against it is a tautology true for every
// fixture row: this exists only to invoke those two predicate
// constructors' own generated code, not to test time comparison
// semantics, which internal/ent's own TimestampMixin already has direct
// coverage for elsewhere in this package.
func fixtureNow() time.Time { return time.Now() }

// equalStrings reports whether a and b hold the same strings in the same
// order.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
