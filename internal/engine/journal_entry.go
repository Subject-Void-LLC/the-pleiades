// Package engine: the run journal's projection, the one place in this
// module where a NodeResult becomes a JournalEntry, and the one place
// Executor.Run writes to its Journal sink.
//
// Read JournalEntry's own doc comment (journal.go) first. It states the
// six-kind provenance rule every field here has to satisfy; this file is
// what actually satisfies it, and it is where the two archtests over that
// type (TestJournalEntryHoldsNoValue and
// TestEveryJournalStringFieldIsConstructed) stop being claims about a
// struct and start being claims about a run.
//
// The shape of this file follows one rule: nothing a device, a credential
// store, a decrypted envelope, or an injector produced is copied into an
// entry. Names are resolved through the registry rather than carried
// across, map contents become sorted vectors of key NAMES, and anything
// that cannot be vouched for is counted rather than named.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// journalRecordTimeout bounds how long one level barrier may wait on the
// Journal sink before giving up on that level's write.
//
// A bound has to exist here rather than only inside a sink. Record is
// called from Run's own goroutine, between a level finishing and the next
// one starting, so a sink that hangs does not merely lose an audit row:
// it stops the run. The Journal port's contract says the caller detaches
// the context, and a detached context with no deadline at all is exactly
// the shape that hangs forever, since the run's own cancellation can no
// longer end it.
//
// Five seconds is generous for both sinks that will exist: the Crawl-tier
// one appends to a local file, and the Walk-tier one publishes a single
// NATS message. A sink wanting less may apply its own shorter deadline on
// top; it cannot lengthen this one, which is the direction that matters.
const journalRecordTimeout = 5 * time.Second

// transportExecStatKeys are the four stat keys every legacy transport
// action emits (transportActionExecutor.Execute, action_ssh.go, the
// ActionResult it returns).
//
// They need a static table because those four FQCNs are not Collection
// methods: they live in engine.NewDefaultTransportBindings, carry no
// collection.Descriptor and therefore no Doc at all, so the registry
// whitelist admits nothing for them. Without this table every ssh_exec
// node, which is the Crawl tier's most common real action, would journal
// zero named stats and four undeclared ones, which is a true record of
// nothing.
//
// It is a duplicate of the literals in action_ssh.go, and that is the
// cost of those four actions having no manifest.
// TestJournalNamesEveryTransportStat pins the copy against what that
// executor really emits, through a real run, so a fifth stat key fails a
// test instead of quietly becoming an undeclared count.
var transportExecStatKeys = []string{"exit_code", "exit_status_unknown", "stderr", "stdout"}

// engineActionStatKeys is every FQCN the engine dispatches itself rather
// than through the Collection registry, mapped to the stat keys that
// action declares.
//
// It is both halves of build-order steps 9 and 10 for the non-Collection
// actions: membership in this table is what lets resolveFQCN store a name
// collection.Lookup has never heard of, and the value is what admitKeys
// intersects that action's stats against.
//
// The three builtins map to no keys on purpose, and the reason is worth
// stating because it looks like an omission. builtinActionExecutor's noop
// echoes the task's own params verbatim into Stats (action.go), and
// set_metadata reports params["data"] verbatim, so their top-level stat
// keys are author-written text, not a documented contract. Admitting them
// would put runbook author bytes into a field the provenance rule calls a
// registry constant. Counting them, which is what an empty declared set
// produces, is the correct answer rather than a gap to fill later.
//
// "ios_backup" is deliberately absent, and it is the one name a reader
// comparing this table against ActionCapability (action_capability.go)
// will miss. That table has five entries and this one has four
// transports, because ios_backup is validated and not executable: it has
// no TransportBinding, no builtin switch case, and nothing else can run
// it, so a runbook naming it reaches the builtin executor's default and
// fails. CheckActionCapabilityBindings already records that reverse gap
// as deliberate. Its journal entry therefore reads FQCNUnregistered,
// which is the accurate record of a name nothing in this process can
// dispatch.
var engineActionStatKeys = map[string][]string{
	"noop":                          nil,
	"set_metadata":                  nil,
	"pleiades.builtin.set_metadata": nil,
	"ssh_exec":                      transportExecStatKeys,
	"serial_exec":                   transportExecStatKeys,
	"serialtcp_exec":                transportExecStatKeys,
	"telnet_exec":                   transportExecStatKeys,
}

