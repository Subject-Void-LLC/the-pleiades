// Tests of async and poll through the real Builder and Executor: the limit
// stops a task, a background task runs while later tasks do, async_status
// collects it, the journal holds its change and not its start, a job nobody
// collected is waited for, and what cannot mean what it says is refused.
package engine_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// slowLog records when each call of the slow method began and ended.
type slowLog struct {
	mu    sync.Mutex
	marks []string
}

func (l *slowLog) mark(s string) {
	l.mu.Lock()
	l.marks = append(l.marks, s)
	l.mu.Unlock()
}

func (l *slowLog) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.marks, ",")
}

// registerSlow registers enginetest.slow: it marks "<id>+" as it starts,
// waits ms milliseconds or until its context ends, marks "<id>-" and reports
// a change, or fails with fail.
func registerSlow(t *testing.T) *slowLog {
	t.Helper()
	log := &slowLog{}
	registerTestMethod(t, "slow", collection.StatusImplemented,
		func(ctx context.Context, _ sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			id, _ := params["id"].(string)
			ms, _ := params["ms"].(int)
			log.mark(id + "+")
			select {
			case <-time.After(time.Duration(ms) * time.Millisecond):
			case <-ctx.Done():
				log.mark(id + "!")
				return collection.Result{}, ctx.Err()
			}
			log.mark(id + "-")
			if params["fail"] == true {
				return collection.Result{}, errors.New("slow failed as asked")
			}
			return collection.Result{Changed: true}, nil
		})
	return log
}

// runAsync runs runbook on one device d1 with a journal sink and a renderer.
func runAsync(t *testing.T, runbook string, sink *journalSink) engine.RunResult {
	t.Helper()
	device := &inventorytest.Stub{StubID: "d1", StubName: "d1", StubState: inventory.StateActive}
	x := engine.NewExecutor(mapResolver{"all": {device}}, newBridge(&recordingFallback{}), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithRenderer(render.New()), engine.WithJournal(sink))
	result, err := x.Run(context.Background(), yamlRunbook(t, runbook))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result
}

// TestAsync_TheLimitStopsATask: a method still running at its async limit
// has its context cancelled, and the task fails naming the limit.
func TestAsync_TheLimitStopsATask(t *testing.T) {
	log := registerSlow(t)
	result := runAsync(t, `id: a
hosts: all
tasks:
  - name: too slow
    enginetest.slow:
      id: s
      ms: 5000
    async: 1
    poll: 1
`, &journalSink{})
	if !result.HasErrors() || !strings.Contains(result.Nodes[0].Err.Error(), "its async limit of 1s ran out") {
		t.Fatalf("result %+v, want a failure naming the async limit", result.Nodes)
	}
	if got := log.joined(); got != "s+,s!" {
		t.Errorf("the method marked %q, want s+,s! (stopped by its context)", got)
	}
}

// TestAsync_ABackgroundTaskRunsWhileTheRunGoesOn: a poll: 0 task starts and
// the next task on the same device runs while it does, sharing its lock; the
// async_status collects its real result; the journal holds the job's change
// under its own node, and nothing for its start; its register holds the job id.
func TestAsync_ABackgroundTaskRunsWhileTheRunGoesOn(t *testing.T) {
	log := registerSlow(t)
	sink := &journalSink{}
	result := runAsync(t, `id: a
hosts: all
tasks:
  - name: long
    enginetest.slow:
      id: long
      ms: 600
    async: 30
    poll: 0
    register: job
  - name: meanwhile
    enginetest.slow:
      id: quick
      ms: 10
  - name: collect
    async_status:
      jid: "{{ result.job.ansible_job_id }}"
    register: done
`, sink)
	if result.HasErrors() {
		t.Fatalf("run failed: %+v", result.Nodes)
	}
	// Which of the two starts first is the scheduler's choice; what matters
	// is that the quick task finished while the long one still ran.
	if got := log.joined(); got != "long+,quick+,quick-,long-" && got != "quick+,long+,quick-,long-" {
		t.Errorf("marks %q, want the quick task to start and finish inside the long one", got)
	}
	var start, collect engine.NodeResult
	for _, nr := range result.Nodes {
		switch nr.NodeID {
		case "tasks[0]":
			if nr.Changed {
				collect = nr
			} else {
				start = nr
			}
		}
	}
	if id, _ := start.Stats[engine.StatJobID].(string); id == "" {
		t.Errorf("the start registered no job id: %+v", start)
	}
	if !collect.Changed {
		t.Errorf("the collected job is not in the results as a change: %+v", result.Nodes)
	}
	entries := byNode(sink.flat())
	if e, ok := entries["tasks[0]|d1"]; !ok || e.Outcome != engine.OutcomeChanged {
		t.Errorf("journal for the background task: %+v (found %v), want one changed entry", e, ok)
	}
	n := 0
	for _, e := range sink.flat() {
		if e.NodeID == "tasks[0]" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the background task has %d journal entries, want 1 (its change, not its start)", n)
	}
}

// TestAsync_AJobNobodyCollectsIsWaitedForAndJournaled: an async_status that
// does not run (its when is false) leaves the job to Run, which waits for it
// and journals it before returning.
func TestAsync_AJobNobodyCollectsIsWaitedForAndJournaled(t *testing.T) {
	log := registerSlow(t)
	sink := &journalSink{}
	result := runAsync(t, `id: a
hosts: all
tasks:
  - name: long
    enginetest.slow:
      id: long
      ms: 300
    async: 30
    poll: 0
    register: job
  - name: collect, or not
    async_status:
      jid: "{{ result.job.ansible_job_id }}"
    when_cel: 1 == 2
`, sink)
	if result.HasErrors() {
		t.Fatalf("run failed: %+v", result.Nodes)
	}
	if got := log.joined(); got != "long+,long-" {
		t.Errorf("marks %q: Run returned before the job ended", got)
	}
	if e, ok := byNode(sink.flat())["tasks[0]|d1"]; !ok || e.Outcome != engine.OutcomeChanged {
		t.Errorf("the drained job is not journaled as its change: %+v (%v)", e, ok)
	}
}

