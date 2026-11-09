// The Walk tier's Release Gate for the Collections a dispatch names (Phase
// 136b): the Controller's real Worker fills each dispatch with the identity
// of every first-party group its runbook runs; real NATS carries it; and a
// real Runner compares it with its own build, warns where the two differ,
// and runs the job either way.
package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/native"
	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// collectionsGateRunbook pings the device, a method of the net group.
const collectionsGateRunbook = `# Pings the device over SSH. Read only; nothing to undo.
id: identity-walk
tasks:
  - name: ping
    net.ssh.ping: {}
`

// editingBus is the Controller's bus with one change: each dispatch it
// publishes is first handed to edit, which stands for a Controller of
// another build, and recorded as it was sent. Everything else reaches the
// real bus as the Worker wrote it.
type editingBus struct {
	event.Bus
	mu   sync.Mutex
	edit func(*wire.DispatchPayload)
	sent []wire.DispatchPayload
}

// Publish edits and records a dispatch, then publishes it.
func (b *editingBus) Publish(ctx context.Context, subject string, evt event.Event) error {
	if evt.Type != "runbook.dispatched" {
		return b.Bus.Publish(ctx, subject, evt)
	}
	var payload wire.DispatchPayload
	if err := json.Unmarshal(evt.Data, &payload); err != nil {
		return err
	}
	b.mu.Lock()
	if b.edit != nil {
		b.edit(&payload)
	}
	b.sent = append(b.sent, payload)
	b.mu.Unlock()
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	evt.Data = data
	return b.Bus.Publish(ctx, subject, evt)
}

// lockedBuffer is a log destination two goroutines may write and read.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p.
func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// linesFor returns the messages of the JSON log lines naming job.
func (l *lockedBuffer) linesFor(job string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, raw := range bytes.Split(l.buf.Bytes(), []byte("\n")) {
		var line struct {
			Msg string `json:"msg"`
			Job string `json:"job"`
		}
		if json.Unmarshal(raw, &line) == nil && line.Job == job {
			out = append(out, line.Msg)
		}
	}
	return out
}