// The two keys sdk.RecordInverse writes its record under
// (pkg/sdk/inverse.go, the map literal it hands to SetStat).
//
// They are re-declared here rather than imported because pkg/sdk exports
// no constant for either: RecordInverse builds its map with bare string
// literals, so sdk.StatInverse names the stat but nothing names the two
// keys inside it. That is a real gap in pkg/sdk and this is the second
// copy of those literals in the module. Until pkg/sdk exports them, a
// reader renaming one has to find this file too.
const (
	inverseRecordFQCN   = "fqcn"
	inverseRecordParams = "params"
)

// resolvedFQCN is what resolveFQCN made of one FQCN string, and it is the
// only shape allowed to reach JournalEntry.FQCN or InverseFQCN.
//
// The two key sets travel with the name because they are only meaningful
// together: "which keys may be named" is a property of the method the
// name resolved TO, never of the name a caller wrote.
type resolvedFQCN struct {
	// Name is what the entry stores: a registered descriptor's own name,
	// an engine action's own table key, or the FQCNUnregistered constant.
	// It is never the caller's bytes.
	Name string

	// Unresolved reports that nothing matched and Name is the sentinel.
	Unresolved bool

	// engineAction reports that this name resolved through
	// engineActionStatKeys rather than through the Collection registry,
	// so it is one of the seven FQCNs the engine dispatches itself.
	//
	// It gates the two sdk stat keys. Only a Collection method can call
	// sdk.RecordInverse or sdk.RecordDiff, because only a Collection
	// method is handed a RunbookContext to call them on. An engine action
	// therefore cannot legitimately produce either key, and one appearing
	// in its stats came from somewhere else: builtinActionExecutor sets a
	// noop's stats to task.Params verbatim (action.go), so a runbook
	// author writing an "inverse" param would otherwise have that
	// projected as a real recorded undo, complete with a resolved
	// InverseFQCN. A rollback engine reading InverseFQCN is the eventual
	// consumer, so a forged one is a correctness problem and not merely
	// an untidy record.
	engineAction bool

	// statKeys and paramKeys are the names this method's own Doc declares.
	// A nil set admits nothing, which is what a method with no Doc gets.
	//
	// paramKeys is always nil for an engine action: engineActionStatKeys
	// declares stat names only, so every param of an ssh_exec or a noop
	// node is counted as undeclared and none is ever named. That is
	// conservative in the safe direction and is what the design's step 10
	// asks for, but it is recorded here because the count is otherwise a
	// puzzle for whoever first reads an entry for one of those nodes.
	statKeys  map[string]bool
	paramKeys map[string]bool
}

// resolveFQCN turns an FQCN string of unknown provenance into a name the
// journal may store.
//
// This is build-order step 9, and it exists because neither source of an
// FQCN is trustworthy. Task.FQCN is author-written YAML: validateTask
// (tasktree.go) checks only that exactly one of fqcn, block and parallel
// is set, nothing anywhere checks it against the registry at compile
// time, and at run time an unknown name is not refused either, since
// collectionActionExecutor delegates on a Lookup miss, the transport
// executor delegates again on a bindings miss, and only the builtin
// executor's default finally errors. So arbitrary text runs, fails, and
// arrives here on exactly the failure path the journal exists for.
// sdk.Inverse.FQCN is worse: it is not author text but whatever a
// Collection method put in a map at run time, and sdk.RecordInverse
// validates only that it is non-empty.
//
// The order is registry first, engine actions second, sentinel last:
//
//  1. collection.Lookup hit: store the DESCRIPTOR'S own Name, not the
//     argument. The two are equal today; storing the descriptor's is what
//     keeps them equal if a registry ever normalizes a name.
//  2. engineActionStatKeys hit: store name. A map hit proves the argument
//     is byte-identical to this file's own compile-time constant, so what
//     is stored is that constant and merely happens to have been spelled
//     by the caller as well.
//  3. Neither: store FQCNUnregistered and set Unresolved. The unresolved
//     string is never stored. Dropping it costs little, because NodeID
//     still names the graph position and the operator still has the
//     runbook.
func resolveFQCN(name string) resolvedFQCN {
	if desc, ok := collection.Lookup(name); ok {
		return resolvedFQCN{
			Name:      desc.Name,
			statKeys:  returnFieldNames(desc.Manifest.Doc.Returns),
			paramKeys: paramNames(desc.Manifest.Doc.Params),
		}
	}
	if statKeys, ok := engineActionStatKeys[name]; ok {
		return resolvedFQCN{Name: name, engineAction: true, statKeys: nameSet(statKeys)}
	}
	return resolvedFQCN{Name: FQCNUnregistered, Unresolved: true}
}

