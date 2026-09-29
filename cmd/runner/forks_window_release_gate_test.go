// The Release Gate for the forks window (Phase 110): a job launched with
// forks set never has more than forks of its devices running at once, and
// every one of its devices still runs.
//
// Everything on the path is real: the Controller's fan-out Worker over the
// real ent store (SQLite through the real migrations), real NATS JetStream,
// a real Runner Agent executing the dispatch in a real per-task child
// against a real sshd, the Runner's real result publish, and the
// Controller's real ResultConsumer pumping the next device as each result
// lands. What the gate measures is measured on the device, not by this
// code: each task records how many tasks were in flight on the device when
// it began, so a window that let a third device start would leave a 3 in
// the device's own file.
//
// The unwindowed job beside it is the control. With no forks it must reach
// more than two at once on the same devices, or a pass would mean nothing:
// the Runner's own pool, or the device, could be what kept the windowed job
// at two.
package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

// overlapRunbook records, in /tmp/overlap-<tag> on the device, how many
// tasks were in flight when this one began, then holds its place for three
// seconds so neighbours overlap it if anything lets them.
func overlapRunbook(tag string) string {
	cmd := fmt.Sprintf("mkdir -p /tmp/inflight-%[1]s && touch /tmp/inflight-%[1]s/$$ && ls /tmp/inflight-%[1]s | wc -l >> /tmp/overlap-%[1]s && sleep 3 && rm /tmp/inflight-%[1]s/$$", tag)
	return fmt.Sprintf("id: overlap-%s\ntasks:\n  - name: hold a place\n    exec.shell:\n      cmd: %q\n", tag, cmd)
}

// sshGateDevice is a device the Worker can dispatch to the harness's sshd:
// a stub carrying its address, plus the SSH port accessor a real SSH
// device type has.
type sshGateDevice struct {
	*inventorytest.Stub
	port int
}

// SSHHost returns the device's address.
func (d *sshGateDevice) SSHHost() string { h, _ := d.Properties().String("host"); return h }

// SSHPort returns the sshd container's mapped port.
func (d *sshGateDevice) SSHPort() int { return d.port }

// gateRepository is an inventory of the gate's devices, answering the
// fan-out's group walk and the pump's read by name.
type gateRepository struct{ devices []pkginventory.InventoryItem }

// GetGroup streams every device, whatever the selector: the gate's job
// targets them all.
func (r *gateRepository) GetGroup(context.Context, pkginventory.Selector) (inventory.Iterator, error) {
	return &gateIterator{devices: r.devices}, nil
}

// GetByName returns the device named name.
func (r *gateRepository) GetByName(_ context.Context, name string) (pkginventory.InventoryItem, error) {
	for _, d := range r.devices {
		if d.Name() == name {
			return d, nil
		}
	}
	return nil, inventory.ErrItemNotFound
}

// Create is not used by the gate.
func (r *gateRepository) Create(context.Context, pkginventory.InventoryItem) error { return nil }

// Save is not used by the gate.
func (r *gateRepository) Save(context.Context, pkginventory.InventoryItem) error { return nil }

// Retire is not used by the gate.
func (r *gateRepository) Retire(context.Context, string) error { return nil }

// GroupAncestry reports no hierarchy, so every device takes the defaults.
func (r *gateRepository) GroupAncestry(context.Context, string) ([]inventory.HierarchyLayer, error) {
	return nil, nil
}

// gateIterator streams devices in order.
type gateIterator struct {
	devices []pkginventory.InventoryItem
	index   int
}

// Next advances to the next device.
func (i *gateIterator) Next(context.Context) bool { i.index++; return i.index <= len(i.devices) }

// Item returns the current device.
func (i *gateIterator) Item() pkginventory.InventoryItem { return i.devices[i.index-1] }

// Error reports no iteration failure.
func (i *gateIterator) Error() error { return nil }

// Close releases nothing.
func (i *gateIterator) Close() error { return nil }

// gateCredentials hands every device the sshd container's login.
type gateCredentials struct{}

// Lookup returns the release gate's SSH login for any device.
func (gateCredentials) Lookup(context.Context, string) (credential.Credential, error) {
	return credential.Credential{Username: releaseGateSSHUser, Password: releaseGateSSHPassword}, nil
}