// TestCollectionsReleaseGate_TheRunnerComparesWhatTheControllerNamed runs
// three jobs through the real Controller Worker, NATS and a real Runner
// whose log is read back: as the Worker built it (one build on both sides:
// the dispatch names the net group at this build's identity, and the
// Runner logs no line about it); with the group's digest edited (the same
// version with other declarations, warned as a stale catalog or a corrupt
// build); and with the digest and version edited (another build, warned as
// other declarations). Each warning also reaches the job's own log as a
// task.warning event, read back off the real stream. Every job runs to a
// succeeded result.
func TestCollectionsReleaseGate_TheRunnerComparesWhatTheControllerNamed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	files := map[string]string{"identity-walk.yaml": collectionsGateRunbook}
	h := newReleaseGateHarnessBare(t, knownHostsInEnvironment, files)
	ctx := context.Background()

	// The Runner, over its own runbook copy, logging where the gate reads.
	runnerLog := &lockedBuffer{}
	runnerDir := t.TempDir()
	writeRunbooks(t, runnerDir, files)
	runnerRunbooks, err := runbook.NewDirSource(runnerDir)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := native.NewAdapter(h.bus, runnerRunbooks, slog.New(slog.NewJSONHandler(runnerLog, &slog.HandlerOptions{Level: slog.LevelWarn})),
		native.WithRenderer(render.New()), native.WithRunnerID(gateRunnerID))
	if err != nil {
		t.Fatal(err)
	}
	agent := runner.NewAgent(h.dispatches, adapter, h.js, lock.NewInProcessManager(), topology.MaxDeliverDefault, nil, nil, runner.WithResultReporting(h.bus))
	agentCtx, cancelAgent := context.WithCancel(ctx)
	t.Cleanup(cancelAgent)
	go func() { _ = agent.Run(agentCtx) }()

	// The Controller: the real ent store, the fan-out Worker over the
	// editing bus, and the result consumer.
	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: "sqlite://" + t.TempDir() + "/controller.db"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	store := dispatch.NewEntJobStore(client)
	device := &sshGateDevice{Stub: &inventorytest.Stub{StubID: "identity-walk-1", StubName: "identity-walk-1", StubState: pkginventory.StateActive,
		Caps: []capability.Name{capability.NameSSHTransport}, Props: map[string]pkginventory.PropertyValue{"host": h.sshHost}}, port: h.sshPort}
	controllerDir := t.TempDir()
	writeRunbooks(t, controllerDir, files)
	controllerRunbooks, err := runbook.NewDirSource(controllerDir)
	if err != nil {
		t.Fatal(err)
	}
	bus := &editingBus{Bus: h.bus}
	worker := dispatch.NewWorker(store, &gateRepository{devices: []pkginventory.InventoryItem{device}}, controllerRunbooks, bus, gateCredentials{})
	subCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	if err := dispatch.NewResultConsumer(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Subscribe(subCtx, h.bus); err != nil {
		t.Fatal(err)
	}

	wantNet, err := collection.BuiltinIdentity("net.ssh.ping", buildinfo.Version())
	if err != nil {
		t.Fatal(err)
	}
	otherDigest := wire.ManifestDigestPrefix + strings.Repeat("9", 64)
	for _, tc := range []struct {
		name string
		edit func(*wire.DispatchPayload)
		// warn is a part of the one line the Runner logs, or empty for none.
		warn string
	}{
		{name: "one build"},
		{name: "an edited digest", warn: "stale module catalog or a corrupt build", edit: func(p *wire.DispatchPayload) {
			id := p.Collections["net"]
			id.Manifests = otherDigest
			p.Collections = map[string]wire.CollectionIdentity{"net": id}
		}},
		{name: "another build", warn: "planned against other declarations", edit: func(p *wire.DispatchPayload) {
			p.Collections = map[string]wire.CollectionIdentity{"net": {Group: "net", Version: "0.3.9", Manifests: otherDigest}}
		}},
	} {
		bus.mu.Lock()
		bus.edit, bus.sent = tc.edit, nil
		bus.mu.Unlock()
		job := &dispatch.Job{RunbookID: "identity-walk", GroupName: "gate", Actor: "release-gate", Kind: "runbook"}
		if err := store.Create(ctx, job); err != nil {
			t.Fatal(err)
		}
		evt, err := event.WrapPayload(uuid.New().String(), "job.requested", map[string]string{"job_id": job.JobID})
		if err != nil {
			t.Fatal(err)
		}
		if err := worker.HandleJobRequested(*evt); err != nil {
			t.Fatal(err)
		}
		if !jobReaches(store, job.JobID, "completed", 2*time.Minute) {
			t.Fatalf("%s: job never completed; devices:\n%s\nRunner events:\n%s", tc.name, jobRows(store, job.JobID), jobEvents(t, h, job.JobID))
		}
		_, tasks, err := store.Get(ctx, job.JobID)
		if err != nil {
			t.Fatal(err)
		}
		if len(tasks) != 1 || tasks[0].Result != dispatch.ResultSucceeded {
			t.Errorf("%s: devices %s, want the one device's run succeeded whatever the comparison found", tc.name, jobRows(store, job.JobID))
		}

		bus.mu.Lock()
		sent := append([]wire.DispatchPayload(nil), bus.sent...)
		bus.mu.Unlock()
		if len(sent) != 1 {
			t.Fatalf("%s: %d dispatches sent, want 1", tc.name, len(sent))
		}
		if tc.edit == nil && (len(sent[0].Collections) != 1 || sent[0].Collections["net"] != wantNet) {
			t.Errorf("%s: the Controller named %+v, want the net group at this build, %+v", tc.name, sent[0].Collections, wantNet)
		}

		lines := runnerLog.linesFor(job.JobID)
		switch {
		case tc.warn == "" && len(lines) != 0:
			t.Errorf("%s: the Runner logged %q, want no line about the dispatch's Collections", tc.name, lines)
		case tc.warn != "" && (len(lines) != 1 || !strings.Contains(lines[0], tc.warn)):
			t.Errorf("%s: the Runner logged %q, want one line saying %q", tc.name, lines, tc.warn)
		}

		// And the same warning reaches the job's own log on the real
		// stream, as a task.warning event, which is where an operator reads
		// it; one build puts none there.
		var warnings []string
		for _, line := range strings.Split(jobEvents(t, h, job.JobID), "\n") {
			if strings.HasPrefix(line, "task.warning ") {
				warnings = append(warnings, line)
			}
		}
		switch {
		case tc.warn == "" && len(warnings) != 0:
			t.Errorf("%s: the job's log carries %q, want no warning", tc.name, warnings)
		case tc.warn != "" && (len(warnings) != 1 || !strings.Contains(warnings[0], "WARNING: ") || !strings.Contains(warnings[0], tc.warn) || !strings.Contains(warnings[0], "net")):
			t.Errorf("%s: the job's log carries %q, want one task.warning saying %q of the net group", tc.name, warnings, tc.warn)
		}
	}
}
