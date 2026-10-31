// Tests of handlers and notify through the real Builder and Executor: which
// handlers run, on which devices, how often and in what order, and what is
// refused.
package engine_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// touchLog records each call of the touch method as "<device>:<id>", in order.
type touchLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *touchLog) of(device string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, c := range l.calls {
		if d, id, _ := strings.Cut(c, ":"); d == device {
			out = append(out, id)
		}
	}
	return out
}

// registerTouch registers enginetest.touch: it records its call, reports
// changed when change is true, and fails when fail is true.
func registerTouch(t *testing.T) *touchLog {
	t.Helper()
	log := &touchLog{}
	registerTestMethod(t, "touch", collection.StatusImplemented,
		func(_ context.Context, _ sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			log.mu.Lock()
			log.calls = append(log.calls, device.Name()+":"+params["id"].(string))
			log.mu.Unlock()
			if params["fail"] == true {
				return collection.Result{}, errors.New("touch failed as asked")
			}
			return collection.Result{Changed: params["change"] == true}, nil
		})
	return log
}

// runHandlers runs runbook on devices d1 and d2 with a renderer, returning
// the run's result and error.
func runHandlers(t *testing.T, runbook string) (engine.RunResult, error) {
	t.Helper()
	devices := []inventory.InventoryItem{
		&inventorytest.Stub{StubID: "d1", StubName: "d1", StubState: inventory.StateActive},
		&inventorytest.Stub{StubID: "d2", StubName: "d2", StubState: inventory.StateActive},
	}
	x := engine.NewExecutor(mapResolver{"all": devices}, newBridge(&recordingFallback{}), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithRenderer(render.New()))
	return x.Run(context.Background(), yamlRunbook(t, runbook))
}

// TestNotify_RunOnceInDefinitionOrderWhereSomethingChanged: two tasks
// notify restart and one notifies a topic reload listens to; each handler
// runs once per device it was notified on, in the order handlers: lists them
// (reload first, though restart was notified first), after the section; a
// task that changed nothing notifies nothing; and a handler not notified on a
// device is skipped there as not notified.
func TestNotify_RunOnceInDefinitionOrderWhereSomethingChanged(t *testing.T) {
	log := registerTouch(t)
	result, err := runHandlers(t, `id: h
hosts: all
tasks:
  - name: a
    enginetest.touch:
      id: a
      change: "{{ device.name == 'd1' }}"
    notify: restart
  - name: b
    enginetest.touch:
      id: b
      change: true
    notify:
      - restart
      - config-changed
  - name: c
    enginetest.touch:
      id: c
      change: false
    notify: restart
handlers:
  - name: reload
    enginetest.touch:
      id: H-reload
    listen: config-changed
  - name: restart
    enginetest.touch:
      id: H-restart
  - name: never
    enginetest.touch:
      id: H-never
`)
	if err != nil || result.HasErrors() {
		t.Fatalf("Run: %v, %+v", err, result.Nodes)
	}
	for _, device := range []string{"d1", "d2"} {
		if got := strings.Join(log.of(device), ","); got != "a,b,c,H-reload,H-restart" {
			t.Errorf("%s ran %s, want a,b,c,H-reload,H-restart", device, got)
		}
	}
	skips := 0
	for _, nr := range result.Nodes {
		if nr.NodeID == "tasks.handlers[2]" {
			if !nr.Skipped || nr.SkipReason != engine.SkipReasonNotNotified {
				t.Errorf("the never-notified handler on %s: %+v", nr.Device, nr)
			}
			skips++
		}
	}
	if skips != 2 {
		t.Errorf("the never-notified handler has %d skips, want one per device", skips)
	}
}

