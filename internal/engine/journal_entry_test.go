// These tests cover Phase 40's projection: what Executor.Run actually
// writes to a Journal sink, level by level, for a real compiled DAG.
//
// They run the real Builder, the real Executor and, where the claim is
// about a specific executor's own output, that executor rather than a
// stand-in, because the claims are about construction sites. A test that
// built a JournalEntry itself and then asserted its fields would prove
// only that a struct literal holds what it was given.
//
// The recurring assertion is two-sided, following the shape
// FAILURE_PATTERNS.md #120 records as missing when over-masking once
// shipped and passed every unit test: an entry must not carry a value,
// AND it must still name the method, the device, the node and the reason.
// A projection that dropped everything would pass a one-sided test.
package engine_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// journalSink is a Journal test double that keeps every Record call.
//
// It carries no lock, and that is an assertion rather than an oversight:
// Run calls Record from its own goroutine at the level barrier, after
// runConcurrently has joined every node goroutine of that level, so a
// projection that ever wrote from a device goroutine would show up as a
// data race under the suite's own -race runs rather than as a subtle
// ordering bug.
type journalSink struct {
	calls [][]engine.JournalEntry
	err   error
}

// Record implements engine.Journal by keeping entries and reporting the
// configured error, if any.
func (s *journalSink) Record(_ context.Context, entries []engine.JournalEntry) error {
	s.calls = append(s.calls, entries)
	return s.err
}

// flat returns every entry this sink was handed, in the order it received
// them, for an assertion about the run as a whole rather than about one
// level.
func (s *journalSink) flat() []engine.JournalEntry {
	var all []engine.JournalEntry
	for _, call := range s.calls {
		all = append(all, call...)
	}
	return all
}

// byNode indexes a sink's entries by node id and device, the pair that
// identifies one execution, so an assertion names what it is about
// instead of indexing into a slice whose order it also has to know.
func byNode(entries []engine.JournalEntry) map[string]engine.JournalEntry {
	indexed := make(map[string]engine.JournalEntry, len(entries))
	for _, e := range entries {
		indexed[e.NodeID+"|"+e.DeviceID] = e
	}
	return indexed
}

// runJournaled compiles payload, runs it with sink wired, and returns
// both the run's own result and the sink, so a test can assert the
// journal against the execution it describes rather than in isolation.
func runJournaled(t *testing.T, payload string, resolver engine.TargetResolver, actions engine.ActionExecutor, sink *journalSink) engine.RunResult {
	t.Helper()
	dag := buildDAG(t, payload)
	x := engine.NewExecutor(resolver, actions, lock.NewInProcessManager(), event.NewInProcessBus(),
		engine.NewInProcessWorkflowContext(), 0, engine.WithJournal(sink))

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	return result
}