// paramNames is the set of names a method's Doc.Params declares.
func paramNames(params []collection.Param) map[string]bool {
	set := make(map[string]bool, len(params))
	for _, p := range params {
		set[p.Name] = true
	}
	return set
}

// returnFieldNames is the set of names a method's Doc.Returns declares.
func returnFieldNames(fields []collection.ReturnField) map[string]bool {
	set := make(map[string]bool, len(fields))
	for _, f := range fields {
		set[f.Name] = true
	}
	return set
}

// nameSet turns a static key list into the same set shape a Doc produces.
func nameSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// admitKeys returns the TOP-LEVEL keys of values that declared admits,
// sorted, plus a count of every key it refused.
//
// This is build-order step 10, and three properties of it are load
// bearing.
//
// It never recurses. One level down a key can be device text:
// internal/catalog/http/request.go builds its "headers" stat by lower
// casing the names of the response headers the device itself sent
// (request.go's own response handling, keyed by resp.Header), so a
// "set-cookie" key under it was written by the far side, not by a
// method's Doc. Top level is safe because a method chose those names in
// its own source; one level down is not, and no amount of intersecting
// makes it so, because the nested key is itself part of the value.
//
// A refused key is counted, never named. Doc is a documentation-generator
// struct that is partial by construction (pkg/collection/doc.go says so
// itself, and Doc.Fragments names params that live in a registry
// internal/engine cannot reach), so an undocumented key is as likely to
// be a documentation gap as a surprise. Counting keeps the gap visible
// instead of dropping it silently, which is the shape LESSONS_LEARNED #71
// exists to forbid.
//
// alwaysAdmitted is how sdk.StatInverse and sdk.StatDiff get in without a
// Doc vouching for them. See admitStatKeys for why that is not a
// loophole.
//
// The sort is not cosmetic. Go randomizes map iteration, so an unsorted
// vector would differ between two writes of the same entry, and a record
// that differs run to run for the same run is not a record.
func admitKeys(values map[string]interface{}, declared map[string]bool, alwaysAdmitted ...string) ([]string, int) {
	var kept []string
	undeclared := 0
	for key := range values {
		if declared[key] || slices.Contains(alwaysAdmitted, key) {
			kept = append(kept, key)
			continue
		}
		undeclared++
	}
	slices.Sort(kept)
	return kept, undeclared
}

