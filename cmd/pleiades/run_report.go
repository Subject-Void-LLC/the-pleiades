// The report a run produces: its plan, what each task did, and how it
// ended. The text view and --json both render this one model, so they
// cannot disagree about what happened.
package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// taskStatus is what one task did on one device.
type taskStatus string

// The statuses a task reports, one per line the text view prints.
const (
	// statusOK ran and changed nothing.
	statusOK taskStatus = "ok"
	// statusChanged ran and changed something.
	statusChanged taskStatus = "changed"
	// statusWouldChange is a check that found something to change.
	statusWouldChange taskStatus = "would_change"
	// statusCheckedOnly is a check_mode task in a real run: asked, not run.
	statusCheckedOnly taskStatus = "checked_only"
	// statusFailed ended with an error.
	statusFailed taskStatus = "failed"
	// statusHeld ended holding its result for a person's decision
	// (sdk.HeldError): it failed the run closed, and is told apart from a
	// failure because running again after the decision continues it.
	// Phase 27 turns it into a pause (internal/engine's pause.go).
	statusHeld taskStatus = "held"
	// statusSkipped did not run, for the reason given.
	statusSkipped taskStatus = "skipped"
	// statusUnchecked could not say what it would change.
	statusUnchecked taskStatus = "unchecked"
	// statusConditionalSkip is a rescue task in a check: it runs only if its
	// block fails, which a check does not predict, so it is neither checked
	// nor a gap in the check.
	statusConditionalSkip taskStatus = "conditional_skip"
	// statusNotRun is a planned task the run never reached: validation
	// refused the runbook, an earlier failure ended the run, or it was
	// aborted. It has no device, since nothing resolved its target, and
	// its reason says which. It differs from skipped, whose task was
	// reached and passed over by its own condition.
	statusNotRun taskStatus = "not_run"
)

// runReport is one run, as both views print it.
type runReport struct {
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
	BlastRadius blastRadius `json:"blast_radius"`
	// Selection is the --tags and --skip-tags that chose the tasks.
	Selection string `json:"selection,omitempty"`
	// Plan is the authored task tree, section by section.
	Plan []planSection `json:"plan,omitempty"`
	// Validation is what stopped the run before it started.
	Validation []validationIssue `json:"validation,omitempty"`
	// Tasks is each task's result on each device, in the executor's order.
	Tasks []taskReport `json:"tasks,omitempty"`
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
	Warnings []runWarning `json:"warnings,omitempty"`
	// Rollback is, for `pleiades rollback`, what it planned: the run it
	// undoes, what it leaves in place, what nobody can say changed, and,
	// when it refused, why.
	Rollback *rollbackReport `json:"rollback,omitempty"`
	// Outcome is how the run ended.
	Outcome runOutcome `json:"outcome"`
}

// blastRadius is validate.BlastRadius as the report writes it.
type blastRadius struct {
	// Devices is the number of distinct devices any task targets.
	Devices int `json:"devices"`
	// Tiers are the distinct tier properties among them.
	Tiers []string `json:"tiers,omitempty"`
}

// planSection is one of pretasks, tasks and posttasks.
type planSection struct {
	// Section is "pretasks", "tasks" or "posttasks".
	Section string `json:"section"`
	// Tasks are the section's tasks, in order.
	Tasks []planTask `json:"tasks"`
}

// planTask is one authored task, with the groups nested under it.
type planTask struct {
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
	// Block, Rescue, Always and Parallel are a group's own tasks.
	Block    []planTask `json:"block,omitempty"`
	Rescue   []planTask `json:"rescue,omitempty"`
	Always   []planTask `json:"always,omitempty"`
	Parallel []planTask `json:"parallel,omitempty"`
}