// TestJournalRecordsOneLevelPerCall is the shape of the whole feature: a
// three-level runbook produces three Record calls, in order, each
// carrying that level's entries and nothing else, numbered densely across
// the run.
//
// It also covers three of the five outcomes at once, because they differ
// only in which branch of the projection's outcome switch they take: a
// task that ran unchanged, one that reported a change, and one whose
// condition evaluated false.
func TestJournalRecordsOneLevelPerCall(t *testing.T) {
	sink := &journalSink{}
	result := runJournaled(t, `{
		"id": "journal-demo",
		"tasks": [
			{"name": "precheck", "fqcn": "noop", "register": "pre", "params": {"needs_reboot": true}},
			{"name": "apply", "fqcn": "noop", "when_cel": "stat.pre[\"\"].needs_reboot == true", "params": {"changed": true}},
			{"name": "never", "fqcn": "noop", "when_cel": "stat.pre[\"\"].needs_reboot == false"}
		]
	}`, mapResolver{}, engine.NewBuiltinActionExecutor(), sink)

	if result.HasErrors() {
		t.Fatalf("expected a clean run, got %+v", result.Nodes)
	}
	if len(sink.calls) != 3 {
		t.Fatalf("expected one Record call per topological level, got %d calls: %+v", len(sink.calls), sink.calls)
	}
	for i, call := range sink.calls {
		if len(call) != 1 {
			t.Fatalf("level %d carried %d entries, want the level's single node", i+1, len(call))
		}
	}

	entries := sink.flat()

	// Sequence is dense and 1-based across the whole run, not restarted
	// per level: it is what orders entries a concurrent level cannot order
	// by timestamp.
	for i, e := range entries {
		if e.Sequence != i+1 {
			t.Errorf("entry %d has Sequence %d, want %d: the numbering must be dense across the run", i, e.Sequence, i+1)
		}
	}

	// One run, one RunID, on every entry.
	for _, e := range entries {
		if e.RunID == "" {
			t.Fatalf("entry %+v carries no RunID", e)
		}
		if e.RunID != entries[0].RunID {
			t.Errorf("entry %q carries RunID %q, want the run's own %q", e.NodeID, e.RunID, entries[0].RunID)
		}
		if e.DAGID != "journal-demo" {
			t.Errorf("entry %q carries DAGID %q, want the runbook's own id", e.NodeID, e.DAGID)
		}
		if !strings.HasPrefix(e.DAGVersion, "sha256:") {
			t.Errorf("entry %q carries DAGVersion %q, want the computed sha256:<hex> digest", e.NodeID, e.DAGVersion)
		}
		if e.JobID != "" || e.Attempt != 0 {
			t.Errorf("entry %q carries JobID %q attempt %d: both belong to the Walk sink, which stamps them from the dispatch payload",
				e.NodeID, e.JobID, e.Attempt)
		}
	}

	indexed := byNode(entries)

	ran := indexed["tasks[0]|"]
	if ran.Outcome != engine.OutcomeRan {
		t.Errorf("precheck recorded outcome %q, want %q", ran.Outcome, engine.OutcomeRan)
	}
	if ran.FQCN != "noop" || ran.FQCNUnresolved {
		t.Errorf("precheck recorded FQCN %q (unresolved %v), want the engine builtin resolved", ran.FQCN, ran.FQCNUnresolved)
	}
	if ran.TaskName != "precheck" || ran.Register != "pre" {
		t.Errorf("precheck recorded name %q register %q, want the runbook's own labels", ran.TaskName, ran.Register)
	}
	// noop echoes the task's own params into its stats, so both key
	// vectors are author text and neither may be named. Counting them is
	// the projection refusing to vouch for a name no Doc declares.
	if len(ran.StatKeys) != 0 || ran.UndeclaredStatCount != 1 {
		t.Errorf("precheck recorded StatKeys %v undeclared %d, want none named and one counted: noop's stats are the task's own params",
			ran.StatKeys, ran.UndeclaredStatCount)
	}
	if len(ran.ParamKeys) != 0 || ran.UndeclaredParamCount != 1 {
		t.Errorf("precheck recorded ParamKeys %v undeclared %d, want none named and one counted", ran.ParamKeys, ran.UndeclaredParamCount)
	}
	if ran.StartedAt.IsZero() || ran.FinishedAt.Before(ran.StartedAt) {
		t.Errorf("precheck recorded bounds [%v, %v], want the interval the executor stamped", ran.StartedAt, ran.FinishedAt)
	}

	if changed := indexed["tasks[1]|"]; changed.Outcome != engine.OutcomeChanged {
		t.Errorf("apply recorded outcome %q, want %q", changed.Outcome, engine.OutcomeChanged)
	}

	skipped := indexed["tasks[2]|"]
	if skipped.Outcome != engine.OutcomeSkipped || skipped.SkipKind != engine.SkipKindWhenCEL {
		t.Errorf("never recorded outcome %q kind %q, want a when_cel skip", skipped.Outcome, skipped.SkipKind)
	}
	// A lone when_cel is condition 1 of 1, the numbers evalAnd computed
	// for its own reason sentence. The sentence itself is never stored: it
	// quotes the author's expression verbatim.
	if skipped.SkipOrdinal != 1 || skipped.SkipTotal != 1 {
		t.Errorf("never recorded ordinal %d of %d, want 1 of 1", skipped.SkipOrdinal, skipped.SkipTotal)
	}
}

