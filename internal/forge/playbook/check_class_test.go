// The class and the check answer as a converted task's report carries them.
//
// check_class_internal_test.go holds the module table against the registry
// call by call; a table call cannot see a task's parameters, so it cannot
// tell a guarded command from an unguarded one. This converts real tasks,
// through the same Translate a migration runs, and reads what the report
// says about each: the class the table declared and whether that very call
// can run in check mode, with the reason when it cannot.
package playbook_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
)

func TestConvertedTasks_CarryTheirClassAndCheckAnswer(t *testing.T) {
	for _, tc := range []struct {
		name     string
		task     string
		class    playbook.Class
		canCheck bool
	}{
		{"a guarded command is checkable", `ansible.builtin.command: {cmd: "touch /tmp/marker", creates: /tmp/marker}`, playbook.ClassImperative, true},
		{"an unguarded command is not", `ansible.builtin.command: "touch /tmp/marker"`, playbook.ClassImperative, false},
		{"raw has no guard to give", `ansible.builtin.raw: uptime`, playbook.ClassImperative, false},
		{"a restart predicts its change", `ansible.builtin.systemd_service: {name: nginx, state: restarted}`, playbook.ClassImperative, true},
		{"a package state is checkable", `ansible.builtin.apt: {name: nginx}`, playbook.ClassAsserted, true},
		{"a wait only reads, and still cannot be checked", `ansible.builtin.wait_for: {port: 22}`, playbook.ClassObserve, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tasks := converted(convertTask(t, tc.task))
			if len(tasks) != 1 {
				t.Fatalf("converted into %d tasks, want 1: %+v", len(tasks), tasks)
			}
			got := tasks[0]
			if got.Class != tc.class {
				t.Errorf("class %s, want %s", got.Class, tc.class)
			}
			if got.CanCheck == nil {
				t.Fatal("a converted task carries no check answer")
			}
			if *got.CanCheck != tc.canCheck {
				t.Errorf("can_check %v, want %v (reason %q)", *got.CanCheck, tc.canCheck, got.CheckReason)
			}
			if !*got.CanCheck && got.CheckReason == "" {
				t.Error("a task that cannot be checked says nothing about why")
			}
		})
	}
}
