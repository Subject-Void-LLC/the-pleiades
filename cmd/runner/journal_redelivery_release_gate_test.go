// These are Phase 40's Walk-tier Release Gate: the run journal, proven
// against a real NATS broker, a real sshd container, a real Runner agent
// and the real Controller-side store, with the rows read back through a
// second database connection that bypasses ent entirely.
//
// The Crawl tier has its own gate (cmd/pleiades/journal_release_gate_test.go)
// and this is deliberately not a parameterized version of it. The two
// sinks share a port and nothing else: one appends JSON Lines to a file in
// a project directory, the other publishes onto a subject that a separate
// process consumes into SQL. Two sinks means two proofs.
//
// What is proven here that no unit test can reach: that a redelivered
// dispatch journals as a RETRY rather than as an unrelated second run,
// which is the whole reason JournalEntry carries an Attempt at all, and
// that the attempt number reaching the row is JetStream's own delivery
// counter rather than anything the run computed for itself.
package main_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

// journalRow is one row read back raw, holding only the columns these
// assertions are about.
type journalRow struct {
	JobID        string
	DeviceID     string
	Attempt      int
	NodeID       string
	RunID        string
	Outcome      string
	FailureStage string
	TaskName     string
	FQCN         string
}

// startJournalStore opens a real file-backed control-plane database,
// subscribes a real journal.Subscriber to the real journal subject on the
// harness's own bus, and returns the path so a test can read the rows
// back without going through ent.
//
// It subscribes AFTER the harness has built the agent, which is safe and
// deliberate: topology.SubscribeConsumerConfig asks for DeliverAllPolicy,
// so a consumer created late still receives every message the stream
// already holds. Relying on that is better than racing the agent to
// subscribe first, because the race has no losing branch that fails
// loudly.
func startJournalStore(t *testing.T, h *releaseGateHarness) string {
	t.Helper()
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "journal_release_gate.db")
	client, err := ent.OpenEmbedded(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open the control plane database: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	subscriber := journal.NewSubscriber(journal.NewEntStore(client), nil)
	if err := subscriber.Subscribe(ctx, h.bus); err != nil {
		t.Fatalf("failed to subscribe the journal store: %v", err)
	}
	return dbPath
}

