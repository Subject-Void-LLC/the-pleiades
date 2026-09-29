// Tests for the runbook's rollback keys: a task's rollback: list and the
// runbook's reversible: flag.
package engine_test

import (
	"strings"
	"testing"
)

// rollbackRunbook is a runbook whose first task carries an authored undo,
// written the way every task is written: the method as the key.
const rollbackRunbook = `id: undo-demo
hosts: web
reversible: true
tasks:
  - name: start the worker
    exec.command:
      cmd: systemctl start worker
    rollback:
      - name: stop it again
        exec.command:
          cmd: systemctl stop worker
      - name: and say so
        noop:
          msg: stopped
  - name: plain
    noop: {}
`

func TestRollbackKey_ParsesAsTasksThatAreNotNodes(t *testing.T) {
	dag, err := buildYAML(t, rollbackRunbook)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !dag.Reversible {
		t.Error("the runbook's reversible: true did not reach the DAG")
	}
	task := dag.Nodes["tasks[0]"]
	if task == nil || len(task.Rollback) != 2 {
		t.Fatalf("tasks[0] = %+v, want two rollback steps", task)
	}
	step := task.Rollback[0]
	if step.FQCN != "exec.command" || step.Params["cmd"] != "systemctl stop worker" || step.Name != "stop it again" {
		t.Errorf("first rollback step = %+v, want the module-as-key form read as a method call", step)
	}
	// The steps run only in a rollback, never in this run.
	for id, n := range dag.Nodes {
		if strings.Contains(id, "rollback") || (n.Name == "stop it again") {
			t.Errorf("a rollback step became node %s of the forward run", id)
		}
	}
}

// TestRollbackKey_DoesNotChangeTheVersion: adding an undo after a run
// failed is the ordinary way to write one, so it must not read as drift
// from the run it undoes.
func TestRollbackKey_DoesNotChangeTheVersion(t *testing.T) {
	plain := `id: undo-demo
hosts: web
tasks:
  - name: start the worker
    exec.command:
      cmd: systemctl start worker
  - name: plain
    noop: {}
`
	with, err := buildYAML(t, rollbackRunbook)
	if err != nil {
		t.Fatalf("build with rollback: %v", err)
	}
	without, err := buildYAML(t, plain)
	if err != nil {
		t.Fatalf("build without: %v", err)
	}
	if with.Version != without.Version {
		t.Errorf("adding rollback: and reversible: changed the version from %s to %s", without.Version, with.Version)
	}
	// The control: changing what the forward run does changes it.
	changed, err := buildYAML(t, strings.Replace(plain, "systemctl start worker", "systemctl start other", 1))
	if err != nil {
		t.Fatalf("build changed: %v", err)
	}
	if changed.Version == without.Version {
		t.Error("a different forward command hashed the same")
	}
}

func TestRollbackKey_RefusesWhatARollbackCannotRun(t *testing.T) {
	for _, tc := range []struct {
		name, runbook, want string
	}{
		{"on a block", `id: r
hosts: web
tasks:
  - name: group
    block:
      - noop: {}
    rollback:
      - noop: {}
`, "block or parallel"},
		{"inside rescue", `id: r
hosts: web
tasks:
  - name: group
    block:
      - noop: {}
    rescue:
      - name: recover
        noop: {}
        rollback:
          - noop: {}
`, "rescue: or always:"},
		{"a step with register", `id: r
hosts: web
tasks:
  - name: a
    noop: {}
    rollback:
      - noop: {}
        register: undo
`, "register"},
		{"a step with when", `id: r
hosts: web
tasks:
  - name: a
    noop: {}
    rollback:
      - noop: {}
        when: ["true"]
`, "when"},
		{"a step naming another device", `id: r
hosts: web
tasks:
  - name: a
    noop: {}
    rollback:
      - noop:
          target: db
`, "params.target"},
		{"a step that is a block", `id: r
hosts: web
tasks:
  - name: a
    noop: {}
    rollback:
      - block:
          - noop: {}
`, "one method call"},
		{"a nested rollback", `id: r
hosts: web
tasks:
  - name: a
    noop: {}
    rollback:
      - noop: {}
        rollback:
          - noop: {}
`, "rollback"},
		{"a step with two methods", `id: r
hosts: web
tasks:
  - name: a
    noop: {}
    rollback:
      - noop: {}
        exec.command: {cmd: x}
`, "exactly one module"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildYAML(t, tc.runbook)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("build = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}
