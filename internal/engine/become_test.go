// Tests for the become keys: what a runbook, a block and a task may say, which
// task each one reaches, what is refused, and what the executor tells a method.
package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// becomeOf builds a runbook and returns each leaf task's account by name, ""
// for one that runs as the login.
func becomeOf(t *testing.T, runbook string) map[string]string {
	t.Helper()
	dag, err := buildYAML(t, runbook)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, task := range dag.Nodes {
		if task.FQCN == "" {
			continue
		}
		user, _ := engine.BecomeFor(task)
		out[task.Name] = user
	}
	return out
}

// TestBecome_TheNarrowestKeyWins: a runbook's become reaches every task, a
// block's replaces it inside the block, become: false turns it off, and a
// task's become_user replaces the account but keeps the become.
func TestBecome_TheNarrowestKeyWins(t *testing.T) {
	got := becomeOf(t, `id: b
become: true
tasks:
  - name: plain
    noop: {}
  - name: as netadmin
    noop: {}
    become_user: netadmin
  - name: off
    noop: {}
    become: false
  - name: group
    become_user: svc
    block:
      - name: in group
        noop: {}
      - name: in group, off
        noop: {}
        become: false
      - name: in group, own user
        noop: {}
        become_user: other
    rescue:
      - name: in rescue
        noop: {}
  - name: parallel
    parallel:
      - name: in parallel
        noop: {}
`)
	want := map[string]string{
		"plain": "root", "as netadmin": "netadmin", "off": "",
		"in group": "svc", "in group, off": "", "in group, own user": "other",
		"in rescue": "svc", "in parallel": "root",
	}
	for name, user := range want {
		if got[name] != user {
			t.Errorf("task %q becomes %q, want %q", name, got[name], user)
		}
	}
	// With no become anywhere, nothing becomes.
	for name, user := range becomeOf(t, "id: n\ntasks:\n  - name: a\n    noop: {}\n") {
		if user != "" {
			t.Errorf("task %q becomes %q with no become key", name, user)
		}
	}
}

// TestBecome_RefusesWhatCannotMeanWhatItSays: each refusal names where.
func TestBecome_RefusesWhatCannotMeanWhatItSays(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, want string }{
		"su":                       {"id: x\ntasks:\n  - name: a\n    noop: {}\n    become: true\n    become_method: su\n", `become_method "su" is not supported`},
		"a bad account":            {"id: x\ntasks:\n  - name: a\n    noop: {}\n    become: true\n    become_user: \"root; id\"\n", "is not an account name"},
		"a template account":       {"id: x\ntasks:\n  - name: a\n    noop: {}\n    become: true\n    become_user: \"{{ who }}\"\n", "not as a template"},
		"user with become false":   {"id: x\ntasks:\n  - name: a\n    noop: {}\n    become: false\n    become_user: bob\n", "become: false"},
		"runbook level, bad":       {"id: x\nbecome: true\nbecome_method: doas\ntasks:\n  - name: a\n    noop: {}\n", `become_method "doas"`},
		"become_exe stays refused": {"id: x\ntasks:\n  - name: a\n    noop: {}\n    become_exe: /usr/bin/sudo\n", "an Ansible keyword"},
		"become_flags stays":       {"id: x\ntasks:\n  - name: a\n    noop: {}\n    become_flags: -H\n", "an Ansible keyword"},
	} {
		_, err := buildYAML(t, tc.yaml)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %v, want one mentioning %q", name, err, tc.want)
		}
	}
	// A rollback step may not set it: it replays the account the task ran as.
	_, err := buildYAML(t, "id: x\ntasks:\n  - name: a\n    noop: {}\n    rollback:\n      - name: r\n        noop: {}\n        become: true\n")
	if err == nil || !strings.Contains(err.Error(), "become") {
		t.Errorf("a rollback step with become: %v", err)
	}
}

