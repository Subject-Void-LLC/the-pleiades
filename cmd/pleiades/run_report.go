// The report a run produces: its plan, what each task did, and how it
// ended. The text view and --json both render this one model, so they
// cannot disagree about what happened.
package main

import (
	"fmt"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The report's model lives in internal/clireport, where tools/gendocs
// reads it to publish its schema; these names are this command's for it.
type (
	// runReport is one run, as both views print it.
	runReport = clireport.RunReport
	// blastRadius is validate.BlastRadius as the report writes it.
	blastRadius = clireport.BlastRadius
	// planSection is one of pretasks, tasks and posttasks.
	planSection = clireport.PlanSection
	// planTask is one authored task, with the groups nested under it.
	planTask = clireport.PlanTask
	// validationIssue is one validate.Finding.
	validationIssue = clireport.ValidationIssue
	// taskReport is one task's result on one device, masked.
	taskReport = clireport.TaskReport
	// runOutcome is how a run ended, with the status the command exits with.
	runOutcome = clireport.RunOutcome
)

// The statuses a task reports (clireport's, documented there).
const (
	statusOK              = clireport.StatusOK
	statusChanged         = clireport.StatusChanged
	statusWouldChange     = clireport.StatusWouldChange
	statusCheckedOnly     = clireport.StatusCheckedOnly
	statusFailed          = clireport.StatusFailed
	statusHeld            = clireport.StatusHeld
	statusSkipped         = clireport.StatusSkipped
	statusUnchecked       = clireport.StatusUnchecked
	statusConditionalSkip = clireport.StatusConditionalSkip
	statusNotRun          = clireport.StatusNotRun
)

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