func TestForksWindowReleaseGate_NeverMoreThanForksAtOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	const devices = 5
	files := map[string]string{"overlap-window.yaml": overlapRunbook("window"), "overlap-open.yaml": overlapRunbook("open")}
	h := newReleaseGateHarnessFor(t, knownHostsInEnvironment, files)
	ctx := context.Background()

	// The Controller's half: the real ent store through its real
	// migrations, the fan-out Worker, and the result consumer pumping the
	// window, subscribed to the same real NATS the Runner reports on.
	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: "sqlite://" + t.TempDir() + "/controller.db"})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	store := dispatch.NewEntJobStore(client)

	repo := &gateRepository{}
	for i := 0; i < devices; i++ {
		repo.devices = append(repo.devices, &sshGateDevice{
			Stub: &inventorytest.Stub{
				StubID:    pkginventory.DeviceID(fmt.Sprintf("forks-gate-%d", i)),
				StubName:  fmt.Sprintf("forks-gate-%d", i),
				StubState: pkginventory.StateActive,
				Caps:      []capability.Name{capability.NameSSHTransport, capability.NameShellExec},
				Props:     map[string]pkginventory.PropertyValue{"host": h.sshHost},
			},
			port: h.sshPort,
		})
	}
	runbookDir := t.TempDir()
	writeRunbooks(t, runbookDir, files)
	runbooks, err := runbook.NewDirSource(runbookDir)
	if err != nil {
		t.Fatalf("runbook source: %v", err)
	}
	worker := dispatch.NewWorker(store, repo, runbooks, h.bus, gateCredentials{})
	consumer := dispatch.NewResultConsumer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), dispatch.WithResultPump(worker.Pump))
	subCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	if err := consumer.Subscribe(subCtx, h.bus); err != nil {
		t.Fatalf("subscribe the result consumer: %v", err)
	}

	for _, tc := range []struct {
		tag   string
		forks int
	}{{"window", 2}, {"open", 0}} {
		fields := launch.Fields{}
		if tc.forks > 0 {
			fields[dispatch.ForksField] = tc.forks
		}
		job := &dispatch.Job{RunbookID: "overlap-" + tc.tag, GroupName: "gate", Actor: "release-gate", Fields: fields}
		if err := store.Create(ctx, job); err != nil {
			t.Fatalf("%s: Create: %v", tc.tag, err)
		}
		evt, err := event.WrapPayload(uuid.New().String(), "job.requested", map[string]string{"job_id": job.JobID})
		if err != nil {
			t.Fatalf("%s: wrap: %v", tc.tag, err)
		}
		if err := worker.HandleJobRequested(*evt); err != nil {
			t.Fatalf("%s: HandleJobRequested: %v", tc.tag, err)
		}
		if !jobReaches(store, job.JobID, "completed", 2*time.Minute) {
			t.Fatalf("%s: job never completed; devices:\n%s\nRunner events:\n%s", tc.tag, jobRows(store, job.JobID), jobEvents(t, h, job.JobID))
		}

		counts := overlapCounts(t, h, tc.tag)
		if len(counts) != devices {
			t.Fatalf("%s: the device recorded %d tasks, want one per device (%d): %v", tc.tag, len(counts), devices, counts)
		}
		peak := 0
		for _, n := range counts {
			peak = max(peak, n)
		}
		t.Logf("%s (forks %d): in flight as each task began %v, peak %d", tc.tag, tc.forks, counts, peak)
		switch {
		case tc.forks > 0 && peak > tc.forks:
			t.Errorf("%s: %d tasks ran at once on the device, want at most forks (%d): %v", tc.tag, peak, tc.forks, counts)
		case tc.forks > 0 && peak < tc.forks:
			t.Errorf("%s: at most %d tasks ran at once, want the window used in full (%d): %v", tc.tag, peak, tc.forks, counts)
		case tc.forks == 0 && peak <= 2:
			// The control: with no window, the same five devices must
			// overlap past two, or the windowed pass proved nothing.
			t.Errorf("%s: with no forks at most %d tasks ran at once; the gate cannot tell a window from the Runner's own limits: %v", tc.tag, peak, counts)
		}
		got, _, err := store.Get(ctx, job.JobID)
		if err != nil {
			t.Fatalf("%s: Get: %v", tc.tag, err)
		}
		if got.DispatchedCount != devices {
			t.Errorf("%s: job counted %d dispatched, want %d", tc.tag, got.DispatchedCount, devices)
		}
	}
}

// writeRunbooks writes files into dir.
func writeRunbooks(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil { // #nosec G306 -- a test runbook fixture, not secret material
			t.Fatalf("write runbook %s: %v", name, err)
		}
	}
}

// jobReaches polls the store until the job reads want, reporting whether
// it did before the deadline.
func jobReaches(store dispatch.JobStore, jobID, want string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if job, err := store.Lookup(context.Background(), jobID); err == nil && job.State == want {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// jobRows says where each of the job's devices got to, for a failure.
func jobRows(store dispatch.JobStore, jobID string) string {
	_, tasks, err := store.Get(context.Background(), jobID)
	if err != nil {
		return err.Error()
	}
	var rows []string
	for _, task := range tasks {
		rows = append(rows, fmt.Sprintf("%s %s result=%q %q", task.DeviceID, task.Outcome, task.Result, task.Reason+task.ResultReason))
	}
	return strings.Join(rows, "\n")
}

// jobEvents reads the job's own log events off the stream, which is what
// the Runner said about each device, for a failure.
func jobEvents(t *testing.T, h *releaseGateHarness, jobID string) string {
	t.Helper()
	ctx := context.Background()
	consumer, err := h.js.CreateOrUpdateConsumer(ctx, topology.StreamName, topology.LogViewerConsumerConfig(jobID))
	if err != nil {
		return err.Error()
	}
	msgs, err := consumer.Fetch(100, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		return err.Error()
	}
	var lines []string
	for msg := range msgs.Messages() {
		var wrapped event.Event
		if json.Unmarshal(msg.Data(), &wrapped) != nil {
			continue
		}
		var evt wire.JobEvent
		if json.Unmarshal(wrapped.Data, &evt) != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s %s %s", evt.Task, evt.Status, evt.EventData.Message))
	}
	return strings.Join(lines, "\n")
}

// overlapCounts reads, from the device itself, how many tasks were in
// flight as each task of the tag's job began.
func overlapCounts(t *testing.T, h *releaseGateHarness, tag string) []int {
	t.Helper()
	code, r, err := h.sshd.Exec(context.Background(), []string{"cat", "/tmp/overlap-" + tag}, tcexec.Multiplexed())
	if err != nil || code != 0 {
		t.Fatalf("reading /tmp/overlap-%s on the device: exit %d, %v", tag, code, err)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	var counts []int
	for _, field := range strings.Fields(string(raw)) {
		n, err := strconv.Atoi(field)
		if err != nil {
			t.Fatalf("the device's /tmp/overlap-%s holds %q, which is not a count", tag, field)
		}
		counts = append(counts, n)
	}
	return counts
}
