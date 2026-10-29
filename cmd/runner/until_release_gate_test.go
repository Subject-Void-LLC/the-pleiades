// The Walk tier's Release Gate for retries (Phase 88): a Controller job runs
// a task with until, retries and delay through a real Runner on a real sshd.
// The task's own result satisfies until only on its second try, each try is
// journaled as its own attempt in the Controller's database, only the last
// try is registered, and the job completes.
package main_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/google/uuid"
)

// walkUntilRunbook's first task answers "waiting" the first time it runs on
// the device (leaving a marker behind) and "ready" every time after, so its
// until holds on the second try and never on the first. The second task
// writes what the first registered, which proves only the last try was.
const walkUntilRunbook = `id: until-walk
tasks:
  - name: wait until the device says ready
    exec.shell:
      cmd: "if [ -e /tmp/until-walk/seen ]; then echo ready; else mkdir -p /tmp/until-walk && touch /tmp/until-walk/seen && echo waiting; fi"
    register: probe
    until: ["result.probe.stdout == 'ready'"]
    retries: 3
    delay: 1
  - name: keep what was registered
    file.copy:
      dest: /tmp/until-walk/registered
      content: "{{ result.probe.stdout }}\n"
`

func TestUntilReleaseGate_RetriesOnARunnerUntilItsOwnResultHolds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	files := map[string]string{"until-walk.yaml": walkUntilRunbook}
	h := newReleaseGateHarnessFor(t, knownHostsInEnvironment, files)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: "sqlite://" + t.TempDir() + "/controller.db"})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	store := dispatch.NewEntJobStore(client)
	journalStore := journal.NewEntStore(client)

	device := &sshGateDevice{
		Stub: &inventorytest.Stub{
			StubID: "until-walk-1", StubName: "until-walk-1", StubState: pkginventory.StateActive,
			Caps:  []capability.Name{capability.NameSSHTransport, capability.NameShellExec, capability.NamePOSIXFileSystem},
			Props: map[string]pkginventory.PropertyValue{"host": h.sshHost},
		},
		port: h.sshPort,
	}
	runbookDir := t.TempDir()
	writeRunbooks(t, runbookDir, files)
	runbooks, err := runbook.NewDirSource(runbookDir)
	if err != nil {
		t.Fatalf("runbook source: %v", err)
	}
	worker := dispatch.NewWorker(store, &gateRepository{devices: []pkginventory.InventoryItem{device}}, runbooks, h.bus, gateCredentials{})
	subCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	if err := dispatch.NewResultConsumer(store, quiet).Subscribe(subCtx, h.bus); err != nil {
		t.Fatalf("subscribe the result consumer: %v", err)
	}
	// Every dispatch in this test was sent, so the journal admits each entry.
	admission := journal.WithAdmission(func(ctx context.Context, jobID, deviceID string) (journal.Admission, error) {
		state, err := store.DispatchState(ctx, jobID, deviceID)
		switch {
		case err != nil:
			return journal.AdmitUnrecorded, err
		case state == dispatch.DispatchSent:
			return journal.AdmitDispatched, nil
		case state == dispatch.DispatchNotSent:
			return journal.AdmitNotDispatched, nil
		default:
			return journal.AdmitUnrecorded, nil
		}
	})
	if err := journal.NewSubscriber(journalStore, quiet, admission).Subscribe(subCtx, h.bus); err != nil {
		t.Fatalf("subscribe the journal consumer: %v", err)
	}

	h.onDevice(t, "rm -rf /tmp/until-walk")
	job := &dispatch.Job{RunbookID: "until-walk", GroupName: "gate", Actor: "release-gate", Kind: "runbook"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	evt, err := event.WrapPayload(uuid.New().String(), "job.requested", map[string]string{"job_id": job.JobID})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.HandleJobRequested(*evt); err != nil {
		t.Fatalf("HandleJobRequested: %v", err)
	}
	if !jobReaches(store, job.JobID, "completed", 2*time.Minute) {
		t.Fatalf("job never completed; devices:\n%s\nRunner events:\n%s", jobRows(store, job.JobID), jobEvents(t, h, job.JobID))
	}

	// The device's own row: the task held on its second try, so the Runner
	// reported the device succeeded rather than failed.
	_, tasks, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Result != "succeeded" {
		t.Fatalf("device rows:\n%s\nwant one, succeeded", jobRows(store, job.JobID))
	}

	// Two attempts and the task after them: three entries.
	var entries []string
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		got, err := journalStore.AllForJob(ctx, job.JobID)
		if err == nil && len(got) >= 3 {
			entries = entries[:0]
			for _, e := range got {
				entries = append(entries, e.NodeID+" "+string(e.Outcome)+" "+string(e.FailureStage))
			}
			break
		}
	}
	// Each try is its own attempt entry and neither failed: the first ran and
	// its until did not hold, which is a reason to try again, not a failure.
	want := []string{"tasks[0].attempt[0] changed ", "tasks[0].attempt[1] changed ", "tasks[1] changed "}
	if strings.Join(entries, "|") != strings.Join(want, "|") {
		t.Fatalf("journal entries %q, want %q", entries, want)
	}

	// The task after the retries read only the last try's registered result:
	// the first try's "waiting" was never registered.
	if got := h.onDevice(t, "cat /tmp/until-walk/registered"); got != "ready" {
		t.Errorf("the task after the retries read %q, want the last try's \"ready\"", got)
	}
}