// TestJournalMintsANewRunIDPerRun proves RunID identifies one Run call
// rather than one Executor, which is what makes it safe to group by. A
// reused Executor is explicitly supported (Executor holds no per-run
// state), so an id hung off it would merge two executions into one.
func TestJournalMintsANewRunIDPerRun(t *testing.T) {
	dag := buildDAG(t, `{"id": "reused", "tasks": [{"name": "a", "fqcn": "noop"}]}`)
	sink := &journalSink{}
	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(),
		event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, engine.WithJournal(sink))

	for i := 0; i < 2; i++ {
		if _, err := x.Run(context.Background(), dag); err != nil {
			t.Fatalf("run %d failed: %v", i, err)
		}
	}

	entries := sink.flat()
	if len(entries) != 2 {
		t.Fatalf("expected one entry per run, got %d", len(entries))
	}
	if entries[0].RunID == entries[1].RunID {
		t.Errorf("both runs recorded RunID %q; a reused Executor must still mint one per Run call", entries[0].RunID)
	}
	// Sequence restarts with the run, because it orders entries inside one
	// run and nothing else.
	if entries[0].Sequence != 1 || entries[1].Sequence != 1 {
		t.Errorf("recorded sequences %d and %d, want each run to start at 1", entries[0].Sequence, entries[1].Sequence)
	}
}

// TestJournalRecordsTheFailureStage proves a failed node records WHICH
// stage failed, read off the executor's control flow, and records no part
// of the error's own text. NodeResult.Err embeds device output verbatim
// and the engine contractually never masks it, so the stage is the only
// way to record a failure at all.
func TestJournalRecordsTheFailureStage(t *testing.T) {
	sink := &journalSink{}
	runJournaled(t, `{"id": "doomed", "tasks": [{"name": "doomed", "fqcn": "noop"}]}`,
		mapResolver{}, failingActionExecutor{}, sink)

	entries := sink.flat()
	if len(entries) != 1 {
		t.Fatalf("expected the failed node to still be journaled, got %d entries", len(entries))
	}
	e := entries[0]
	if e.Outcome != engine.OutcomeFailed {
		t.Errorf("recorded outcome %q, want %q", e.Outcome, engine.OutcomeFailed)
	}
	if e.FailureStage != engine.FailureStageAction {
		t.Errorf("recorded stage %q, want %q: the ActionExecutor.Execute call is what failed", e.FailureStage, engine.FailureStageAction)
	}
	// The two-sided half: the entry still names the node and the method,
	// so a refusal to store the error text has not cost the record its
	// point.
	if e.NodeID != "tasks[0]" || e.FQCN != "noop" {
		t.Errorf("recorded node %q fqcn %q, want the failing node named", e.NodeID, e.FQCN)
	}
}