// admitStatKeys applies admitKeys to a node's stats, admitting the two
// pkg/sdk stat keys unconditionally.
//
// The unconditional admission is measured, not assumed. 38 of the 43
// reversible implemented methods in the catalog do not declare "inverse"
// in their own Doc.Returns (counted from
// docs/reference/schemas/module-catalog.json, the generated file that is
// authoritative over any hand-written tally), so a pure Doc whitelist
// would silently drop the inverse key for nearly every method that emits
// one, and drop it from the exact record a rollback would later be
// planned from. Three implemented methods declare no Returns at all.
//
// It is not a loophole in the provenance rule, because both names are
// pkg/sdk package constants (sdk.StatInverse, sdk.StatDiff), which is
// kind 2 by provenance and does not depend on any method's documentation
// being complete. What is admitted is the two NAMES. Neither value is
// stored: the inverse contributes a resolved FQCN and a vector of its
// param key names, and the diff contributes one bool.
func admitStatKeys(stats map[string]interface{}, r resolvedFQCN) ([]string, int) {
	if r.engineAction {
		// No unconditional sdk keys here. See resolvedFQCN.engineAction:
		// only a Collection method can call sdk.RecordInverse, so an
		// "inverse" key in an engine action's stats is a runbook author's
		// own param echoed back by the noop builtin, not a recorded undo.
		// It is counted as undeclared like any other unrecognized key,
		// which is the honest record: something was there, and this
		// platform will not vouch for what.
		return admitKeys(stats, r.statKeys)
	}
	return admitKeys(stats, r.statKeys, sdk.StatInverse, sdk.StatDiff)
}

// skipKindFor maps a compiled condition's own keyword onto the journal's
// SkipKind, and returns SkipKindNone for anything it does not recognize.
//
// The three cases are ConditionProgram's own keyword literals, set at
// compileItems' only three call sites (conditional.go: "when", "when_or"
// and "when_cel"), so this copies a keyword across rather than inventing
// a second vocabulary that could drift from it.
//
// SkipKindNone for a fourth keyword is deliberate and is the fail-closed
// half: projectResult refuses to write an entry for a skip carrying no
// kind, so a new condition keyword surfaces as a loud, counted refusal
// rather than as a skip silently mislabeled "when".
func skipKindFor(cp *ConditionProgram) SkipKind {
	switch cp.keyword {
	case "when":
		return SkipKindWhen
	case "when_or":
		return SkipKindWhenOr
	case "when_cel":
		return SkipKindWhenCEL
	default:
		return SkipKindNone
	}
}

// validFailureStage reports whether stage is one of the nine stages a
// failed node may record.
//
// Every one is listed rather than range-checked, because the point is
// that adding a tenth constant is not enough: a stage this function has
// never heard of is refused, and the entry that would have carried it is
// not written. See projectLevel on what that costs whoever adds one.
func validFailureStage(stage FailureStage) bool {
	switch stage {
	case FailureStageWorkflowRead,
		FailureStageConditionEval,
		FailureStageSecretMask,
		FailureStageResolveTarget,
		FailureStageLockAll,
		FailureStageLockDevice,
		FailureStageAction,
		FailureStageRegisterMask,
		FailureStageRecord:
		return true
	case FailureStageNone:
		// The zero value. A failed result carrying it is a failure site
		// that never tagged itself, which is exactly the silent hole this
		// check exists to refuse.
		return false
	default:
		return false
	}
}

// validSkipKind reports whether kind is one of the four reasons a node
// may be recorded as skipped. It enumerates them for the same reason
// validFailureStage does.
func validSkipKind(kind SkipKind) bool {
	switch kind {
	case SkipKindWhen, SkipKindWhenOr, SkipKindWhenCEL, SkipKindLifecycle:
		return true
	case SkipKindNone:
		return false
	default:
		return false
	}
}