// TestNotify_OnlyTheDevicesThatChangedRunIt: a notifier that changed only on
// d1 runs its handler only there.
func TestNotify_OnlyTheDevicesThatChangedRunIt(t *testing.T) {
	log := registerTouch(t)
	result, err := runHandlers(t, `id: h
hosts: all
tasks:
  - name: a
    enginetest.touch:
      id: a
      change: "{{ device.name == 'd1' }}"
    notify: restart
handlers:
  - name: restart
    enginetest.touch:
      id: H
`)
	if err != nil || result.HasErrors() {
		t.Fatalf("Run: %v, %+v", err, result.Nodes)
	}
	if d1, d2 := strings.Join(log.of("d1"), ","), strings.Join(log.of("d2"), ","); d1 != "a,H" || d2 != "a" {
		t.Errorf("d1 ran %s and d2 %s, want a,H and a", d1, d2)
	}
}

// TestNotify_EachSectionFlushesItsOwn: a handler notified in pretasks runs
// at the end of pretasks, and again at the end of tasks only when notified
// again there.
func TestNotify_EachSectionFlushesItsOwn(t *testing.T) {
	log := registerTouch(t)
	result, err := runHandlers(t, `id: h
hosts: all
pretasks:
  - name: p
    enginetest.touch:
      id: p
      change: true
    notify: restart
tasks:
  - name: t1
    enginetest.touch:
      id: t1
      change: true
    notify: restart
  - name: t2
    enginetest.touch:
      id: t2
      change: false
posttasks:
  - name: q
    enginetest.touch:
      id: q
      change: false
    notify: restart
handlers:
  - name: restart
    enginetest.touch:
      id: H
`)
	if err != nil || result.HasErrors() {
		t.Fatalf("Run: %v, %+v", err, result.Nodes)
	}
	if got := strings.Join(log.of("d1"), ","); got != "p,H,t1,t2,H,q" {
		t.Errorf("d1 ran %s, want p,H,t1,t2,H,q", got)
	}
}

// TestNotify_AFailureThatEndsTheRunRunsNone: a task that changed and
// notified, then a later failure, leaves the handler unrun, as Ansible does
// without force_handlers.
func TestNotify_AFailureThatEndsTheRunRunsNone(t *testing.T) {
	log := registerTouch(t)
	result, _ := runHandlers(t, `id: h
hosts: all
tasks:
  - name: a
    enginetest.touch:
      id: a
      change: true
    notify: restart
  - name: b
    enginetest.touch:
      id: b
      fail: true
handlers:
  - name: restart
    enginetest.touch:
      id: H
`)
	if !result.HasErrors() {
		t.Fatal("the failing task did not fail the run")
	}
	if got := strings.Join(log.of("d1"), ","); got != "a,b" {
		t.Errorf("d1 ran %s, want a,b and no handler", got)
	}
}

