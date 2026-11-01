// The run report: what `pleiades run`, `pleiades adhoc` and `pleiades
// rollback` print with --json, and what their text views render.
package clireport

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// RunReportVersion is RunReport's major version.
const RunReportVersion = 1

// TaskStatus is what one task did on one device.
type TaskStatus string

// The statuses a task reports, one per line the text view prints.
const (
	// StatusOK ran and changed nothing.
	StatusOK TaskStatus = "ok"
	// StatusChanged ran and changed something.
	StatusChanged TaskStatus = "changed"
	// StatusWouldChange is a check that found something to change.
	StatusWouldChange TaskStatus = "would_change"
	// StatusCheckedOnly is a check_mode task in a real run: asked, not run.
	StatusCheckedOnly TaskStatus = "checked_only"
	// StatusFailed ended with an error.
	StatusFailed TaskStatus = "failed"
	// StatusHeld ended holding its result for a person's decision
	// (sdk.HeldError): it failed the run closed, and is told apart from a
	// failure because running again after the decision continues it.
	// Phase 27 turns it into a pause (internal/engine's pause.go).
	StatusHeld TaskStatus = "held"
	// StatusSkipped did not run, for the reason given.
	StatusSkipped TaskStatus = "skipped"
	// StatusUnchecked could not say what it would change.
	StatusUnchecked TaskStatus = "unchecked"
	// StatusConditionalSkip is a rescue task in a check: it runs only if its
	// block fails, which a check does not predict, so it is neither checked
	// nor a gap in the check.
	StatusConditionalSkip TaskStatus = "conditional_skip"
	// StatusNotRun is a planned task the run never reached: validation
	// refused the runbook, an earlier failure ended the run, or it was
	// aborted. It has no device, since nothing resolved its target, and
	// its reason says which. It differs from skipped, whose task was
	// reached and passed over by its own condition.
	StatusNotRun TaskStatus = "not_run"
)

// KnownTaskStatuses are every TaskStatus this producer writes.
var KnownTaskStatuses = []string{
	string(StatusOK), string(StatusChanged), string(StatusWouldChange), string(StatusCheckedOnly), string(StatusFailed),
	string(StatusHeld), string(StatusSkipped), string(StatusUnchecked), string(StatusConditionalSkip), string(StatusNotRun),
}

// KnownOutcomeStatuses are every RunOutcome.Status this producer writes.
var KnownOutcomeStatuses = []string{"complete", "rescued", "failed", "incomplete", "invalid", "error"}

// The kinds of RunWarning.
const (
	// WarnConcurrentRun is another live run changing some of the same
	// devices when it started, or still running when it ended.
	WarnConcurrentRun = "concurrent_run"
	// WarnConcurrentChange is another run's journaled change to some of
	// this run's devices while this run worked.
	WarnConcurrentChange = "concurrent_change"
	// WarnConcurrentUnknown is a part of that question this run could not
	// answer: a record it could not write, or a journal it could not read.
	WarnConcurrentUnknown = "concurrent_unknown"
	// WarnSerialStopped is a serial: batch whose failure stopped the
	// rollout: Devices names every device of the batches that did not run.
	WarnSerialStopped = "serial_stopped"
)

// KnownWarningKinds are every RunWarning.Kind this producer writes.
var KnownWarningKinds = []string{WarnConcurrentRun, WarnConcurrentChange, WarnConcurrentUnknown, WarnSerialStopped}

// RunReport is one run, as both views print it.
type RunReport struct {
	// SchemaVersion is this report's major version, RunReportVersion.
	SchemaVersion int `json:"schema_version"`
	// Runbook names what ran: the runbook's path, or the ad-hoc call.
	Runbook string `json:"runbook"`
	// Mode is execute or check, after check_mode in the runbook is applied.
	Mode collection.Mode `json:"mode"`
	// Nodes is how many tasks the runbook compiled to.
	Nodes int `json:"nodes"`
	// InventoryHosts is how many devices the inventory holds.
	InventoryHosts int `json:"inventory_hosts"`
	// ServiceEffecting is the runbook's own declaration.
	ServiceEffecting bool `json:"service_effecting"`
	// BlastRadius is how many devices the run targets.
	BlastRadius BlastRadius `json:"blast_radius"`
	// Selection is the --tags and --skip-tags that chose the tasks.
	Selection string `json:"selection,omitempty"`
	// Plan is the authored task tree, section by section.
	Plan []PlanSection `json:"plan,omitempty"`
	// Validation is what stopped the run before it started.
	Validation []ValidationIssue `json:"validation,omitempty"`
	// Tasks is each task's result on each device, in the executor's order.
	Tasks []TaskReport `json:"tasks,omitempty"`
	// Metadata is what set_metadata tasks recorded, masked.
	Metadata map[string]any `json:"metadata,omitempty"`
	// RunID names this run in its journal, and is what `pleiades
	// rollback` and `pleiades journal show` take. Empty for a check, which
	// writes no journal, and for a run that never started.
	RunID string `json:"run_id,omitempty"`
	// Journal is the path of this run's journal file, empty when RunID is.
	Journal string `json:"journal,omitempty"`
	// Warnings are about the run as a whole: another run changing the same
	// devices when it started (concurrent_run), or a change another run
	// journaled to them while it worked (concurrent_change). A check has
	// none, since it changes nothing.
	Warnings []RunWarning `json:"warnings,omitempty"`
	// Rollback is, for `pleiades rollback`, what it planned: the run it
	// undoes, what it leaves in place, what nobody can say changed, and,
	// when it refused, why.
	Rollback *RollbackReport `json:"rollback,omitempty"`
	// Outcome is how the run ended.
	Outcome RunOutcome `json:"outcome"`
}