// projectResult turns one NodeResult into one JournalEntry, or reports
// why it could not.
//
// It reads what NodeResult does not carry from r.dag.Nodes[n.NodeID]: the
// task's author-written name and register, and the raw fqcn: string this
// function is about to refuse to trust.
//
// Every branch that can produce an entry is enumerated here. An error
// return means the result did not fit any of them, and the caller writes
// no row for it rather than writing one with a zero-valued enum. That
// choice is the whole of "fail closed" in an audit projection: a record
// nobody can vouch for is worse than a missing one that was logged and
// counted, because the first is read as fact.
func (r *run) projectResult(n NodeResult) (JournalEntry, error) {
	task := r.dag.Nodes[n.NodeID]
	if task == nil {
		// Unreachable through runNode, which looks the task up by this
		// exact id before doing anything else. It is checked because the
		// alternative to checking is a nil dereference inside the audit
		// path of a run that was otherwise fine.
		return JournalEntry{}, fmt.Errorf("node %q produced a result but names no task in the compiled DAG", n.NodeID)
	}

	// JobID and Attempt are deliberately absent. They belong to a Walk-tier
	// dispatch, which this package knows nothing about, and the Walk sink
	// stamps both at construction from the wire.DispatchPayload it was
	// built for (see JournalEntry.JobID). A value invented here would be a
	// guess about someone else's identifier.
	entry := JournalEntry{
		RunID:      r.runID,
		NodeID:     n.NodeID,
		DAGID:      r.dag.ID,
		DAGVersion: r.dag.Version,
		TaskName:   task.Name,
		Register:   task.Register,
		DeviceID:   n.Device,
		StartedAt:  n.StartedAt,
		FinishedAt: n.FinishedAt,
	}

	// The synthetic parallel fan-out/join marker is settled first, because
	// it is the one node that reaches this function having executed
	// nothing: runNode short-circuits it above the clock read, so it has
	// no bounds, no stats, no params and no method. Its FQCN stays the
	// empty string rather than FQCNUnregistered, and that distinction is
	// the point: FQCNUnregistered says "a name was written and dropped",
	// which would be a lie about a node that never named a method at all.
	// OutcomeNotReached is what a reader keys off instead.
	if task.Kind() == TaskKindSynthetic {
		entry.Outcome = OutcomeNotReached
		return entry, nil
	}

	resolved := resolveFQCN(task.FQCN)
	entry.FQCN = resolved.Name
	entry.FQCNUnresolved = resolved.Unresolved

	// Param key names only, against this method's own Doc.Params. No
	// declaration can ever admit a param VALUE: file.copy's content is a
	// required param and is the file body.
	//
	// One consequence is worth knowing rather than discovering: a task
	// naming a target contributes one undeclared param, because "target"
	// is the engine's own key (TaskTarget, action.go) and no method's Doc
	// declares it. Admitting it would be safe on provenance grounds and it
	// is still not done here, because the rule this file implements is
	// "the executing method's own Doc.Params" and widening it is a design
	// decision, not a convenience.
	entry.ParamKeys, entry.UndeclaredParamCount = admitKeys(task.Params, resolved.paramKeys)
	// journalStats, not Stats. The exported field is nil on the two
	// post-action failure paths by design (see its doc comment), and those
	// are the paths a rollback most needs a record of.
	entry.StatKeys, entry.UndeclaredStatCount = admitStatKeys(n.journalStats, resolved)
	if !resolved.engineAction {
		// Skipped for the seven engine-dispatched FQCNs, for the reason
		// on resolvedFQCN.engineAction: projecting here would let a
		// runbook forge an InverseFQCN a rollback engine would later
		// trust.
		projectInverse(&entry, n.journalStats)
	}

	switch {
	case n.Err != nil:
		if !validFailureStage(n.failureStage) {
			return JournalEntry{}, fmt.Errorf(
				"node %q (device %q) failed at stage %q, which is not one of the nine this journal records",
				n.NodeID, n.Device, n.failureStage)
		}
		entry.Outcome = OutcomeFailed
		entry.FailureStage = n.failureStage

	case n.Skipped:
		if !validSkipKind(n.skipKind) {
			return JournalEntry{}, fmt.Errorf(
				"node %q (device %q) was skipped for reason kind %q, which is not one of the four this journal records",
				n.NodeID, n.Device, n.skipKind)
		}
		entry.Outcome = OutcomeSkipped
		entry.SkipKind = n.skipKind
		entry.SkipOrdinal = n.skipOrdinal
		entry.SkipTotal = n.skipTotal

	case n.Changed:
		entry.Outcome = OutcomeChanged

	default:
		// The action ran and reported no change. This is the only branch
		// reached by elimination rather than by a positive signal, and it
		// is safe to leave that way because NodeResult has exactly one
		// remaining shape: a completed runOne whose ActionResult reported
		// Changed false.
		entry.Outcome = OutcomeRan
	}

	return entry, nil
}