// TestJournalRecordsSkipsAndSyntheticMarkers covers the two outcomes the
// happy path cannot reach: a device whose lifecycle state does not admit
// real work, and the synthetic parallel fan-out/join marker, which
// executes nothing at all.
func TestJournalRecordsSkipsAndSyntheticMarkers(t *testing.T) {
	active := &inventorytest.Stub{StubID: "active-host", StubName: "active-host", StubState: inventory.StateActive}
	quarantined := &inventorytest.Stub{StubID: "quarantined-host", StubName: "quarantined-host", StubState: inventory.StateQuarantined}

	sink := &journalSink{}
	runJournaled(t, `{
		"id": "shapes",
		"tasks": [
			{"name": "fanout", "parallel": [
				{"name": "p0", "fqcn": "noop"},
				{"name": "p1", "fqcn": "noop"}
			]},
			{"name": "work", "fqcn": "noop", "params": {"target": "fleet"}}
		]
	}`, mapResolver{"fleet": {active, quarantined}}, engine.NewBuiltinActionExecutor(), sink)

	indexed := byNode(sink.flat())

	for _, marker := range []string{"tasks[0].fanout", "tasks[0].join"} {
		e, ok := indexed[marker+"|"]
		if !ok {
			t.Fatalf("no entry for the synthetic marker %q", marker)
		}
		if e.Outcome != engine.OutcomeNotReached {
			t.Errorf("%s recorded outcome %q, want %q", marker, e.Outcome, engine.OutcomeNotReached)
		}
		// A marker names no method at all, so its FQCN is empty rather
		// than the FQCNUnregistered sentinel, which would claim a name was
		// written and dropped.
		if e.FQCN != "" || e.FQCNUnresolved {
			t.Errorf("%s recorded FQCN %q (unresolved %v), want an empty name and no dropped-name flag",
				marker, e.FQCN, e.FQCNUnresolved)
		}
		if !e.StartedAt.IsZero() || !e.FinishedAt.IsZero() {
			t.Errorf("%s executed nothing, so both bounds must stay zero, got [%v, %v]", marker, e.StartedAt, e.FinishedAt)
		}
	}

	skipped, ok := indexed["tasks[1]|quarantined-host"]
	if !ok {
		t.Fatalf("no entry for the lifecycle-skipped device; entries were %v", indexed)
	}
	if skipped.Outcome != engine.OutcomeSkipped || skipped.SkipKind != engine.SkipKindLifecycle {
		t.Errorf("recorded outcome %q kind %q, want a lifecycle skip", skipped.Outcome, skipped.SkipKind)
	}
	// Naming which device caused it is the whole point of this skip, so
	// unlike a condition skip it keeps DeviceID populated.
	if skipped.DeviceID != "quarantined-host" {
		t.Errorf("recorded device %q, want the device whose state caused the skip", skipped.DeviceID)
	}
	if skipped.SkipOrdinal != 0 || skipped.SkipTotal != 0 {
		t.Errorf("recorded ordinal %d of %d, want zeros: a lifecycle skip is not a condition of a list",
			skipped.SkipOrdinal, skipped.SkipTotal)
	}
	if ran := indexed["tasks[1]|active-host"]; ran.Outcome != engine.OutcomeRan {
		t.Errorf("the admissible device recorded outcome %q, want %q", ran.Outcome, engine.OutcomeRan)
	}
}

