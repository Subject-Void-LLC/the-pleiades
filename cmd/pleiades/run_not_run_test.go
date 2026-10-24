// End-to-end tests, through the built binary, of what a run that does not
// reach the end of its plan says about the tasks it never ran, of a run
// variable nobody supplied, and of naming a device by its name.
package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stoppedRunbook runs one task, fails the second (its target names no
// device), and so never reaches a block, the block's rescue, or the last
// task, which reads a run variable.
const stoppedRunbook = `# What this does: one task, then a failure that stops the run before the rest.
# Blast radius: no device; every task is the noop keyword.
# Changes: nothing.
# Undo: nothing to undo.
id: stopped
tasks:
  - name: first
    noop:
      msg: hello
  - name: breaks
    noop:
      target: nosuch
  - name: grouped
    block:
      - name: never one
        noop:
          msg: one
    rescue:
      - name: never two
        noop:
          msg: two
  - name: never three
    noop:
      msg: "{{ vars.candidate }}"
`

// TestCLI_AStoppedRunListsTheTasksItNeverRan: a run that a failure stops
// lists every planned task it never reached as not_run, with the failure
// that stopped it as the reason, in the plan's order and after what ran;
// the text view says the same. A refused run lists every task, the first
// one too, since nothing ran.
func TestCLI_AStoppedRunListsTheTasksItNeverRan(t *testing.T) {
	dir := adhocProject(t)
	rb := writeRunbook(t, dir, "stopped", stoppedRunbook)

	rep, raw, code := runPleiadesJSON(t, dir, "run", rb, "-e", "candidate=c.conf")
	if code != 1 || rep.Outcome.Status != "failed" {
		t.Fatalf("exit %d, report:\n%s", code, raw)
	}
	want := []struct{ id, status string }{
		{"tasks[0]", "ok"}, {"tasks[1]", "failed"},
		{"tasks[2].block[0]", "not_run"}, {"tasks[2].rescue[0]", "not_run"}, {"tasks[3]", "not_run"},
	}
	if len(rep.Tasks) != len(want) {
		t.Fatalf("%d tasks, want %d:\n%s", len(rep.Tasks), len(want), raw)
	}
	for i, w := range want {
		got := rep.Tasks[i]
		if got.ID != w.id || got.Status != w.status {
			t.Errorf("tasks[%d] = %s %s, want %s %s", i, got.ID, got.Status, w.id, w.status)
		}
		if w.status != "not_run" {
			continue
		}
		if got.Reason != "the run stopped after tasks[1] failed" || got.Device != "" || got.Host != "" || got.StartedAt != nil {
			t.Errorf("%s reads %+v, want no device, no time, and the failure that stopped the run", got.ID, got)
		}
	}
	if rep.Tasks[4].Name != "never three" || rep.Tasks[4].Method != "noop" {
		t.Errorf("a task never run lost its name or method: %+v", rep.Tasks[4])
	}

	out, err := runPleiades(t, dir, "run", rb, "-e", "candidate=c.conf")
	if err == nil || !strings.Contains(out, "tasks[3]: not run (the run stopped after tasks[1] failed)") {
		t.Errorf("the text view does not name the task it never ran: %v\n%s", err, out)
	}

	// Without the variable the runbook is refused before anything runs, and
	// every task, the first included, is not_run for that reason.
	rep, raw, code = runPleiadesJSON(t, dir, "run", rb)
	if code != 1 || rep.Outcome.Status != "invalid" || len(rep.Tasks) != 5 {
		t.Fatalf("refused run: exit %d, report:\n%s", code, raw)
	}
	for _, task := range rep.Tasks {
		if task.Status != "not_run" || task.Reason != "validation refused the runbook, so nothing ran" {
			t.Errorf("refused run's %s reads %s (%s)", task.ID, task.Status, task.Reason)
		}
	}
}

// TestCLI_AMissingRunVariableIsFoundBeforeAnythingRuns: validate takes
// --extra-vars and -e, read as run reads them, and names a variable a
// parameter template reads that none of them supplies, with its task; run
// refuses the same runbook with the same finding before any task runs, so
// the device the first task targets is never contacted.
func TestCLI_AMissingRunVariableIsFoundBeforeAnythingRuns(t *testing.T) {
	dir := adhocProject(t)
	rb := writeRunbook(t, dir, "needs_var", `# What this does: echoes a run variable on web01.
# Blast radius: web01, read only.
# Changes: nothing.
# Undo: nothing to undo.
id: needs_var
hosts: web01
tasks:
  - name: read first
    exec.command:
      argv: [uname, -r]
  - name: use the candidate
    noop:
      msg: "{{ vars.candidate }}"
      region: "{{ vars.region | default('none') }}"
`)
	const finding = `[vars] node "tasks[1]": task tasks[1] (name "use the candidate"), parameter msg: no value is supplied for vars.candidate`

	out, err := runPleiades(t, dir, "validate", rb)
	if err == nil || !strings.Contains(out, finding) || strings.Contains(out, "vars.region") {
		t.Errorf("validate with no variables: %v\n%s", err, out)
	}
	for _, flag := range []string{"-e", "--extra-vars"} {
		if out, err := runPleiades(t, dir, "validate", rb, flag, "candidate=configs/r3.conf"); err != nil || !strings.Contains(out, "validate: no issues found") {
			t.Errorf("validate %s candidate=...: %v\n%s", flag, err, out)
		}
	}
	// The same parser as run's: a file of variables, and a name given twice
	// refused.
	vars := filepath.Join(dir, "vars.yaml")
	if err := os.WriteFile(vars, []byte("candidate: configs/r3.conf\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiades(t, dir, "validate", rb, "-e", "@"+vars); err != nil || !strings.Contains(out, "no issues found") {
		t.Errorf("validate -e @file: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "validate", rb, "-e", "@"+vars, "-e", "candidate=x"); err == nil || !strings.Contains(out, "sets candidate twice") {
		t.Errorf("validate with a name given twice: %v\n%s", err, out)
	}

	rep, raw, code := runPleiadesJSON(t, dir, "run", rb)
	if code != 1 || rep.Outcome.Status != "invalid" || len(rep.Validation) != 1 || rep.Validation[0].Rule != "vars" ||
		!strings.Contains(rep.Validation[0].Message, "vars.candidate") || rep.Validation[0].Node != "tasks[1]" {
		t.Fatalf("run with no variables: exit %d, report:\n%s", code, raw)
	}
	// Nothing ran: the first task, which would have dialed web01, is listed
	// as never run, with no device and no time.
	if len(rep.Tasks) != 2 || rep.Tasks[0].Status != "not_run" || rep.Tasks[0].StartedAt != nil || rep.RunID != "" {
		t.Errorf("a refused run ran something:\n%s", raw)
	}
}

// TestCLI_AnUncheckedTaskIsNamedByItsDevicesName: the outcome's list of
// tasks --allow-unchecked accepted names each device by name, as the text
// view does, while each task keeps its device's id in device and its name
// in host.
func TestCLI_AnUncheckedTaskIsNamedByItsDevicesName(t *testing.T) {
	dir := adhocProject(t)
	rb := writeRunbook(t, dir, "unchecked", `# What this does: runs a command on web01, which a check cannot predict.
# Blast radius: web01.
# Changes: whatever uptime changes, which is nothing.
# Undo: nothing to undo.
id: unchecked
hosts: web01
tasks:
  - name: uptime
    ssh_exec:
      cmd: uptime
`)
	rep, raw, code := runPleiadesJSON(t, dir, "run", rb, "--mode", "check", "--allow-unchecked", "ssh_exec")
	if code != 0 || len(rep.Tasks) != 1 {
		t.Fatalf("exit %d, report:\n%s", code, raw)
	}
	task := rep.Tasks[0]
	if task.Host != "web01" || task.Device == "" || task.Device == "web01" || task.Status != "unchecked" {
		t.Fatalf("the task reads %+v, want web01's id in device and its name in host", task)
	}
	if got := rep.Outcome.AllowedUnchecked; len(got) != 1 || got[0] != "tasks[0] [web01]" {
		t.Errorf("allowed_unchecked = %v, want [tasks[0] [web01]]", got)
	}
	if strings.Contains(rep.Outcome.Message, task.Device) {
		t.Errorf("the outcome names the device by its id: %s", rep.Outcome.Message)
	}
}
