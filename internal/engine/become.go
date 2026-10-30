// Package engine: the become keys a runbook, a block or a task may carry, and
// the one rule that says which account a task's commands run as.
package engine

import (
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// BecomeMethodSudo is the only become_method: sudo, run without a password
// (sudo -n). Ansible's su, doas and the rest are refused, since each has its
// own way of asking for a password, which this platform does not carry.
const BecomeMethodSudo = "sudo"

// StatBecome is the stat a task that ran as another account records: the
// account's name.
const StatBecome = "become"

// DefaultBecomeUser is the account become runs commands as when become_user
// does not say, as in Ansible.
const DefaultBecomeUser = "root"

// BecomeFor returns the account a task's commands run as, and whether it runs
// as another account at all. The builder has already copied a runbook's and a
// block's keys down onto each task (propagateBecome), so the executor reads
// only the task.
//
// It is the one statement of the rule: the executor decides what to run with
// it, validation decides what to refuse with it, and a rollback replays what
// it recorded, so the three cannot disagree about which tasks run elevated.
func BecomeFor(task *Task) (user string, on bool) {
	if task == nil || task.Become == nil || !*task.Become {
		return "", false
	}
	if task.BecomeUser == "" {
		return DefaultBecomeUser, true
	}
	return task.BecomeUser, true
}

// checkBecomeKeys refuses a become, become_user or become_method that cannot
// mean what it says. label names the place in a message. A template is
// refused for the reason check_mode's is: the account a task runs as must be
// what the file says, not something the run decides.
func checkBecomeKeys(label string, become *bool, user, method string) error {
	if IsTemplate(user) || IsTemplate(method) {
		return fmt.Errorf("%s: become_user and become_method must be written literally, not as a template: the account a task runs as is decided by the runbook", label)
	}
	if method != "" && method != BecomeMethodSudo {
		return fmt.Errorf("%s: become_method %q is not supported: only %s, run without a password (sudo -n), is; %s says what each Ansible keyword becomes", label, method, BecomeMethodSudo, migrationGuide)
	}
	if user != "" && !remoteexec.ValidBecomeUser(user) {
		return fmt.Errorf("%s: become_user %q is not an account name (lower-case letters, digits, _ and -, at most 32)", label, user)
	}
	if become != nil && !*become && (user != "" || method != "") {
		return fmt.Errorf("%s: become_user or become_method with become: false says to become an account and not to", label)
	}
	return nil
}

// propagateBecome pushes every become key down to the tasks it covers, so each
// node the executor visits carries its own answer: a runbook's keys to every
// task, a block's (or an import_tasks task's, already a block) to its block,
// rescue and always tasks, a parallel group's to its children, at any depth.
// The narrowest key wins, so become: false inside a become: true block turns
// it off for those tasks, and a task's become_user replaces the block's. After
// it, a task that runs elevated has Become true and its account in
// BecomeUser; one that does not has Become nil or false. It must run after
// resolveImportTasks and before anything reads Task.Become.
func propagateBecome(def *WorkflowDef) {
	inherited := becomeScope{on: def.Become != nil && *def.Become, user: def.BecomeUser, method: def.BecomeMethod}
	for _, list := range [][]Task{def.PreTasks, def.Tasks, def.PostTasks} {
		propagateBecomeInto(list, inherited)
	}
}

// becomeScope is the become answer a task inherits.
type becomeScope struct {
	on           bool
	user, method string
}

// propagateBecomeInto resolves each task in tasks against inherited, then
// recurses into its children with the task's own answer.
func propagateBecomeInto(tasks []Task, inherited becomeScope) {
	for i := range tasks {
		t := &tasks[i]
		own := inherited
		if t.Become != nil {
			own.on = *t.Become
		}
		if t.BecomeUser != "" {
			own.user = t.BecomeUser
		}
		if t.BecomeMethod != "" {
			own.method = t.BecomeMethod
		}
		if own.on {
			on := true
			t.Become = &on
			t.BecomeUser = strings.TrimSpace(own.user)
			if t.BecomeUser == "" {
				t.BecomeUser = DefaultBecomeUser
			}
			t.BecomeMethod = BecomeMethodSudo
		} else if t.Become != nil {
			// An explicit false stays false, and carries no account.
			t.BecomeUser, t.BecomeMethod = "", ""
		}
		for _, sub := range [][]Task{t.Block, t.Rescue, t.Always, t.Parallel} {
			propagateBecomeInto(sub, own)
		}
	}
}

// validateBecomeKeys refuses a become key that cannot mean what it says, on
// the runbook and on every task, block and parallel group in it, naming where.
// It runs before propagateBecome, which would otherwise hide a contradiction
// (become_user beside become: false) by resolving it.
func validateBecomeKeys(def *WorkflowDef) error {
	if err := checkBecomeKeys("runbook", def.Become, def.BecomeUser, def.BecomeMethod); err != nil {
		return err
	}
	for _, list := range [][]Task{def.PreTasks, def.Tasks, def.PostTasks} {
		if err := validateBecomeIn(list); err != nil {
			return err
		}
	}
	return nil
}

// validateBecomeIn checks each task in tasks and the tasks inside it.
func validateBecomeIn(tasks []Task) error {
	for i := range tasks {
		t := &tasks[i]
		if err := checkBecomeKeys(fmt.Sprintf("task %q", t.Name), t.Become, t.BecomeUser, t.BecomeMethod); err != nil {
			return err
		}
		for _, sub := range [][]Task{t.Block, t.Rescue, t.Always, t.Parallel} {
			if err := validateBecomeIn(sub); err != nil {
				return err
			}
		}
	}
	return nil
}