// TestJournalAdmitsOnlyDeclaredKeys is the key rule end to end, through a
// really registered Collection method with a real Doc: a declared stat is
// named, an undeclared one is counted, the two pkg/sdk stat keys are
// admitted with no Doc vouching for them, and nothing one level down is
// ever named.
func TestJournalAdmitsOnlyDeclaredKeys(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())

	const name = "enginejournal.declared"
	if err := collection.Register(collection.Descriptor{
		Name: name,
		Manifest: collection.Manifest{
			Status:        collection.StatusImplemented,
			Reversibility: collection.Reversibility{Reversible: true},
			Doc: collection.Doc{
				Summary: "A fixture whose Doc declares exactly one param and one return.",
				Params:  []collection.Param{{Name: "path", Type: "string", Description: "A declared param."}},
				Returns: []collection.ReturnField{{Name: "mode", Type: "string", Description: "A declared return."}},
			},
		},
		Invoke: func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			// One declared stat, one the Doc has never heard of, and the
			// two pkg/sdk records. The undeclared one carries a nested map
			// whose keys are exactly the shape a device supplies.
			if err := rc.SetStat("mode", "0644"); err != nil {
				return collection.Result{}, err
			}
			if err := rc.SetStat("headers", map[string]any{"set-cookie": "session=hunter2"}); err != nil {
				return collection.Result{}, err
			}
			if err := sdk.RecordDiff(rc, sdk.Diff{
				Before: map[string]any{"content": "the whole prior file"},
				After:  map[string]any{"content": "the whole new file"},
			}); err != nil {
				return collection.Result{}, err
			}
			if err := sdk.RecordInverse(rc, sdk.Inverse{
				FQCN:   name,
				Params: map[string]any{"path": "/etc/hosts", "content": "the whole prior file"},
			}); err != nil {
				return collection.Result{}, err
			}
			return collection.Result{Changed: true}, nil
		},
	}); err != nil {
		t.Fatalf("registering the fixture method: %v", err)
	}

	sink := &journalSink{}
	runJournaled(t, `{
		"id": "keys",
		"tasks": [{"name": "declared", "fqcn": "`+name+`", "params": {"path": "/etc/hosts", "secret_body": "hunter2"}}]
	}`, mapResolver{}, engine.NewCollectionActionExecutor(engine.NewBuiltinActionExecutor(), engine.NewDeviceRunbookContext), sink)

	entries := sink.flat()
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}
	e := entries[0]

	if e.FQCN != name || e.FQCNUnresolved {
		t.Errorf("recorded FQCN %q (unresolved %v), want the descriptor's own registered name", e.FQCN, e.FQCNUnresolved)
	}
	// "mode" is declared. "diff" and "inverse" are pkg/sdk constants,
	// admitted with no Doc vouching for them, which is what keeps the
	// inverse visible for the 38 of 43 reversible methods whose Doc does
	// not declare it. "headers" is not declared and is counted.
	assertKeys(t, "StatKeys", e.StatKeys, []string{"diff", "inverse", "mode"})
	if e.UndeclaredStatCount != 1 {
		t.Errorf("recorded %d undeclared stats, want 1 for the headers key no Doc declares", e.UndeclaredStatCount)
	}
	assertKeys(t, "ParamKeys", e.ParamKeys, []string{"path"})
	if e.UndeclaredParamCount != 1 {
		t.Errorf("recorded %d undeclared params, want 1 for secret_body", e.UndeclaredParamCount)
	}

	if !e.DiffRecorded {
		t.Error("recorded DiffRecorded false, want the presence of a diff reported")
	}
	if e.InverseFQCN != name || e.InverseFQCNUnresolved {
		t.Errorf("recorded InverseFQCN %q (unresolved %v), want the resolved target", e.InverseFQCN, e.InverseFQCNUnresolved)
	}
	// The inverse's params are read against the RESOLVED TARGET's own Doc,
	// so its declared "path" is named and its undeclared "content", which
	// here is the whole prior file, is counted.
	assertKeys(t, "InverseParamKeys", e.InverseParamKeys, []string{"path"})
	if e.UndeclaredInverseParamCount != 1 {
		t.Errorf("recorded %d undeclared inverse params, want 1 for content", e.UndeclaredInverseParamCount)
	}

	// The guarantee, stated as the test that would have caught a leak: no
	// value any of the above carried appears anywhere in the entry.
	assertNoValueLeaked(t, e, "hunter2", "session=hunter2", "the whole prior file", "the whole new file", "0644", "/etc/hosts")
}

// TestJournalRefusesAnUnregisteredFQCN proves the author's own bytes never
// reach the entry. Nothing checks fqcn: against the registry at compile
// time or at run time, so arbitrary text runs, fails, and arrives at the
// projection on exactly the failure path the journal exists for.
func TestJournalRefusesAnUnregisteredFQCN(t *testing.T) {
	sink := &journalSink{}
	runJournaled(t, `{
		"id": "unknown",
		"tasks": [{"name": "typo", "fqcn": "pkg.apt.instal"}]
	}`, mapResolver{}, engine.NewBuiltinActionExecutor(), sink)

	entries := sink.flat()
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}
	e := entries[0]
	if e.FQCN != engine.FQCNUnregistered || !e.FQCNUnresolved {
		t.Errorf("recorded FQCN %q (unresolved %v), want the sentinel and the flag", e.FQCN, e.FQCNUnresolved)
	}
	assertNoValueLeaked(t, e, "pkg.apt.instal")
	// The record is still useful: NodeID names the graph position and the
	// operator still has the runbook.
	if e.NodeID != "tasks[0]" || e.Outcome != engine.OutcomeFailed {
		t.Errorf("recorded node %q outcome %q, want the failing node named", e.NodeID, e.Outcome)
	}
}