// TestAsync_AFailedJobFailsItsAsyncStatusWhichARescueAnswers.
func TestAsync_AFailedJobFailsItsAsyncStatusWhichARescueAnswers(t *testing.T) {
	registerSlow(t)
	result := runAsync(t, `id: a
hosts: all
tasks:
  - name: guarded
    block:
      - name: long
        enginetest.slow:
          id: long
          ms: 50
          fail: true
        async: 30
        poll: 0
        register: job
      - name: collect
        async_status:
          jid: "{{ result.job.ansible_job_id }}"
    rescue:
      - name: recover
        noop: {}
`, &journalSink{})
	if result.HasErrors() {
		t.Fatalf("a rescued job failure failed the run: %+v", result.Nodes)
	}
	var collectErr error
	for _, nr := range result.Nodes {
		if nr.NodeID == "tasks[0].block[1]" {
			collectErr = nr.Err
		}
	}
	if collectErr == nil || !strings.Contains(collectErr.Error(), "the background task") {
		t.Errorf("async_status error %v, want it to carry the job's failure", collectErr)
	}
}

// TestAsync_RefusesWhatCannotMeanWhatItSays: each refusal names why.
func TestAsync_RefusesWhatCannotMeanWhatItSays(t *testing.T) {
	collect := "  - name: c\n    async_status:\n      jid: \"{{ result.j.ansible_job_id }}\"\n"
	for name, tc := range map[string]struct{ yaml, want string }{
		"poll without async":                 {"id: x\ntasks:\n  - name: t\n    noop: {}\n    poll: 5\n", "poll needs async"},
		"async on a block":                   {"id: x\ntasks:\n  - name: b\n    async: 5\n    block:\n      - name: t\n        noop: {}\n", "not on a block"},
		"async on set_fact":                  {"id: x\ntasks:\n  - name: t\n    set_fact:\n      a: 1\n    async: 5\n", "runs in the engine at once"},
		"poll past async":                    {"id: x\ntasks:\n  - name: t\n    noop: {}\n    async: 5\n    poll: 10\n", "longer than the async limit"},
		"async with a loop":                  {"id: x\ntasks:\n  - name: t\n    noop: {}\n    async: 5\n    loop: [1, 2]\n", "loop or a retry"},
		"background, no register":            {"id: x\ntasks:\n  - name: t\n    noop: {}\n    async: 5\n    poll: 0\n" + collect, "needs a register"},
		"background, never collected":        {"id: x\ntasks:\n  - name: t\n    noop: {}\n    async: 5\n    poll: 0\n    register: j\n", "no later async_status collects it"},
		"async past a day":                   {"id: x\ntasks:\n  - name: t\n    noop: {}\n    async: 90000\n", "from 1 to 86400"},
		"background in a self-undoing block": {"id: x\ntasks:\n  - name: b\n    rollback_on_failure: true\n    block:\n      - name: t\n        noop: {}\n        async: 5\n        poll: 0\n        register: j\n" + collect, "inside block \"b\", which has rollback_on_failure"},
	} {
		if _, err := buildYAML(t, tc.yaml); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %v, want one saying %q", name, err, tc.want)
		}
	}
	if _, err := buildYAML(t, "id: x\ntasks:\n  - name: t\n    noop: {}\n    async: 5\n    poll: 0\n    register: j\n"+collect); err != nil {
		t.Errorf("a collected background task was refused: %v", err)
	}
	// A rescue runs after its block's undo, so a background task there is
	// not one the undo would miss.
	if _, err := buildYAML(t, "id: x\ntasks:\n  - name: b\n    rollback_on_failure: true\n    block:\n      - name: a\n        noop: {}\n    rescue:\n      - name: t\n        noop: {}\n        async: 5\n        poll: 0\n        register: j\n"+collect); err != nil {
		t.Errorf("a background task in a self-undoing block's rescue was refused: %v", err)
	}
}

// TestAsync_CancellingTheRunStopsABackgroundJob: a run cancelled while a
// background job goes on stops the job through its context, rather than
// waiting in Run's drain for the job's whole async limit.
func TestAsync_CancellingTheRunStopsABackgroundJob(t *testing.T) {
	log := registerSlow(t)
	device := &inventorytest.Stub{StubID: "d1", StubName: "d1", StubState: inventory.StateActive}
	x := engine.NewExecutor(mapResolver{"all": {device}}, newBridge(&recordingFallback{}), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithRenderer(render.New()), engine.WithJournal(&journalSink{}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		// Cancel once the job has started; the run then sits in its drain.
		for deadline := time.Now().Add(10 * time.Second); !strings.Contains(log.joined(), "long+") && time.Now().Before(deadline); {
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	began := time.Now()
	_, _ = x.Run(ctx, yamlRunbook(t, `id: a
hosts: all
tasks:
  - name: long
    enginetest.slow:
      id: long
      ms: 60000
    async: 3600
    poll: 0
    register: job
  - name: collect, or not
    async_status:
      jid: "{{ result.job.ansible_job_id }}"
    when_cel: 1 == 2
`))
	if took := time.Since(began); took > 10*time.Second {
		t.Fatalf("Run took %s after the cancel: the background job ran on", took)
	}
	if got := log.joined(); got != "long+,long!" {
		t.Errorf("marks %q, want long+,long! (the job stopped by the run's cancel)", got)
	}
}