// TestBecome_ChangesTheRunbooksVersion: the account is part of what a run
// does, so it is part of the version a rollback is held to.
func TestBecome_ChangesTheRunbooksVersion(t *testing.T) {
	plain, err := buildYAML(t, "id: v\ntasks:\n  - name: a\n    noop: {}\n")
	if err != nil {
		t.Fatal(err)
	}
	elevated, err := buildYAML(t, "id: v\ntasks:\n  - name: a\n    noop: {}\n    become: true\n")
	if err != nil {
		t.Fatal(err)
	}
	if plain.Version == elevated.Version {
		t.Error("a runbook with become hashes as one without")
	}
}

// TestCollectionActionExecutor_AMethodSeesTheAccountItBecomes: the context a
// method runs in says which account, a task without become says none, and the
// result records the account.
func TestCollectionActionExecutor_AMethodSeesTheAccountItBecomes(t *testing.T) {
	var seen []string
	name := registerTestMethod(t, "becomes", collection.StatusImplemented,
		func(ctx context.Context, _ sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			user, ok := sdk.BecomeFrom(ctx)
			if !ok {
				user = "(none)"
			}
			seen = append(seen, user)
			return collection.Result{}, nil
		})
	on := true
	exec := newBridge(&recordingFallback{})
	device := &inventorytest.Stub{StubName: "sw1"}

	res, err := exec.Execute(context.Background(), &engine.Task{FQCN: name, Become: &on, BecomeUser: "netadmin"}, device)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats[engine.StatBecome] != "netadmin" {
		t.Errorf("stats %v do not record the account", res.Stats)
	}
	if _, err := exec.Execute(context.Background(), &engine.Task{FQCN: name}, device); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != "netadmin" || seen[1] != "(none)" {
		t.Errorf("the method saw %v, want [netadmin (none)]", seen)
	}
}

// TestCollectionActionExecutor_BecomeIsRefusedOnARunner: a dispatch the
// per-task child would run as the login is refused, never carried out
// unelevated.
func TestCollectionActionExecutor_BecomeIsRefusedOnARunner(t *testing.T) {
	name := registerTestMethod(t, "runner", collection.StatusImplemented,
		func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			t.Error("the method ran")
			return collection.Result{}, nil
		})
	on := true
	invoker := func(context.Context, collection.Descriptor, inventory.InventoryItem, map[string]interface{}, collection.Mode) (collection.Result, map[string]interface{}, error) {
		t.Error("the invoker ran")
		return collection.Result{}, nil, nil
	}
	exec := engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewDeviceRunbookContext, engine.WithCollectionInvoker(invoker))
	_, err := exec.Execute(context.Background(), &engine.Task{FQCN: name, Become: &on}, &inventorytest.Stub{StubName: "sw1"})
	if err == nil || !strings.Contains(err.Error(), "become is not carried to a Runner") {
		t.Errorf("err %v", err)
	}
}

// TestJournal_RecordsTheAccountATaskBecame: an entry carries the account the
// task ran as, and none for a task that ran as its login, so a rollback can
// replay the undo as the same account.
func TestJournal_RecordsTheAccountATaskBecame(t *testing.T) {
	sink := &journalSink{}
	dev := &inventorytest.Stub{StubID: "sw1", StubName: "sw1", StubState: inventory.StateActive}
	runJournaled(t, `{
		"id": "journal-become",
		"hosts": "sw1",
		"tasks": [
			{"name": "elevated", "fqcn": "noop", "become": true, "become_user": "svc"},
			{"name": "plain", "fqcn": "noop"}
		]
	}`, mapResolver{"sw1": {dev}}, deviceRecordingActionExecutor{onExecute: func(inventory.InventoryItem) {}}, sink)
	got := map[string]string{}
	for _, e := range sink.flat() {
		got[e.TaskName] = e.Become
	}
	if got["elevated"] != "svc" || got["plain"] != "" {
		t.Errorf("journal become = %v, want elevated svc and plain empty", got)
	}
}