// readJournalRaw reads every journal row for one job through a second
// connection opened directly against the database file.
//
// Going around ent is the point rather than a convenience. The claim
// under test is about what is STORED, and asking the same client that
// wrote the rows to read them back would prove only that the client is
// self-consistent. This is the shape internal/crypto's own at-rest gate
// uses for the same reason.
func readJournalRaw(t *testing.T, dbPath, jobID string) []journalRow {
	t.Helper()

	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open the database raw: %v", err)
	}
	defer func() { _ = rawDB.Close() }()

	rows, err := rawDB.QueryContext(context.Background(), `
		SELECT job_id, COALESCE(device_id, ''), attempt, node_id, run_id,
		       outcome, COALESCE(failure_stage, ''), COALESCE(task_name, ''), COALESCE(fqcn, '')
		FROM journal_entries
		WHERE job_id = ?
		ORDER BY attempt, sequence`, jobID)
	if err != nil {
		t.Fatalf("failed to query the journal: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var out []journalRow
	for rows.Next() {
		var r journalRow
		if err := rows.Scan(&r.JobID, &r.DeviceID, &r.Attempt, &r.NodeID, &r.RunID,
			&r.Outcome, &r.FailureStage, &r.TaskName, &r.FQCN); err != nil {
			t.Fatalf("failed to scan a journal row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("failed while reading journal rows: %v", err)
	}
	return out
}

// waitForJournalRows polls until the job has at least want rows or the
// deadline passes, returning whatever it last read.
//
// Polling rather than waiting on an event, because the store is a
// separate consumer from the one the dispatch result arrives on: the
// final task.completed can land before the last journal batch has been
// written, and asserting immediately on it would be a race that usually
// passes.
func waitForJournalRows(t *testing.T, dbPath, jobID string, want int) []journalRow {
	t.Helper()

	deadline := time.Now().Add(90 * time.Second)
	var rows []journalRow
	for time.Now().Before(deadline) {
		rows = readJournalRaw(t, dbPath, jobID)
		if len(rows) >= want {
			return rows
		}
		time.Sleep(250 * time.Millisecond)
	}
	return rows
}

// TestJournalReleaseGate_EveryRedeliveryJournalsAsItsOwnAttempt is the
// redelivery gate.
//
// A dispatch carrying a deliberately wrong password fails inside the real
// child process, against the real sshd container. native.Adapter.Execute
// turns that into an error (result.HasErrors), the Runner's own delivery
// policy Naks it, and JetStream redelivers it until MaxDeliver is spent.
// That is a real retry of the same dispatch, and the journal has to read
// as one: the same job, the same device and the same graph node, five
// times, distinguished only by an attempt number that the platform never
// computed but read off the delivery itself.
//
// The negative this rules out is the one that matters. If Attempt were
// dropped, defaulted, or recomputed per run, five retries would either
// collapse into one row, through the unique index, or land as five rows
// nothing could tell apart from five unrelated runs.
func TestJournalReleaseGate_EveryRedeliveryJournalsAsItsOwnAttempt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}

	h := newReleaseGateHarness(t)
	dbPath := startJournalStore(t, h)

	jobID := uuid.New().String()
	payload := wire.DispatchPayload{
		JobID:        jobID,
		RunbookID:    "ping",
		DeviceID:     "release-gate-device",
		DeviceName:   "release-gate-device",
		DeviceHost:   h.sshHost,
		SSHPort:      h.sshPort,
		Capabilities: []capability.Name{capability.NameSSHTransport},
		// Wrong on purpose. The point is a task that really fails against
		// a real device, not one stubbed into failing.
		Secrets: credential.Flatten(credential.Credential{Username: releaseGateSSHUser, Password: "definitely-the-wrong-password"}),
	}

	final := h.dispatch(t, payload)
	if final.Status != "failed" {
		t.Fatalf("first delivery reported %q, want %q", final.Status, "failed")
	}

	// One node in the runbook, so one row per delivery.
	want := topology.MaxDeliverDefault
	rows := waitForJournalRows(t, dbPath, jobID, want)
	if len(rows) != want {
		t.Fatalf("journalled %d rows for job %s, want %d (one per delivery): %+v", len(rows), jobID, want, rows)
	}

	seenTuple := make(map[string]bool, len(rows))
	seenRun := make(map[string]bool, len(rows))
	for i, r := range rows {
		// Ordered and gapless, counting from one. JetStream's own
		// NumDelivered is 1-based, and a zero here would mean the attempt
		// never reached the row at all.
		if r.Attempt != i+1 {
			t.Errorf("row %d has attempt %d, want %d: the attempts must be the delivery counter, in order", i, r.Attempt, i+1)
		}
		if r.DeviceID != payload.DeviceID {
			t.Errorf("row %d names device %q, want %q", i, r.DeviceID, payload.DeviceID)
		}
		if r.Outcome != "failed" {
			t.Errorf("row %d has outcome %q, want %q: every delivery really did fail", i, r.Outcome, "failed")
		}
		if r.FailureStage != "action" {
			t.Errorf("row %d has failure stage %q, want %q: an SSH authentication failure happens while running the action", i, r.FailureStage, "action")
		}

		tuple := fmt.Sprintf("%s|%s|%d|%s", r.JobID, r.DeviceID, r.Attempt, r.NodeID)
		if seenTuple[tuple] {
			t.Errorf("duplicate (job, device, attempt, node) tuple %q: the unique index exists to make this impossible", tuple)
		}
		seenTuple[tuple] = true
		seenRun[r.RunID] = true
	}

	// Each redelivery is a fresh Executor.Run, so each carries its own
	// RunID. Sharing one would mean the run identity had leaked across
	// process-level retries, which is precisely what JobID plus Attempt
	// exists to express instead.
	if len(seenRun) != want {
		t.Errorf("the %d deliveries carry %d distinct run ids, want %d: each retry is its own run", want, len(seenRun), want)
	}
}

// TestJournalReleaseGate_JournalsTheNodeThatRanNotTheOneThatFailedToStart
// is the positive half, and the assertion FAILURE_PATTERNS.md #120 records
// as the one that was missing when over-masking shipped green.
//
// A journal that refused to store anything would pass every "no secret
// appears" assertion perfectly. So the gate has to say what the rows DO
// contain: the resolved method, the author's own task name, and a graph
// node id, all of them present and correct for a run that really
// happened.
func TestJournalReleaseGate_JournalsTheNodeThatRanNotTheOneThatFailedToStart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}

	h := newReleaseGateHarness(t)
	dbPath := startJournalStore(t, h)

	payload := pingPayload(h)
	final := h.dispatch(t, payload)
	if final.Status == "failed" {
		t.Fatalf("the good-credential dispatch failed, so this gate is measuring the wrong run: %+v", final)
	}

	rows := waitForJournalRows(t, dbPath, payload.JobID, 1)
	if len(rows) != 1 {
		t.Fatalf("journalled %d rows for a one-task runbook, want 1: %+v", len(rows), rows)
	}

	row := rows[0]
	if row.FQCN != "net.ssh.ping" {
		t.Errorf("fqcn = %q, want %q: the method must be resolved through the registry and stored", row.FQCN, "net.ssh.ping")
	}
	if row.TaskName != "ping" {
		t.Errorf("task_name = %q, want %q: the author's own name is one of the few fields stored as written", row.TaskName, "ping")
	}
	if row.NodeID == "" {
		t.Error("node_id is empty: without it a row cannot be tied back to a position in the graph")
	}
	if row.RunID == "" {
		t.Error("run_id is empty: without it the rows of one run cannot be grouped")
	}
	if row.Attempt != 1 {
		t.Errorf("attempt = %d, want 1: a dispatch that succeeded first time is its first delivery", row.Attempt)
	}
	if row.Outcome == "failed" {
		t.Errorf("outcome = %q, but the dispatch reported success", row.Outcome)
	}
}