// TestNotify_ARescueTaskThatChangedNotifies: the failed task notifies
// nothing; the rescue task that ran and changed does, and the handler runs
// after the section, the failure having been rescued.
func TestNotify_ARescueTaskThatChangedNotifies(t *testing.T) {
	log := registerTouch(t)
	result, err := runHandlers(t, `id: h
hosts: all
tasks:
  - name: guarded
    block:
      - name: fails
        enginetest.touch:
          id: f
          fail: true
          change: true
        notify: other
    rescue:
      - name: recovers
        enginetest.touch:
          id: r
          change: true
        notify: restart
handlers:
  - name: other
    enginetest.touch:
      id: H-other
  - name: restart
    enginetest.touch:
      id: H-restart
`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = result
	if got := strings.Join(log.of("d1"), ","); got != "f,r,H-restart" {
		t.Errorf("d1 ran %s, want f,r,H-restart", got)
	}
}

// TestNotify_RunWhateverTheTagFilter: a run selecting only the notifier's
// tag still runs the handler it notified.
func TestNotify_RunWhateverTheTagFilter(t *testing.T) {
	log := registerTouch(t)
	dag := yamlRunbook(t, `id: h
hosts: all
tasks:
  - name: a
    enginetest.touch:
      id: a
      change: true
    notify: restart
    tags: [deploy]
  - name: b
    enginetest.touch:
      id: b
    tags: [other]
handlers:
  - name: restart
    enginetest.touch:
      id: H
`)
	selected, err := engine.Select(dag, engine.TagFilter{Tags: []string{"deploy"}})
	if err != nil {
		t.Fatal(err)
	}
	devices := []inventory.InventoryItem{&inventorytest.Stub{StubID: "d1", StubName: "d1", StubState: inventory.StateActive}}
	x := engine.NewExecutor(mapResolver{"all": devices}, newBridge(&recordingFallback{}), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
	if result, err := x.Run(context.Background(), selected); err != nil || result.HasErrors() {
		t.Fatalf("Run: %v, %+v", err, result.Nodes)
	}
	if got := strings.Join(log.of("d1"), ","); got != "a,H" {
		t.Errorf("d1 ran %s, want a,H", got)
	}
}

// TestNotify_RefuseWhatCannotMeanWhatItSays: each refusal names where.
func TestNotify_RefuseWhatCannotMeanWhatItSays(t *testing.T) {
	const task = "tasks:\n  - name: t\n    noop: {}\n"
	for name, tc := range map[string]struct{ yaml, want string }{
		"an unknown handler":     {"id: x\ntasks:\n  - name: t\n    noop: {}\n    notify: nope\n", `notify: names "nope", which no handler is named or listens to`},
		"a handler with no name": {"id: x\n" + task + "handlers:\n  - noop: {}\n", "a handler needs a name"},
		"two handlers one name":  {"id: x\n" + task + "handlers:\n  - name: h\n    noop: {}\n  - name: h\n    noop: {}\n", `two handlers are named "h"`},
		"a block handler":        {"id: x\n" + task + "handlers:\n  - name: h\n    block:\n      - name: a\n        noop: {}\n", "a block, a parallel group or import_tasks is not supported as a handler"},
		"a handler notifying":    {"id: x\n" + task + "handlers:\n  - name: h\n    noop: {}\n    notify: g\n  - name: g\n    noop: {}\n", "a handler notifying another handler is not supported"},
		"listen on a task":       {"id: x\ntasks:\n  - name: t\n    noop: {}\n    listen: topic\n", "listen: belongs on a handler"},
		"notify on a block":      {"id: x\ntasks:\n  - name: g\n    block:\n      - name: a\n        noop: {}\n    notify: h\nhandlers:\n  - name: h\n    noop: {}\n", "not on a block or a parallel group"},
		"an empty topic":         {"id: x\n" + task + "handlers:\n  - name: h\n    noop: {}\n    listen: \"\"\n", "empty topic"},
	} {
		if _, err := buildYAML(t, tc.yaml); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %v, want one saying %q", name, err, tc.want)
		}
	}
}

// TestNotify_AreInTheRunbooksVersion: handlers and notify change what a run
// does, so they change the version a rollback is held to.
func TestNotify_AreInTheRunbooksVersion(t *testing.T) {
	base := "id: v\ntasks:\n  - name: a\n    noop: {}\n"
	plain, err := buildYAML(t, base)
	if err != nil {
		t.Fatal(err)
	}
	notified, err := buildYAML(t, "id: v\ntasks:\n  - name: a\n    noop: {}\n    notify: h\nhandlers:\n  - name: h\n    noop: {}\n")
	if err != nil {
		t.Fatal(err)
	}
	if plain.Version == notified.Version {
		t.Error("a runbook with a handler hashes as one without")
	}
	if _, ok := notified.Nodes["tasks.handlers[0]"]; !ok {
		t.Errorf("the handler was not spliced after tasks: nodes %v", notified.Nodes)
	}
	if _, ok := plain.Nodes["tasks.handlers[0]"]; ok {
		t.Error("a section that notifies nothing got a handler")
	}
}