// TestJournalNamesEveryTransportStat proves the static table for the four
// legacy transport FQCNs, which carry no Descriptor and therefore no Doc,
// really matches what transportActionExecutor emits.
//
// It asserts the table against the run's own NodeResult.Stats rather than
// against a copy of the four literals, so the executor growing a fifth
// stat key fails this test instead of silently journaling it as
// undeclared.
func TestJournalNamesEveryTransportStat(t *testing.T) {
	device := newSSHDevice("router1", "10.0.0.1", 22)
	bindings := map[string]engine.TransportBinding{
		"ssh_exec": sshBinding(func(context.Context, transport.Target, credential.Credential, string) (transport.Result, error) {
			return transport.Result{Stdout: "hostname router1", ExitCode: 0}, nil
		}),
	}
	actions := engine.NewTransportActionExecutor(bindings, fakeCredentialStore{}, nil, engine.NewBuiltinActionExecutor())

	sink := &journalSink{}
	result := runJournaled(t, `{
		"id": "transport",
		"tasks": [{"name": "show", "fqcn": "ssh_exec", "params": {"target": "fleet", "command": "show version"}}]
	}`, mapResolver{"fleet": {device}}, actions, sink)

	if result.HasErrors() {
		t.Fatalf("expected a clean run, got %+v", result.Nodes)
	}
	entries := sink.flat()
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}
	e := entries[0]

	if e.FQCN != "ssh_exec" || e.FQCNUnresolved {
		t.Errorf("recorded FQCN %q (unresolved %v), want the engine action table's own name", e.FQCN, e.FQCNUnresolved)
	}
	var want []string
	for key := range result.Nodes[0].Stats {
		want = append(want, key)
	}
	assertKeys(t, "StatKeys", e.StatKeys, want)
	if e.UndeclaredStatCount != 0 {
		t.Errorf("recorded %d undeclared stats, want none: the static table must cover every key this executor emits",
			e.UndeclaredStatCount)
	}
	// The command's own output is what the stat named "stdout" holds, and
	// naming the key must never carry it.
	assertNoValueLeaked(t, e, "hostname router1", "show version")
}

// TestJournalRecordErrorNeverFailsTheRun is the contract's hardest half.
// On the Walk tier an execution error routes into redelivery and re-runs
// the runbook against the device, so a failed audit write must not become
// one: a repeated configuration change is far worse than a missing row.
func TestJournalRecordErrorNeverFailsTheRun(t *testing.T) {
	sink := &journalSink{err: errors.New("the sink is down")}
	result := runJournaled(t, `{
		"id": "sink-down",
		"tasks": [
			{"name": "a", "fqcn": "noop"},
			{"name": "b", "fqcn": "noop"}
		]
	}`, mapResolver{}, engine.NewBuiltinActionExecutor(), sink)

	if result.HasErrors() {
		t.Fatalf("a failing journal sink must not fail the run, got %+v", result.Nodes)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("expected both nodes to run, got %d", len(result.Nodes))
	}
	// And it keeps trying: a failing write does not disable the journal
	// for the rest of the run.
	if len(sink.calls) != 2 {
		t.Errorf("sink received %d calls, want one per level even though every write failed", len(sink.calls))
	}
}

// cancelingActions cancels the run's own context while a level is still
// executing, then delegates to the real builtin executor, reproducing the
// Runner's graceful shutdown exactly: Agent.Run drains rather than
// abandoning, so an in-flight job keeps executing with an already-canceled
// context and reaches its next level barrier with one.
type cancelingActions struct {
	cancel context.CancelFunc
	inner  engine.ActionExecutor
}

