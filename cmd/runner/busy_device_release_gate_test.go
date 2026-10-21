// Package main_test: Phase 103a's release gate. Two jobs on one device,
// and a run longer than the ack wait, through the real Agent, the real
// native adapter, real NATS with its real lock bucket, and a real sshd.
package main_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/native"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// busyGateRunbooks hold the device for a set time, or ping it.
var busyGateRunbooks = map[string]string{
	"hold-3.yaml":  holdRunbook("hold-3", 3),
	"hold-25.yaml": holdRunbook("hold-25", 25),
	"hold-75.yaml": holdRunbook("hold-75", 75),
	"ping.yaml":    "# Reaches the device over SSH and changes nothing.\nid: ping\ntasks:\n  - name: ping\n    net.ssh.ping:\n",
}

// holdRunbook runs sleep for seconds on the device.
func holdRunbook(id string, seconds int) string {
	return "# Holds the device for " + strconv.Itoa(seconds) + " s with sleep; changes nothing.\nid: " + id +
		"\ntasks:\n  - name: hold the device\n    exec.command:\n      argv: [\"sleep\", \"" + strconv.Itoa(seconds) + "\"]\n"
}

// busyGate is a Runner whose device lease is taken in the real NATS lock
// bucket, beside the harness's sshd and bus.
type busyGate struct {
	*releaseGateHarness
	consumer jetstream.Consumer
}