// projectInverse fills in entry's inverse fields from the sdk.StatInverse
// record a method may have emitted, and sets DiffRecorded from the
// presence of sdk.StatDiff.
//
// An absent inverse leaves InverseFQCN empty with the flag clear, which
// is the common and correct case: a method that found the device already
// converged must not emit one, and that absence means "undoing this task
// means doing nothing" rather than "a name was dropped".
//
// The inverse's own FQCN goes through the identical resolution the task's
// does, and needs it more: sdk.RecordInverse checks only that the string
// is non-empty, so a method emitting a typo, or a name assembled at run
// time from something it read off a device, reaches here unchecked. A
// malformed record (no fqcn key, or one that is not a string) resolves
// the empty string, which no registry and no engine table holds, so it
// lands on FQCNUnregistered with the flag set. That is the honest record
// of a method that emitted an inverse naming nothing.
//
// DiffRecorded is presence only, never the diff. A diff is a before and
// after pair of exactly the device content JournalEntry refuses to hold.
func projectInverse(entry *JournalEntry, stats map[string]interface{}) {
	if _, ok := stats[sdk.StatDiff]; ok {
		entry.DiffRecorded = true
	}

	record, ok := stats[sdk.StatInverse].(map[string]interface{})
	if !ok {
		// Either no inverse at all, or one whose value is not the map
		// RecordInverse writes. Both are "no inverse to record" here: this
		// function reports what a method emitted, and it is not the place
		// to raise an alarm about a shape the SDK's own writer cannot
		// produce.
		return
	}

	name, _ := record[inverseRecordFQCN].(string)
	inverse := resolveFQCN(name)
	entry.InverseFQCN = inverse.Name
	entry.InverseFQCNUnresolved = inverse.Unresolved

	// Key names of the RESOLVED target's own Doc.Params, not of the task
	// that emitted the record. The inverse is a different method's call.
	params, _ := record[inverseRecordParams].(map[string]interface{})
	entry.InverseParamKeys, entry.UndeclaredInverseParamCount = admitKeys(params, inverse.paramKeys)
}

// projectLevel turns one topological level's results into journal
// entries, numbering each one, and returns whatever it could not
// classify.
//
// It is called from Run's level loop immediately after runConcurrently
// returns and before the failure scan, and that position is the design.
// There are nine places in executor.go that construct a NodeResult, and
// none of them constructs a JournalEntry: every result flows into outs,
// runConcurrently's wg.Wait has already joined the whole level, and one
// call here covers all nine by construction.
//
// # What a tenth construction site costs whoever adds it
//
// Nothing, if it is an ordinary success or an ordinary skip: it reaches
// this function like the other nine and projects correctly with no edit
// here. A tenth FAILURE site is different, and deliberately so. A result
// with a non-nil Err and no stage tag is refused by projectResult, so it
// produces no journal row, one error in the joined result, and a logged,
// counted complaint naming the node. It does not produce a row saying
// "failed, stage unknown", because a stage of "" would be indexed,
// queried and read as a fact about the run.
//
// The fix for whoever hits that is two lines: add the constant to
// FailureStage (journal.go) and to validFailureStage above, then tag the
// new site with it. The refusal is loud precisely so that cost lands on
// the person adding the site rather than on the operator reading the
// journal months later.
//
// Sequence numbering is dense over the entries actually written, so a
// refused result leaves no gap. A gap would read as a lost row, and the
// record of a refusal is the log line and the counter, not a hole.
func (r *run) projectLevel(outs [][]NodeResult) ([]JournalEntry, error) {
	var entries []JournalEntry
	var problems []error

	for _, out := range outs {
		for _, n := range out {
			entry, err := r.projectResult(n)
			if err != nil {
				problems = append(problems, err)
				continue
			}
			r.sequence++
			entry.Sequence = r.sequence
			entries = append(entries, entry)
		}
	}

	return entries, errors.Join(problems...)
}