// Execute implements engine.ActionExecutor by canceling first and then
// running the task normally, so the task itself still succeeds.
func (c cancelingActions) Execute(ctx context.Context, task *engine.Task, device inventory.InventoryItem) (engine.ActionResult, error) {
	c.cancel()
	return c.inner.Execute(ctx, task, device)
}

// contextCheckingJournal records whether the context each Record call
// received was already canceled.
type contextCheckingJournal struct {
	entries []engine.JournalEntry
	seen    []error
}

// Record implements engine.Journal, keeping the entries and the state of
// the context it was handed.
func (j *contextCheckingJournal) Record(ctx context.Context, entries []engine.JournalEntry) error {
	j.seen = append(j.seen, ctx.Err())
	j.entries = append(j.entries, entries...)
	return nil
}

// TestJournalSurvivesACanceledRunContext is the graceful-shutdown case,
// and it is the one the design note records as the draft getting wrong.
//
// On the Runner the context reaching Executor.Run descends from the
// process context, canceled on SIGINT or SIGTERM, and Agent.Run drains
// rather than abandoning. Run's own ctx.Err check sits at the TOP of the
// next level iteration, which is after the journal write, so a Record
// that inherited the run's context would fail exactly on an ordinary
// rolling restart and discard the level that had just completed.
func TestJournalSurvivesACanceledRunContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sink := &contextCheckingJournal{}
	dag := buildDAG(t, `{
		"id": "midrun",
		"tasks": [
			{"name": "a", "fqcn": "noop"},
			{"name": "b", "fqcn": "noop"}
		]
	}`)
	x := engine.NewExecutor(mapResolver{}, cancelingActions{cancel: cancel, inner: engine.NewBuiltinActionExecutor()},
		lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithJournal(sink))

	if _, err := x.Run(ctx, dag); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want the canceled context reported at the next level boundary", err)
	}

	if len(sink.seen) != 1 {
		t.Fatalf("the sink was called %d times, want once for the level that completed before the cancellation", len(sink.seen))
	}
	if sink.seen[0] != nil {
		t.Errorf("Record received a context reporting %v; it must receive context.WithoutCancel of the run's own, "+
			"or a graceful restart discards every level that already completed", sink.seen[0])
	}
	if len(sink.entries) != 1 || sink.entries[0].NodeID != "tasks[0]" {
		t.Fatalf("the completed level was not journaled, got %+v", sink.entries)
	}
}

// assertKeys compares a recorded key vector against the names expected,
// sorted, so an assertion cannot pass or fail on map iteration order.
func assertKeys(t *testing.T, field string, got, want []string) {
	t.Helper()
	sorted := append([]string(nil), want...)
	slices.Sort(sorted)
	if !slices.Equal(got, sorted) {
		t.Errorf("recorded %s %v, want %v", field, got, sorted)
	}
}

// assertNoValueLeaked fails if any of values appears anywhere in the
// entry's own text, which is the guarantee stated as an assertion: an
// entry can hold no value a device, a credential store, a decrypted
// envelope, or an injector produced.
func assertNoValueLeaked(t *testing.T, e engine.JournalEntry, values ...string) {
	t.Helper()
	rendered := strings.Join(append([]string{
		e.JobID, e.RunID, e.NodeID, e.DAGID, e.DAGVersion, e.FQCN, e.TaskName, e.Register,
		e.DeviceID, string(e.Outcome), string(e.FailureStage), string(e.SkipKind), e.InverseFQCN,
	}, append(append(append([]string(nil), e.StatKeys...), e.ParamKeys...), e.InverseParamKeys...)...), "\x00")

	for _, value := range values {
		if value == "" {
			continue
		}
		if strings.Contains(rendered, value) {
			t.Errorf("the journal entry carries %q; no value a device, a credential store, a decrypted "+
				"envelope, or an injector produced may reach it", value)
		}
	}
}