// MarshalJSON writes the report stamped with RunReportVersion, whatever
// SchemaVersion holds: a report is built in several places (a run, a refused
// call, a rollback), and none of them can then print one without its
// version.
func (r RunReport) MarshalJSON() ([]byte, error) {
	// plain has RunReport's fields and none of its methods, so encoding it
	// does not call this method again.
	type plain RunReport
	r.SchemaVersion = RunReportVersion
	// HTML characters stay as they are, as the command's own encoder
	// writes them, so ">=1.0.0" still reads as itself.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(plain(r)); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// BlastRadius is validate.BlastRadius as the report writes it.
type BlastRadius struct {
	// Devices is the number of distinct devices any task targets.
	Devices int `json:"devices"`
	// Tiers are the distinct tier properties among them.
	Tiers []string `json:"tiers,omitempty"`
}

// PlanSection is one of pretasks, tasks and posttasks.
type PlanSection struct {
	// Section is "pretasks", "tasks" or "posttasks".
	Section string `json:"section"`
	// Tasks are the section's tasks, in order.
	Tasks []PlanTask `json:"tasks"`
}

// PlanTask is one authored task, with the groups nested under it.
type PlanTask struct {
	// ID is the task's node id (tasks[0], tasks[1].block[0]).
	ID string `json:"id"`
	// Name is the task's name, which may be empty.
	Name string `json:"name,omitempty"`
	// Method is the method or action it calls; empty for a group.
	Method string `json:"method,omitempty"`
	// Kind is "block" or "parallel" for a group, empty otherwise.
	Kind string `json:"kind,omitempty"`
	// Selected is false for a task --tags or --skip-tags left out.
	Selected bool `json:"selected"`
	// Block is a block's own tasks.
	Block []PlanTask `json:"block,omitempty"`
	// Rescue is a block's rescue tasks, run when one of Block fails.
	Rescue []PlanTask `json:"rescue,omitempty"`
	// Always is a block's always tasks, run however Block ended.
	Always []PlanTask `json:"always,omitempty"`
	// Parallel is a parallel group's tasks, run side by side.
	Parallel []PlanTask `json:"parallel,omitempty"`
}

// ValidationIssue is one validate.Finding.
type ValidationIssue struct {
	// Rule names the rule that refused the runbook.
	Rule string `json:"rule"`
	// Node is the task it refused.
	Node string `json:"node"`
	// Device is the inventory id of the device it concerns, if one.
	Device string `json:"device,omitempty"`
	// Host is that device's inventory name.
	Host string `json:"host,omitempty"`
	// Message says why.
	Message string `json:"message"`
}

// TaskReport is one task's result on one device. Every text in it that
// came from a device or a method is masked.
type TaskReport struct {
	// ID is the task's node id.
	ID string `json:"id"`
	// Name is the task's name, which may be empty.
	Name string `json:"name,omitempty"`
	// Method is the method or action it called.
	Method string `json:"method,omitempty"`
	// Device is the device's inventory id (a UUID), empty for a
	// controller-side task and for a task that never ran. It stays the id
	// by contract, since a name can change and be reused; Host is the name.
	Device string `json:"device,omitempty"`
	// Host is the device's inventory name, the one a person reads.
	Host string `json:"host,omitempty"`
	// Status is what the task did.
	Status TaskStatus `json:"status"`
	// Error is a failed task's error.
	Error string `json:"error,omitempty"`
	// RescuedBy names the first task of the rescue: that answered this
	// failed task's failure; the run went on past it.
	RescuedBy string `json:"rescued_by,omitempty"`
	// Undoes names the task this one undid: a step of a block's
	// rollback_on_failure, which undid what the block's tasks completed
	// when one of them failed. Its id is that task's, with .undo[step].
	Undoes string `json:"undoes,omitempty"`
	// Unreached marks a failed task whose device never answered: its
	// connection failed or timed out before anything was sent, so nothing
	// ran and running it again is safe. A refused login is not unreached.
	Unreached bool `json:"unreached,omitempty"`
	// Label is the loop_control label of the item this result is, when the
	// task is a loop and its author wrote one. The item's own value is never
	// reported, since it may be a secret.
	Label string `json:"label,omitempty"`
	// Ignored marks a failure of a task that sets ignore_errors: it is a
	// failure here and in the journal, and the run went on past it.
	Ignored bool `json:"ignored,omitempty"`
	// Retried marks a failure the task tried again; only its last try fails
	// the task.
	Retried bool `json:"retried,omitempty"`
	// Reason is why a task was skipped, could not be checked, or was
	// never run.
	Reason string `json:"reason,omitempty"`
	// AllowedUnchecked marks an unchecked task --allow-unchecked accepts.
	AllowedUnchecked bool `json:"allowed_unchecked,omitempty"`
	// Warnings are what the person running the work has to act on.
	Warnings []string `json:"warnings,omitempty"`
	// Provider names the external program that ran the method, if one did.
	Provider *collection.Provider `json:"provider,omitempty"`
	// Stats is everything the task reported, masked.
	Stats map[string]any `json:"stats,omitempty"`
	// StartedAt is when the task's own execution began.
	StartedAt *time.Time `json:"started_at,omitempty"`
	// FinishedAt is when it ended.
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// RunOutcome is how a run ended, with the status the command exits with.
type RunOutcome struct {
	// Status is complete, rescued, failed, incomplete, invalid or error.
	Status string `json:"status"`
	// Message is the line the text view ends with.
	Message string `json:"message"`
	// ExitCode is the command's exit status.
	ExitCode int `json:"exit_code"`
	// Unchecked counts tasks a check did not cover and nothing allowed.
	Unchecked int `json:"unchecked,omitempty"`
	// AllowedUnchecked names the unchecked tasks --allow-unchecked allowed,
	// each as its node id and its device's name ("tasks[3] [r1]").
	AllowedUnchecked []string `json:"allowed_unchecked,omitempty"`
}

// RunWarning is something about the run as a whole that the person running
// it has to know, shown in the text view and kept in --json.
type RunWarning struct {
	// Kind is concurrent_run, concurrent_change, concurrent_unknown or
	// serial_stopped.
	Kind string `json:"kind"`
	// When is "start" for what the run found as it began, "end" for what
	// it found as it ended.
	When string `json:"when"`
	// Message is the line the text view prints.
	Message string `json:"message"`
	// RunID names the other run, for a concurrent_change: what `pleiades
	// journal show` takes.
	RunID string `json:"run_id,omitempty"`
	// Runbook is what the other run runs.
	Runbook string `json:"runbook,omitempty"`
	// PID is the other run's process, for a concurrent_run.
	PID int `json:"pid,omitempty"`
	// Devices names the devices the two runs share.
	Devices []string `json:"devices,omitempty"`
}

// RollbackReport is a rollback's own part of the run report.
type RollbackReport struct {
	// Of is the run undone.
	Of string `json:"of"`
	// Levels are the undo's tasks in the order they run.
	Levels [][]rollback.Step `json:"levels,omitempty"`
	// Left are changes left in place, by --leave.
	Left []rollback.Gap `json:"left,omitempty"`
	// Unknown are effects nobody can say were changes: failed actions and
	// an unsealed journal.
	Unknown []rollback.Gap `json:"unknown,omitempty"`
	// UnacceptedUnknown counts Unknown not named with --allow-unknown.
	UnacceptedUnknown int `json:"unaccepted_unknown"`
	// AlreadyUndone are changes an earlier rollback of the run undid.
	AlreadyUndone []rollback.Gap `json:"already_undone,omitempty"`
	// NothingToUndo are changes whose task's rollback: list is empty: its
	// runbook says undoing them means doing nothing.
	NothingToUndo []rollback.Gap `json:"nothing_to_undo,omitempty"`
	// OrderedByClock names the other runs whose order against the one undone
	// was read from the journals' clock times, because a journal predates the
	// project's run counter; every other run was ordered by the counter.
	OrderedByClock []string `json:"ordered_by_clock,omitempty"`
	// Problems are why the rollback was refused, when it was.
	Problems []rollback.Problem `json:"problems,omitempty"`
	// Provisional are, in a check, the steps on a device an earlier step of
	// the rollback changes first: their checks meet the device as it is now.
	Provisional []ProvisionalStep `json:"provisional,omitempty"`
}

// ProvisionalStep is one step whose check is provisional, and why.
type ProvisionalStep struct {
	// Name is the step's task name in the rollback run.
	Name string `json:"name"`
	// Device is the name of the device the step runs on.
	Device string `json:"device"`
	// Reason says which earlier step changes the device first.
	Reason string `json:"reason"`
}
