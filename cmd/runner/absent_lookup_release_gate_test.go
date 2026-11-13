// The Walk tier's Release Gate for absence as data (Phase 131, R4): a
// lookup of something not there answers exists false on a Runner as it does
// on the CLI, and that false survives the per-task child process, the
// Runner's register and the journal and result the Controller reads.
package main_test

import (
	"context"
	"encoding/json"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// walkAbsentDir is the directory the gate's runbook reads and writes on the
// device.
const walkAbsentDir = "/tmp/absent-walk"

// walkAbsentRunbook looks for a path that is not there and branches on the
// answer. Nothing here ignores an error: a lookup that failed would fail
// the job at its first task, and an exists dropped on the way (an omitempty
// false) would fail the third, whose condition could not be read. The
// second task is the branch a runbook took when an absent lookup failed.
const walkAbsentRunbook = `id: absent-walk
tasks:
  - name: look for what is not there
    file.stat:
      path: ` + walkAbsentDir + `/never-there
    register: probe
  - name: the branch a failed lookup took
    exec.shell:
      cmd: "echo took > ` + walkAbsentDir + `/failure-branch"
    when_cel: "!has(result.probe)"
  - name: the branch absence takes
    file.copy:
      dest: ` + walkAbsentDir + `/absent-branch
      content: "{{ result.probe.exists }}\n"
    when: ["result.probe.exists == false"]
`

// TestWalk_AnAbsentLookupKeepsExistsFalse runs walkAbsentRunbook on real
// NATS through the real Agent, native Adapter and per-task child process,
// against a real sshd. The run's job completes; the Controller's journal
// records the lookup ran (not failed), the failure branch skipped and the
// absence branch changed; and the device holds the false the Runner
// rendered from the registered exists. A check of the same runbook, on the
// check subject, completes with nothing unchecked, so the check's lookup
// also answered exists false and its failure branch, which a check cannot
// answer, was skipped.
func TestWalk_AnAbsentLookupKeepsExistsFalse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Walk-tier absence Release Gate container test in short mode")
	}
	h := newReleaseGateHarnessFor(t, knownHostsInEnvironment, map[string]string{"absent-walk.yaml": walkAbsentRunbook})
	// The directory is the login's, since onDevice runs as root and the
	// runbook's tasks run as the login.
	h.onDevice(t, "rm -rf "+walkAbsentDir+" && mkdir -p "+walkAbsentDir+" && chown 1000:1000 "+walkAbsentDir)
	payload := func(mode string) wire.DispatchPayload {
		return wire.DispatchPayload{
			JobID: uuid.New().String(), RunbookID: "absent-walk", Mode: mode,
			DeviceID: "release-gate-device", DeviceName: "release-gate-device",
			DeviceHost: h.sshHost, SSHPort: h.sshPort,
			Capabilities: []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem, capability.NameShellExec, capability.NameCommandExec},
			Secrets:      credential.Flatten(credential.Credential{Username: releaseGateSSHUser, Password: releaseGateSSHPassword}),
		}
	}

	run := payload("execute")
	entries := h.journalEntries(t, run.JobID)
	results := h.results(t, run.JobID)
	h.publish(t, topology.DispatchSubject(run.DeviceID), run)
	select {
	case entry := <-results:
		if entry.Outcome != "completed" {
			t.Errorf("the run reported %+v, want completed", entry)
		}
	case <-time.After(time.Minute):
		t.Fatal("the run reported no result")
	}
	want := map[string]engine.Outcome{"tasks[0]": engine.OutcomeRan, "tasks[1]": engine.OutcomeSkipped, "tasks[2]": engine.OutcomeChanged}
	if got := entries.wait(t, len(want)); !maps.Equal(got, want) {
		t.Errorf("the journal records %v, want %v", got, want)
	}
	if got := h.onDevice(t, "cat "+walkAbsentDir+"/absent-branch 2>/dev/null || echo missing"); got != "false" {
		t.Errorf("the absence branch wrote %q, want the registered false", got)
	}
	if got := h.onDevice(t, "test -e "+walkAbsentDir+"/failure-branch && echo exists || echo absent"); got != "absent" {
		t.Errorf("the branch a failed lookup took ran on the device: %q", got)
	}

	// The check: the same read, the same false, and the uncheckable failure
	// branch skipped rather than counted unchecked.
	h.startCheckAgent(t)
	check := payload("check")
	results = h.results(t, check.JobID)
	h.publish(t, topology.CheckSubject(check.DeviceID), check)
	select {
	case entry := <-results:
		if entry.Outcome != "completed" || entry.Unchecked != 0 {
			t.Errorf("the check reported %+v, want completed with nothing unchecked", entry)
		}
	case <-time.After(time.Minute):
		t.Fatal("the check reported no result")
	}
}

// journalWatch collects the journal entries one job publishes, as the
// Controller's journal subscriber decodes them.
type journalWatch struct {
	mu       sync.Mutex
	outcomes map[string]engine.Outcome
}

// journalEntries subscribes to jobID's journal subject before its dispatch
// is published, so no batch is missed.
func (h *releaseGateHarness) journalEntries(t *testing.T, jobID string) *journalWatch {
	t.Helper()
	w := &journalWatch{outcomes: map[string]engine.Outcome{}}
	err := h.bus.Subscribe(context.Background(), topology.JournalSubject(jobID), func(evt event.Event) error {
		var batch journal.Batch
		if err := json.Unmarshal(evt.Data, &batch); err != nil {
			t.Errorf("decoding a journal batch: %v", err)
			return nil
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		for _, e := range batch.Entries {
			w.outcomes[e.NodeID] = e.Outcome
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribing to the journal: %v", err)
	}
	return w
}

// wait returns the outcomes recorded once n nodes have one, or what arrived
// within thirty seconds.
func (w *journalWatch) wait(t *testing.T, n int) map[string]engine.Outcome {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		w.mu.Lock()
		got := maps.Clone(w.outcomes)
		w.mu.Unlock()
		if len(got) >= n || time.Now().After(deadline) {
			return got
		}
		time.Sleep(100 * time.Millisecond)
	}
}
