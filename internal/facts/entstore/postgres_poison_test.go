// A batch PostgreSQL can never store is acknowledged rather than redelivered
// until its delivery budget runs out, and what can be stored is
// (FAILURE_PATTERNS 919). Each case is something a device or a Runner can
// put in a batch, run against the production dialect.
package entstore_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/poison"
)

// unchecked is an entry carrying the hash Prepare would give its value,
// built without Prepare, so the batch holds exactly what a Runner could
// publish whatever Prepare refuses.
func unchecked(t *testing.T, key string, value any) facts.Entry {
	t.Helper()
	hash, err := facts.Hash(key, value)
	if err != nil {
		t.Fatal(err)
	}
	return facts.Entry{Key: key, Value: value, Hash: hash, Kind: facts.KindStable}
}

// TestSubscriber_APoisonedBatchIsAcknowledgedOnPostgres: a batch whose job
// or device ID holds a NUL failed every query PostgreSQL ran with it
// (SQLSTATE 22021) and went back to the bus; it is now dropped as malformed
// before any query, the admission check included. A NUL or a huge number in
// a value, or a NUL in the run's provenance, never was a poison: the store
// keeps each as text inside its JSON column, and these cases hold that.
func TestSubscriber_APoisonedBatchIsAcknowledgedOnPostgres(t *testing.T) {
	kernel := func() facts.Entry { return unchecked(t, "ansible_kernel", "6.1") }
	cases := map[string]struct {
		build func(device string) facts.Batch
		// want is the keys stored afterwards.
		want []string
	}{
		"a NUL in the device": {func(string) facts.Batch {
			return facts.Batch{JobID: "j1", DeviceID: "web\x001", Entries: []facts.Entry{kernel()}}
		}, nil},
		"a NUL in the job": {func(device string) facts.Batch {
			return facts.Batch{JobID: "j\x001", DeviceID: device, Entries: []facts.Entry{kernel()}}
		}, nil},
		// Invalid UTF-8 is no case here: encoding/json writes it as U+FFFD
		// when a Runner publishes, and the Controller's decoder does the same
		// to raw bytes a hand-made message sends, so no batch reaches the
		// database carrying it.
		"a control character in the device": {func(string) facts.Batch {
			return facts.Batch{JobID: "j1", DeviceID: "web\n1", Entries: []facts.Entry{kernel()}}
		}, nil},
		"a NUL in a value": {func(device string) facts.Batch {
			return facts.Batch{JobID: "j1", DeviceID: device, Entries: []facts.Entry{kernel(), unchecked(t, "ansible_hostname", "web\x00pad")}}
		}, []string{"ansible_hostname", "ansible_kernel"}},
		"a NUL in a nested field's name": {func(device string) facts.Batch {
			return facts.Batch{JobID: "j1", DeviceID: device, Entries: []facts.Entry{kernel(), unchecked(t, "ansible_lsb", map[string]any{"id\x00": "x"})}}
		}, []string{"ansible_kernel", "ansible_lsb"}},
		"a number past what PostgreSQL's numeric holds": {func(device string) facts.Batch {
			return facts.Batch{JobID: "j1", DeviceID: device, Entries: []facts.Entry{kernel(), unchecked(t, "ansible_memtotal_mb", json.Number("1e200000"))}}
		}, []string{"ansible_kernel", "ansible_memtotal_mb"}},
		"a NUL in the run's node": {func(device string) facts.Batch {
			return facts.Batch{JobID: "j1", DeviceID: device, By: facts.Provenance{Run: "r1", Node: "t\x00"}, Entries: []facts.Entry{kernel()}}
		}, []string{"ansible_kernel"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store, device := postgresStore(t)
			batch := tc.build(device)
			batch.ObservedAt = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
			// The admission check queries the database with both IDs in
			// production; here it admits, and is never reached by a
			// malformed batch.
			admitted := false
			sub := entstore.NewSubscriber(store, nil, func(context.Context, string, string) (journal.Admission, error) {
				admitted = true
				return journal.AdmitDispatched, nil
			})
			if err := handle(t, sub, batch); err != nil {
				t.Fatalf("Handle = %v; a batch that can never be stored was handed back to be redelivered", err)
			}
			if tc.want == nil && admitted {
				t.Error("a malformed batch reached the admission check, which queries the database with its IDs")
			}
			current, err := store.Current(context.Background(), device)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, e := range current {
				got = append(got, e.Key)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("stored %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("stored %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// failingStore is a facts.Store whose Record returns err.
type failingStore struct {
	facts.Store
	err error
}

// Record implements facts.Store by failing.
func (f failingStore) Record(context.Context, string, facts.Observation) error { return f.err }

// serverError returns the error a real PostgreSQL server answers query
// with.
func serverError(t *testing.T, query string, args ...any) error {
	t.Helper()
	postgresStore(t)
	db, err := sql.Open("postgres", pgAdmin)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var out string
	err = db.QueryRow(query, args...).Scan(&out)
	if err == nil {
		t.Fatalf("%s succeeded, so it cannot stand for a refusal", query)
	}
	return err
}

// uniqueViolation returns the error a real PostgreSQL server answers a
// second insert of one primary key with (SQLSTATE 23505), on one pinned
// connection so the temporary table is still there.
func uniqueViolation(t *testing.T) error {
	t.Helper()
	postgresStore(t)
	db, err := sql.Open("postgres", pgAdmin)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "CREATE TEMP TABLE once (x int PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO once VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	_, err = conn.ExecContext(ctx, "INSERT INTO once VALUES (1)")
	if err == nil {
		t.Fatal("a second insert of one primary key succeeded")
	}
	return err
}

// TestSubscriber_ADataExceptionIsAcknowledgedAndOthersRedelivered: whatever
// slips past the checks and is refused by the server as data (SQLSTATE class
// 22) is acknowledged, since it fails the same way on every delivery, while
// a unique violation, which is how two replicas extending one chain learn to
// try again, and a connection failure are still handed back. Each error is
// the server's own, not one built by hand.
func TestSubscriber_ADataExceptionIsAcknowledgedAndOthersRedelivered(t *testing.T) {
	nul := serverError(t, "SELECT $1::text", "a\x00b")
	overflow := serverError(t, "SELECT (1e200000::numeric * 1e200000::numeric)::text")
	zero := serverError(t, "SELECT (1/0)::text")
	unique := uniqueViolation(t)
	for name, tc := range map[string]struct {
		err         error
		acknowledge bool
	}{
		"a NUL in text":               {nul, true},
		"a numeric overflow":          {overflow, true},
		"a division by zero":          {zero, true},
		"a unique violation":          {unique, false},
		"a connection that went away": {errors.New("facts: driver: bad connection"), false},
	} {
		t.Run(name, func(t *testing.T) {
			if code, got := poison.Unstorable(tc.err); got != tc.acknowledge || got && len(code) != 5 {
				t.Fatalf("Unstorable(%v) = %q, %v, want %v", tc.err, code, got, tc.acknowledge)
			}
			store, device := postgresStore(t)
			sub := entstore.NewSubscriber(failingStore{Store: store, err: tc.err}, nil, nil)
			batch := facts.Batch{JobID: "j1", DeviceID: device, ObservedAt: time.Now(), Entries: []facts.Entry{unchecked(t, "ansible_kernel", "6.1")}}
			err := handle(t, sub, batch)
			if acknowledged := err == nil; acknowledged != tc.acknowledge {
				t.Fatalf("Handle = %v; acknowledged %v, want %v", err, acknowledged, tc.acknowledge)
			}
		})
	}
}

// TestConsumers_ARefusedValueIsAcknowledgedOnPostgres holds the run journal
// and job result consumers to what the facts consumer keeps, on the dialect
// that refuses what SQLite takes (FAILURE_PATTERNS 924). They live here
// because this package has the PostgreSQL harness: a NUL in a journal
// batch's IDs or in a task name, and in a result's IDs, failed every
// delivery and was handed back until its budget ran out, and a NUL a device
// put in a result's reason lost the device's outcome.
func TestConsumers_ARefusedValueIsAcknowledgedOnPostgres(t *testing.T) {
	entryFor := func(job, device, task string) engine.JournalEntry {
		return engine.JournalEntry{
			JobID: job, DeviceID: device, RunID: "run-1", Sequence: 1, NodeID: "tasks[0]",
			DAGID: "a-runbook", FQCN: "exec.command", TaskName: task, Outcome: engine.OutcomeChanged,
			StartedAt: time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 11, 5, 10, 0, 1, 0, time.UTC),
		}
	}
	journalCases := map[string]struct {
		job, device, task string
	}{
		"a journal batch with a NUL in the job":    {"job\x001", "dev-1", "a task"},
		"a journal batch with a NUL in the device": {"job-1", "dev\x001", "a task"},
		"a journal batch with a NUL in a task":     {"job-1", "dev-1", "a\x00task"},
	}
	for name, tc := range journalCases {
		t.Run(name, func(t *testing.T) {
			client, _ := postgresClient(t)
			sub := journal.NewSubscriber(journal.NewEntStore(client), slog.New(slog.DiscardHandler))
			batch := journal.Batch{JobID: tc.job, DeviceID: tc.device, Entries: []engine.JournalEntry{entryFor(tc.job, tc.device, tc.task)}}
			data, err := json.Marshal(batch)
			if err != nil {
				t.Fatal(err)
			}
			if err := sub.Handle(event.Event{ID: "e1", Type: journal.EventType, Data: data}); err != nil {
				t.Fatalf("Handle = %v; a batch that can never be stored was handed back to be redelivered", err)
			}
		})
	}

	t.Run("a result with a NUL in the job or the device", func(t *testing.T) {
		client, _ := postgresClient(t)
		consumer := dispatch.NewResultConsumer(dispatch.NewEntJobStore(client), slog.New(slog.DiscardHandler))
		for _, ids := range [][2]string{{"job\x001", "dev-1"}, {"job-1", "dev\x001"}} {
			if err := consumer.Handle(resultEventOf(t, ids[0], ids[1], "")); err != nil {
				t.Fatalf("Handle(%q, %q) = %v; a result that can never be stored was handed back", ids[0], ids[1], err)
			}
		}
	})

	t.Run("a result whose reason holds a NUL is recorded", func(t *testing.T) {
		client, _ := postgresClient(t)
		store := dispatch.NewEntJobStore(client)
		ctx := context.Background()
		job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "launcher"}
		if err := store.Create(ctx, job); err != nil {
			t.Fatal(err)
		}
		_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{DeviceID: "dev-1", DeviceName: "dev-1", Outcome: dispatch.OutcomeDispatched}); err != nil {
			t.Fatal(err)
		}
		if err := store.SettleRunning(ctx, job.JobID, fence, 1, 0, 0); err != nil {
			t.Fatal(err)
		}
		consumer := dispatch.NewResultConsumer(store, slog.New(slog.DiscardHandler))
		if err := consumer.Handle(resultEventOf(t, job.JobID, "dev-1", "exit 1: bad\x00byte")); err != nil {
			t.Fatalf("Handle = %v; the device's result was not recorded", err)
		}
		got, _, err := store.Get(ctx, job.JobID)
		if err != nil || got.State != "completed" {
			t.Fatalf("job = %+v, %v; want it ended by the device's recorded result", got, err)
		}
	})
}

// resultEventOf is a failed result the way a Runner publishes one.
func resultEventOf(t *testing.T, jobID, deviceID, reason string) event.Event {
	t.Helper()
	body, err := json.Marshal(map[string]string{"id": jobID + ":" + deviceID, "job_id": jobID, "device_id": deviceID, "outcome": "failed", "reason": reason})
	if err != nil {
		t.Fatal(err)
	}
	return event.Event{ID: "r1", Data: body}
}