// validationIssue is one validate.Finding.
type validationIssue struct {
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

// taskReport is one task's result on one device. Every text in it that
// came from a device or a method is masked.
type taskReport struct {
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
	Status taskStatus `json:"status"`
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
	// StartedAt and FinishedAt bound the task's own execution.
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// runOutcome is how a run ended, with the status the command exits with.
type runOutcome struct {
	// Status is complete, failed, incomplete, invalid or error.
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

// planReport fills the parts of the report known before anything runs.
func planReport(label string, items []pkginventory.InventoryItem, dag *engine.DAG, mode collection.Mode, world validate.WorldView) *runReport {
	radius := validate.CalculateBlastRadius(world)
	rep := &runReport{
		Runbook:          label,
		Mode:             mode,
		Nodes:            len(dag.Nodes),
		InventoryHosts:   len(items),
		ServiceEffecting: dag.Metadata.ServiceEffecting,
		BlastRadius:      blastRadius{Devices: radius.DeviceCount, Tiers: radius.Tiers},
		Selection:        describeSelection(dag.Selection),
	}
	for _, section := range []struct {
		label string
		tasks []engine.Task
	}{{"pretasks", dag.PreTasks}, {"tasks", dag.Tasks}, {"posttasks", dag.PostTasks}} {
		if len(section.tasks) > 0 {
			rep.Plan = append(rep.Plan, planSection{Section: section.label, Tasks: planTasks(dag, section.tasks, section.label)})
		}
	}
	return rep
}

// planTasks is the plan of tasks, whose node ids start with prefix.
func planTasks(dag *engine.DAG, tasks []engine.Task, prefix string) []planTask {
	out := make([]planTask, 0, len(tasks))
	for i := range tasks {
		task := &tasks[i]
		id := prefix + "[" + strconv.Itoa(i) + "]"
		entry := planTask{ID: id, Name: task.Name, Method: task.FQCN, Selected: dag.Nodes[id] != nil}
		switch task.Kind() {
		case engine.TaskKindBlock:
			entry.Kind = "block"
			entry.Block = planTasks(dag, task.Block, id+".block")
			entry.Rescue = planTasks(dag, task.Rescue, id+".rescue")
			entry.Always = planTasks(dag, task.Always, id+".always")
		case engine.TaskKindParallel:
			entry.Kind = "parallel"
			entry.Parallel = planTasks(dag, task.Parallel, id+".parallel")
		}
		out = append(out, entry)
	}
	return out
}

// validationIssues is a validation report's findings as the report writes
// them, each device named as well as identified.
func validationIssues(report validate.Report, items []pkginventory.InventoryItem) []validationIssue {
	hosts := hostNames(items)
	out := make([]validationIssue, 0, len(report.Findings))
	for _, f := range report.Findings {
		out = append(out, validationIssue{Rule: f.RuleName, Node: f.Node, Device: string(f.Device), Host: hosts[string(f.Device)], Message: f.Message})
	}
	return out
}

// hostNames maps each inventory item's id to its name.
func hostNames(items []pkginventory.InventoryItem) map[string]string {
	hosts := make(map[string]string, len(items))
	for _, item := range items {
		hosts[string(item.ID())] = item.Name()
	}
	return hosts
}

// taskReports is each node's result as the report writes it, masked
// through the run's complete secret set.
func taskReports(dag *engine.DAG, result *engine.RunResult, items []pkginventory.InventoryItem, allowUnchecked map[string]bool) []taskReport {
	hosts := hostNames(items)
	checking := result.Mode == collection.ModeCheck
	out := make([]taskReport, 0, len(result.Nodes))
	for _, node := range result.Nodes {
		// A task that ran more than once on a device is one report entry per
		// run, under the id its journal entry has (item and try appended).
		t := taskReport{ID: node.NodeID + node.Iteration, Device: node.Device, Host: hosts[node.Device], Provider: node.Provider,
			Label: redact.Text(result.Secrets, node.Label), Ignored: node.Ignored, Retried: node.Retried}
		if task := dag.Nodes[node.NodeID]; task != nil {
			t.Name, t.Method = task.Name, task.FQCN
		}
		if node.Undo != nil {
			t.Name, t.Method, t.Undoes = node.Undo.Name, node.Undo.Method, node.Undo.Node
		}
		if !node.StartedAt.IsZero() {
			started := node.StartedAt
			t.StartedAt = &started
		}
		if !node.FinishedAt.IsZero() {
			finished := node.FinishedAt
			t.FinishedAt = &finished
		}
		switch {
		case node.Conditional:
			t.Status, t.Reason = statusConditionalSkip, node.SkipReason
		case node.Unchecked:
			t.Status, t.Reason = statusUnchecked, redact.Text(result.Secrets, node.SkipReason)
			t.AllowedUnchecked = allowUnchecked[t.Method]
		case node.Err != nil:
			t.Status, t.Error = statusFailed, redact.Text(result.Secrets, node.Err.Error())
			if _, held := engine.HoldOf(node.Err); held {
				t.Status = statusHeld
			}
			t.Unreached = engine.Unreached(node.Err)
			t.RescuedBy = node.RescuedBy
		case node.Skipped:
			t.Status, t.Reason = statusSkipped, redact.Text(result.Secrets, node.SkipReason)
		case node.Changed && (checking || node.Checked):
			t.Status = statusWouldChange
		case node.Changed:
			t.Status = statusChanged
		case node.Checked && !checking:
			t.Status = statusCheckedOnly
		default:
			t.Status = statusOK
		}
		if len(node.Stats) > 0 {
			t.Stats, _ = redact.Value(result.Secrets, node.Stats).(map[string]any)
			t.Warnings = warningsOf(t.Stats)
		}
		out = append(out, t)
	}
	return out
}

// warningsOf returns stats' sdk.StatWarnings as text, one entry each.
func warningsOf(stats map[string]any) []string {
	switch list := stats[sdk.StatWarnings].(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, w := range list {
			out = append(out, fmt.Sprint(w))
		}
		return out
	}
	return nil
}