// recordLevel projects one level's results and writes them to the
// Journal sink. It returns nothing: a journal write can never change a
// run's outcome.
//
// # The context is detached, and that is not a detail
//
// Record receives context.WithoutCancel(ctx) plus journalRecordTimeout,
// never ctx. On the Runner the context reaching Executor.Run descends
// from the process context (cmd/runner/main.go builds it with
// context.WithCancel(context.Background()) and cancels it on SIGINT or
// SIGTERM) by way of executeWithLease's execCtx, which propagates that
// cancellation for an interruptible payload and is canceled
// unconditionally by its own deferred cancelExec when the dispatch ends
// (internal/runner/agent_exec.go). Agent.Run then drains rather than
// abandoning, waiting for every worker to finish its current message
// (internal/runner/agent_run.go), so an in-flight job keeps executing
// with an already-canceled context. Run's own ctx.Err check sits at the
// TOP of the next level iteration, which is after this write. A Record
// that inherited ctx would therefore fail exactly when the record matters
// most: an ordinary rolling restart would discard the journal of every
// level that had already completed, and the redelivery that follows would
// then corrupt the record rather than repair it. WithoutCancel keeps the
// context's values, so an active OpenTelemetry span still crosses the
// boundary and only the cancellation is dropped.
//
// # A Record error never fails the run
//
// This is not politeness about an observability side effect. On the Walk
// tier an execution error routes into event.HandleDeliveryFailure, which
// Naks for redelivery below maxDeliver (internal/runner/agent_handle.go),
// so the runbook runs AGAIN against the same device. A failed audit write
// turning into a repeated configuration change is far worse than a
// missing audit row.
//
// It is not swallowed either. The failure is logged at error and counted
// on r.journalFailures, and the running total goes into the log line, so
// a sink failing every write cannot look like a run that journaled
// nothing. The log names the run, the DAG, the level and the devices;
// it cannot name the job, because JobID is the Walk sink's to stamp and
// this package never sees one. RunID is what joins this line to the rows.
//
// slog.Default is used rather than a logger option on Executor. Both
// Walk-tier composition roots call slog.SetDefault before anything runs
// (cmd/controller/main.go, cmd/runner/main.go), so this reaches the
// configured, masking handler; adding a WithLogger option to Executor is
// a separate decision this phase does not need to make.
func (r *run) recordLevel(ctx context.Context, levelIndex int, outs [][]NodeResult) {
	entries, projectErr := r.projectLevel(outs)

	if projectErr != nil {
		slog.Default().Error("run journal refused to record a node result it could not classify",
			slog.String("run_id", r.runID),
			slog.String("dag", r.dag.ID),
			slog.Int("level", levelIndex),
			slog.String("error", projectErr.Error()))
	}

	// A level always produces at least one result, so an empty slice here
	// means every one of them was refused above and has already been
	// reported. Calling a sink with nothing to write would only add a
	// second, quieter symptom of the same thing.
	if len(entries) == 0 {
		return
	}

	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), journalRecordTimeout)
	defer cancel()

	if err := r.x.journal.Record(recordCtx, entries); err != nil {
		r.journalFailures++
		slog.Default().Error("failed to write a level of the run journal",
			slog.String("run_id", r.runID),
			slog.String("dag", r.dag.ID),
			slog.Int("level", levelIndex),
			slog.Int("entries", len(entries)),
			slog.String("devices", journalDeviceList(entries)),
			slog.Int("failed_writes", r.journalFailures),
			slog.String("error", err.Error()))
	}
}

// journalDeviceList names the distinct devices a level's entries covered,
// sorted, for the failure log line. The empty device (a controller-side
// task) is reported as "<none>" rather than as an empty element, so a
// reader can tell it apart from a formatting accident.
func journalDeviceList(entries []JournalEntry) string {
	seen := make(map[string]bool, len(entries))
	var devices []string
	for _, e := range entries {
		id := e.DeviceID
		if id == "" {
			id = "<none>"
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		devices = append(devices, id)
	}
	slices.Sort(devices)
	return strings.Join(devices, ",")
}