// newBusyGate starts sshd, NATS, the dispatch consumer and one Agent with
// opts over the native adapter and the NATS lock manager.
func newBusyGate(t *testing.T, opts ...runner.AgentOption) *busyGate {
	t.Helper()
	if testing.Short() {
		t.Skip("the busy-device release gate starts sshd and NATS")
	}
	ctx := context.Background()
	sshd, sshHost, sshPort := startSSHContainer(t)
	writeKnownHostsFor(t, net.JoinHostPort(sshHost, strconv.Itoa(sshPort)), knownHostsInHomeDir)
	url := testsupport.StartNATS(t).URL()

	bus, err := event.NewNatsBus(ctx, url, nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
	if err != nil {
		t.Fatalf("nats bus: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName, topology.DispatchConsumerConfig())
	if err != nil {
		t.Fatal(err)
	}
	locks, err := lock.NewNatsLockManager(ctx, url, nil, topology.StreamProvisioner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = locks.Close() })

	dir := t.TempDir()
	for name, content := range busyGateRunbooks {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil { // #nosec G306 -- a test runbook fixture, not secret material
			t.Fatal(err)
		}
	}
	runbooks, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := native.NewAdapter(bus, runbooks, nil)
	if err != nil {
		t.Fatal(err)
	}
	agent := runner.NewAgent(consumer, adapter, js, locks, topology.MaxDeliverDefault, nil, nil,
		append([]runner.AgentOption{runner.WithResultReporting(bus)}, opts...)...)
	agentCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go func() { _ = agent.Run(agentCtx) }()

	return &busyGate{
		releaseGateHarness: &releaseGateHarness{sshd: sshd, sshHost: sshHost, sshPort: sshPort, bus: bus, js: js, adapter: adapter},
		consumer:           consumer,
	}
}

// send dispatches runbookID to the gate's one device as a new job, the way
// internal/dispatch does, and returns the job's id and its results.
func (g *busyGate) send(t *testing.T, runbookID string) (string, <-chan runner.ResultEntry) {
	t.Helper()
	payload := wire.DispatchPayload{
		JobID:         uuid.New().String(),
		RunbookID:     runbookID,
		DeviceID:      "busy-gate-device",
		DeviceName:    "busy-gate-device",
		DeviceHost:    g.sshHost,
		SSHPort:       g.sshPort,
		Interruptible: true,
		Capabilities:  []capability.Name{capability.NameSSHTransport, capability.NameCommandExec},
		Secrets:       credential.Flatten(credential.Credential{Username: releaseGateSSHUser, Password: releaseGateSSHPassword}),
	}
	results := g.results(t, payload.JobID)
	evt, err := event.WrapPayload(uuid.New().String(), "runbook.dispatched", payload)
	if err != nil {
		t.Fatal(err)
	}
	pubCtx := event.WithIdempotencyKey(context.Background(), topology.DispatchIdentity(payload.JobID, payload.DeviceID))
	if err := g.bus.Publish(pubCtx, topology.DispatchSubject(payload.DeviceID), *evt); err != nil {
		t.Fatal(err)
	}
	return payload.JobID, results
}

// result waits up to within for one reported result.
func result(t *testing.T, results <-chan runner.ResultEntry, within time.Duration, what string) runner.ResultEntry {
	t.Helper()
	select {
	case r := <-results:
		return r
	case <-time.After(within):
		t.Fatalf("%s reported no result within %v", what, within)
		return runner.ResultEntry{}
	}
}

// TestBusyDeviceReleaseGate_TheSecondJobEndsBusyNamingTheFirst: a job
// whose device another job holds for longer than its wait ends failed,
// naming the holder, rather than staying running forever (FAILURE_PATTERNS
// 396 and 496), and the holder's own result is untouched. The schedule is
// shortened (1 s waits) so the gate takes seconds, not five minutes.
func TestBusyDeviceReleaseGate_TheSecondJobEndsBusyNamingTheFirst(t *testing.T) {
	g := newBusyGate(t, runner.WithContentionSchedule(500*time.Millisecond, time.Second))
	holder, holderResults := g.send(t, "hold-25")
	time.Sleep(3 * time.Second) // the holder takes the lease and starts its sleep
	_, waiterResults := g.send(t, "ping")

	busy := result(t, waiterResults, 20*time.Second, "the job waiting on a busy device")
	if busy.Outcome != "failed" || busy.Reason != "device busy: held by job "+holder {
		t.Fatalf("the waiting job reported %q, %q; want failed, device busy: held by job %s", busy.Outcome, busy.Reason, holder)
	}
	if done := result(t, holderResults, 60*time.Second, "the holding job"); done.Outcome != "completed" {
		t.Errorf("the holding job reported %q, %q; want completed", done.Outcome, done.Reason)
	}
}

// TestBusyDeviceReleaseGate_TheSecondJobRunsWhenTheFirstEndsInTime: a job
// whose device is held for less than its wait runs once the holder ends.
func TestBusyDeviceReleaseGate_TheSecondJobRunsWhenTheFirstEndsInTime(t *testing.T) {
	g := newBusyGate(t, runner.WithContentionSchedule(2*time.Second, 4*time.Second))
	_, holderResults := g.send(t, "hold-3")
	time.Sleep(time.Second)
	_, waiterResults := g.send(t, "ping")

	if r := result(t, waiterResults, 40*time.Second, "the waiting job"); r.Outcome != "completed" {
		t.Errorf("the waiting job reported %q, %q; want completed once the device was free", r.Outcome, r.Reason)
	}
	if r := result(t, holderResults, 40*time.Second, "the holding job"); r.Outcome != "completed" {
		t.Errorf("the holding job reported %q, %q", r.Outcome, r.Reason)
	}
}

// TestBusyDeviceReleaseGate_ARunLongerThanTheAckWaitIsDeliveredOnce: a run
// of 75 s under the real 30 s ack wait is delivered once and reports once,
// because the Runner reports it in progress while it runs. Before, it was
// delivered again every 30 s while it ran.
func TestBusyDeviceReleaseGate_ARunLongerThanTheAckWaitIsDeliveredOnce(t *testing.T) {
	g := newBusyGate(t)
	_, results := g.send(t, "hold-75")
	if r := result(t, results, 3*time.Minute, "the 75 s run"); r.Outcome != "completed" {
		t.Fatalf("the 75 s run reported %q, %q", r.Outcome, r.Reason)
	}
	select {
	case r := <-results:
		t.Fatalf("the run reported a second result: %+v", r)
	case <-time.After(2 * time.Second):
	}
	info, err := g.consumer.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Delivered.Consumer != 1 || info.NumRedelivered != 0 {
		t.Errorf("the dispatch was delivered %d times (%d redelivered), want once", info.Delivered.Consumer, info.NumRedelivered)
	}
}
