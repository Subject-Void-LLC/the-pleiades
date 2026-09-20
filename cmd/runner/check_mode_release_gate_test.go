// Package main_test: the Walk-tier Release Gate for check mode
// (IMPLEMENTATION.md Phase 46's Walk-tier build item).
package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// walkCheckPath is the directory the gate's runbook manages on the device.
const walkCheckPath = "/tmp/pleiades-walk-tier-check"

// TestCheckModeReleaseGate_TheWalkTierChecksAndChangesNothing proves the
// Walk tier's check mode against real NATS, the real Agent and native
// Adapter, and a real sshd, verified on the device over a connection this
// test opens itself.
//
// First the harness's own Agent runs alone, which is a Runner from before
// check mode: a check published to the check subject never reaches it,
// and the device is untouched. Then a check-capable pull loop joins, the
// waiting check is taken and checked, and still nothing changes, even for
// a payload claiming to be a real run. The control is the same runbook as
// a real run on the dispatch subject, which does create the directory.
func TestCheckModeReleaseGate_TheWalkTierChecksAndChangesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Walk-tier check mode Release Gate container test in short mode")
	}
	h := newReleaseGateHarnessFor(t, knownHostsInEnvironment, map[string]string{
		"dir.yaml": "id: dir\ntasks:\n  - name: make-a-directory\n    fqcn: file.directory\n    params:\n      path: " + walkCheckPath + "\n",
		// One task a check can answer and one it cannot (a command's effect
		// is unknowable without running it).
		"mixed.yaml": "id: mixed\ntasks:\n  - name: make-a-directory\n    fqcn: file.directory\n    params:\n      path: " + walkCheckPath + "\n" +
			"  - name: run-a-command\n    fqcn: exec.command\n    params:\n      cmd: \"touch " + walkCheckPath + "-ran\"\n",
	})
	addr := net.JoinHostPort(h.sshHost, strconv.Itoa(h.sshPort))
	absent := func() bool {
		return readOverSSH(t, addr, "if [ -e "+walkCheckPath+" ]; then echo present; else echo absent; fi") == "absent"
	}
	payload := func(mode string) wire.DispatchPayload {
		return wire.DispatchPayload{
			JobID: uuid.New().String(), RunbookID: "dir", Mode: mode,
			DeviceID: "release-gate-device", DeviceName: "release-gate-device",
			DeviceHost: h.sshHost, SSHPort: h.sshPort,
			Capabilities: []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem},
			Secrets:      credential.Flatten(credential.Credential{Username: releaseGateSSHUser, Password: releaseGateSSHPassword}),
		}
	}

	// A fleet from before check mode: the check waits, untaken.
	check := payload("check")
	watch := h.watchJob(t, check.JobID)
	h.publish(t, topology.CheckSubject(check.DeviceID), check)
	if evt, ok := watch.completed(t, 6*time.Second); ok {
		t.Fatalf("a Runner with only the dispatch loop took a check: %+v", evt)
	}
	if !absent() {
		t.Fatal("the device changed while the check waited")
	}

	// A check-capable Runner joins and takes the waiting check.
	h.startCheckAgent(t)
	evt, ok := watch.completed(t, 30*time.Second)
	if !ok {
		t.Fatal("the check-capable loop never took the waiting check")
	}
	if !strings.HasPrefix(evt.EventData.Message, "check:") || !strings.Contains(evt.EventData.Message, "nothing was changed") {
		t.Errorf("the completion does not say it was a check: %+v", evt)
	}
	if !absent() {
		t.Fatal("a check created the directory on the device")
	}

	// A payload on the check subject claiming to be a real run is checked.
	liar := payload("execute")
	watch = h.watchJob(t, liar.JobID)
	h.publish(t, topology.CheckSubject(liar.DeviceID), liar)
	if evt, ok := watch.completed(t, 30*time.Second); !ok || !strings.HasPrefix(evt.EventData.Message, "check:") {
		t.Fatalf("a payload on the check subject claiming execute = %+v (arrived %v), want it checked", evt, ok)
	}
	if !absent() {
		t.Fatal("a payload on the check subject changed the device")
	}

	// The control: the same runbook as a real run changes the device.
	real := payload("execute")
	watch = h.watchJob(t, real.JobID)
	h.publish(t, topology.DispatchSubject(real.DeviceID), real)
	if evt, ok := watch.completed(t, 30*time.Second); !ok || evt.Status != "changed" {
		t.Fatalf("the real run = %+v (arrived %v), want it to report a change", evt, ok)
	}
	if absent() {
		t.Fatal("the real run did not create the directory, so the check assertions above prove nothing")
	}

	// A check with a task it cannot answer finishes and reports how many,
	// on the result the Controller records, never as a clean check, and
	// the command it could not check did not run.
	mixed := payload("check")
	mixed.RunbookID = "mixed"
	mixed.Capabilities = append(mixed.Capabilities, capability.NameCommandExec)
	results := h.results(t, mixed.JobID)
	h.publish(t, topology.CheckSubject(mixed.DeviceID), mixed)
	select {
	case entry := <-results:
		if entry.Outcome != "completed" || entry.Unchecked != 1 || !strings.Contains(entry.Reason, "check incomplete: 1 task(s)") {
			t.Errorf("the incomplete check reported %+v, want completed with one unchecked task and a reason saying so", entry)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the incomplete check reported no result")
	}
	if got := readOverSSH(t, addr, "if [ -e "+walkCheckPath+"-ran ]; then echo ran; else echo absent; fi"); got != "absent" {
		t.Error("the command a check could not check ran on the device")
	}

	// A check journals nothing, and the real run's journal is the control
	// that proves this harness journals at all.
	for jobID, want := range map[string]bool{check.JobID: false, liar.JobID: false, real.JobID: true} {
		if got := h.journaled(t, jobID); got != want {
			t.Errorf("job %s journaled = %v, want %v", jobID, got, want)
		}
	}
}

// journaled reports whether anything was published on jobID's run journal
// subject, read from the stream itself rather than from a subscriber that
// might have joined late. Each journal batch is published before the
// device's completion event, so by the time a job has completed its
// journal is already in the stream.
func (h *releaseGateHarness) journaled(t *testing.T, jobID string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := h.js.Stream(ctx, topology.StreamName)
	if err != nil {
		t.Fatalf("binding the stream: %v", err)
	}
	_, err = stream.GetLastMsgForSubject(ctx, topology.JournalSubject(jobID))
	switch {
	case err == nil:
		return true
	case errors.Is(err, jetstream.ErrMsgNotFound):
		return false
	default:
		t.Fatalf("reading job %s's journal subject: %v", jobID, err)
		return false
	}
}

// results returns the results the Runner publishes for jobID, read the way
// the Controller's result consumer reads them.
func (h *releaseGateHarness) results(t *testing.T, jobID string) <-chan runner.ResultEntry {
	t.Helper()
	out := make(chan runner.ResultEntry, 4)
	err := h.bus.Subscribe(context.Background(), topology.ResultSubject(jobID), func(evt event.Event) error {
		var entry runner.ResultEntry
		if err := json.Unmarshal(evt.Data, &entry); err != nil {
			t.Errorf("decoding a result: %v", err)
			return nil
		}
		out <- entry
		return nil
	})
	if err != nil {
		t.Fatalf("subscribing to results: %v", err)
	}
	return out
}
