// The text view of a run: the plan, printed before anything runs, then
// each task's result and how the run ended, all read from the report.
package main

import (
	"fmt"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// printPlan prints what a run is about to do: the runbook, its mode, its
// reach, and its task tree, ending with the line results follow.
func printPlan(rep *runReport) {
	checking := rep.Mode == collection.ModeCheck
	fmt.Printf("plan for %s (%d nodes, %d inventory hosts loaded):\n", rep.Runbook, rep.Nodes, rep.InventoryHosts)
	if checking {
		fmt.Println("mode: check (tasks report what they would change; nothing on any device is changed)")
	}

	// ServiceEffecting and blast radius are runbook-level, native-only
	// summary facts (see engine.Metadata's doc comment), so they are
	// surfaced once up front rather than per task.
	fmt.Printf("service-effecting: %t\n", rep.ServiceEffecting)
	if len(rep.BlastRadius.Tiers) > 0 {
		fmt.Printf("blast radius: %d devices, tiers: %v\n", rep.BlastRadius.Devices, rep.BlastRadius.Tiers)
	} else {
		fmt.Printf("blast radius: %d devices\n", rep.BlastRadius.Devices)
	}
	fmt.Println()

	// The interesting structure (block/rescue/always nesting) lives in the
	// authored tree, not in the synthesized happy-path chain, so the plan
	// is printed from that tree. A section with no tasks is left out,
	// matching Ansible's own convention that pretasks and posttasks are
	// optional and tasks alone can carry a whole play.
	if rep.Selection != "" {
		fmt.Printf("selection: %s\n", termsafe.EscapeLine(rep.Selection))
	}
	for _, section := range rep.Plan {
		fmt.Printf("%s:\n", section.Section)
		printPlanTasks(section.Tasks, 1)
	}

	fmt.Println()
	if checking {
		fmt.Println("checking:")
	} else {
		fmt.Println("executing:")
	}
}

// printPlanTasks prints tasks in order, indented two spaces per depth.
// Each task prints its name, or its method if it has none, so a task
// always has something readable printed even though a name need not be
// unique. A group recurses one level deeper under a "block:" or
// "parallel:" label, a block's rescue and always as further labeled
// sections at that same depth, mirroring how Ansible authors read a
// block/rescue/always task.
func printPlanTasks(tasks []planTask, depth int) {
	indent := strings.Repeat("  ", depth)
	childIndent := strings.Repeat("  ", depth+1)
	for _, task := range tasks {
		label := task.Name
		if label == "" {
			label = task.Method
		}
		note := ""
		if !task.Selected {
			note = "  (not selected)"
		}
		// A task name is runbook text, and a runbook may be converted from
		// someone else's playbook, so it is escaped before a terminal sees
		// it (internal/termsafe).
		fmt.Printf("%s%s%s\n", indent, termsafe.EscapeLine(label), note)
		switch task.Kind {
		case "block":
			fmt.Printf("%sblock:\n", childIndent)
			printPlanTasks(task.Block, depth+2)
			if len(task.Rescue) > 0 {
				fmt.Printf("%srescue:\n", childIndent)
				printPlanTasks(task.Rescue, depth+2)
			}
			if len(task.Always) > 0 {
				fmt.Printf("%salways:\n", childIndent)
				printPlanTasks(task.Always, depth+2)
			}
		case "parallel":
			fmt.Printf("%sparallel:\n", childIndent)
			printPlanTasks(task.Parallel, depth+2)
		}
	}
}

// printResults prints each task's result and, for a run that did not
// fail, the line saying how it ended. A failed or incomplete run's last
// line is the error the command ends with, which main prints.
func printResults(rep *runReport, verbose bool) {
	for _, t := range rep.Tasks {
		label := taskLabel(t)
		// Every text below came from a device or a method and was masked
		// into the report; it is escaped here, for the terminal.
		switch t.Status {
		case statusUnchecked:
			note := ""
			if t.AllowedUnchecked {
				note = " [allowed by --allow-unchecked]"
			}
			fmt.Printf("  %s: COULD NOT CHECK (%s)%s\n", label, termsafe.Escape(t.Reason), note)
			continue
		case statusFailed:
			fmt.Printf("  %s: FAILED: %v\n", label, termsafe.Escape(t.Error))
		case statusSkipped:
			fmt.Printf("  %s: skipped (%s)\n", label, termsafe.Escape(t.Reason))
		case statusWouldChange:
			fmt.Printf("  %s: would change\n", label)
		case statusChanged:
			fmt.Printf("  %s: changed\n", label)
		case statusCheckedOnly:
			// check_mode on this task in a real run: it was asked, not run.
			fmt.Printf("  %s: ok (checked only)\n", label)
		default:
			fmt.Printf("  %s: ok\n", label)
		}
		// Always, verbose or not: a warning is something the person running
		// the work has to act on (sdk.StatWarnings).
		for _, w := range t.Warnings {
			fmt.Printf("    WARNING: %s\n", termsafe.EscapeLine(w))
		}
		if verbose && t.Provider != nil {
			fmt.Printf("    provided by: %s (%s)\n", termsafe.EscapeLine(t.Provider.Program), t.Provider.Digest)
		}
		if verbose && t.Status != statusFailed && t.Status != statusSkipped {
			printNodeStats(t.Stats, nil)
		}
	}

	if len(rep.Metadata) > 0 {
		fmt.Println()
		fmt.Println("metadata:")
		printMetadata(rep.Metadata, nil)
	}

	// The run id is printed whatever the outcome: a run that failed
	// partway is the one an operator most needs to find again, to read or
	// to roll back.
	if rep.RunID != "" {
		fmt.Println()
		fmt.Printf("run %s, journal %s\n", rep.RunID, termsafe.EscapeLine(rep.Journal))
	}

	switch rep.Outcome.Status {
	case "complete":
		fmt.Println()
		fmt.Println(rep.Outcome.Message)
	case "incomplete":
		// The tasks were named above, so the fix is visible without
		// rerunning anything; the error main prints says what is missing.
		fmt.Println()
	}
}

// taskLabel is how a result line names its task: the node id, and the
// device it ran on when it ran on one.
func taskLabel(t taskReport) string {
	if t.Device == "" {
		return t.ID
	}
	return fmt.Sprintf("%s [%s]", t.ID, t.Device)
}

// printMetadata prints result.Metadata (populated only from "set_metadata"
// tasks, see internal/engine's ActionResult.IsMetadata), sorted by
// register name, then device ID, then key, for deterministic output.
// Every value is masked through redact.Text using result.Secrets
// before printing: a set_metadata task can echo back a value an earlier
// register_mask/secret_mask task marked secret just as easily as any other
// task's output can.
func printMetadata(metadata map[string]interface{}, secrets []string) {
	registers := make([]string, 0, len(metadata))
	for name := range metadata {
		registers = append(registers, name)
	}
	sort.Strings(registers)

	for _, name := range registers {
		fmt.Printf("  %s:\n", name)

		byDevice, ok := metadata[name].(map[string]interface{})
		if !ok {
			continue
		}
		deviceIDs := make([]string, 0, len(byDevice))
		for id := range byDevice {
			deviceIDs = append(deviceIDs, id)
		}
		sort.Strings(deviceIDs)

		for _, deviceID := range deviceIDs {
			stats, ok := byDevice[deviceID].(map[string]interface{})
			if !ok {
				continue
			}
			keys := make([]string, 0, len(stats))
			for k := range stats {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			prefix := "    "
			if deviceID != "" {
				prefix = fmt.Sprintf("    [%s] ", deviceID)
			}
			for _, k := range keys {
				value := termsafe.EscapeLine(redact.Text(secrets, fmt.Sprintf("%v", stats[k])))
				fmt.Printf("%s%s: %s\n", prefix, termsafe.EscapeLine(k), value)
			}
		}
	}
}

// printNodeStats prints one successful node's own output under `run
// --verbose`: every key the action recorded, sorted, masked through this
// run's complete secret set.
//
// It exists because "ok" and "changed" are the whole of what a
// successful run used to say. A task that read a device's OS version, a
// file's mode or a service's state produced that answer, put it in
// Stats, and had it printed nowhere; the only way to see it from the CLI
// was to make the task exit non-zero, because the failure path prints
// the error and a Collection method's error carries its output. Several
// example runbooks and release gates were written that way, and a test
// that has to break something to observe it is testing the wrong thing.
//
// Not on by default. A run over a large inventory would otherwise print
// every device's whole stdout between the plan and the summary, which
// buries the one line saying whether the run succeeded.
//
// A multi-line value is printed under its key rather than beside it,
// because the common case here is exactly that: a captured stdout with
// newlines in it, which as a single line is unreadable and as %q is
// worse.
func printNodeStats(stats map[string]interface{}, secrets []string) {
	keys := make([]string, 0, len(stats))
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		// A value is a device's or a program's output, so anything in it a
		// terminal would act on is shown escaped (termsafe); lines stay
		// lines.
		value := termsafe.Escape(redact.Text(secrets, statText(stats[k])))
		name := termsafe.EscapeLine(k)
		if value == "" {
			continue
		}
		if !strings.Contains(value, "\n") {
			fmt.Printf("    %s: %s\n", name, value)
			continue
		}
		fmt.Printf("    %s:\n", name)
		for _, line := range strings.Split(strings.TrimRight(value, "\n"), "\n") {
			fmt.Printf("      %s\n", line)
		}
	}
}

// statText renders one stat for printNodeStats. A list or a map (a list
// of VMs, a diff) is written as indented YAML, one entry to a line, since
// Go's own rendering puts a whole list of maps on one unreadable line;
// anything else is written as Go writes it.
func statText(v any) string {
	switch v.(type) {
	case []any, []string, []map[string]any, map[string]any:
		var b strings.Builder
		enc := yaml.NewEncoder(&b)
		enc.SetIndent(2)
		if enc.Encode(v) == nil && enc.Close() == nil {
			return b.String()
		}
	}
	return fmt.Sprintf("%v", v)
}

// exitIncomplete is the status of a check that failed nothing but left
// tasks unchecked, and of a real run whose check_mode tasks could not be
// checked. It is neither success (0), since part of the plan was never
// looked at, nor failure (1), so a pipeline can tell the two apart and
// accept one without hiding the other.
const exitIncomplete = 3

// incompleteError is the error an incomplete run ends with, carrying
// exitIncomplete.
type incompleteError struct{ msg string }

// Error implements error.
func (e *incompleteError) Error() string { return e.msg }

// ExitCode implements exitCoder.
func (e *incompleteError) ExitCode() int { return exitIncomplete }
