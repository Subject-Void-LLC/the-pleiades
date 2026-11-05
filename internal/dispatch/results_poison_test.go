// A job result no delivery can store is acknowledged rather than
// redelivered until its budget runs out, and a NUL a device put in the
// reason no longer costs the device's outcome (FAILURE_PATTERNS 924).
package dispatch_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lib/pq"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
)

// recordingStore is a JobStore that remembers what RecordResult was asked
// to store, and fails it with err when err is set.
type recordingStore struct {
	dispatch.JobStore
	reasons []string
	err     error
}

// RecordResult implements dispatch.JobStore.
func (s *recordingStore) RecordResult(ctx context.Context, jobID, deviceID string, result dispatch.Result, reason string, unchecked int) (bool, error) {
	s.reasons = append(s.reasons, reason)
	if s.err != nil {
		return false, s.err
	}
	return s.JobStore.RecordResult(ctx, jobID, deviceID, result, reason, unchecked)
}

// TestResultConsumer_AnIDThatCannotNameOneIsDroppedBeforeAnyQuery: a NUL
// in a result's job or device ID failed every PostgreSQL query made with it
// (SQLSTATE 22021, measured on the facts consumer's), so the result was
// handed back forever. It is now acknowledged without reaching the store.
func TestResultConsumer_AnIDThatCannotNameOneIsDroppedBeforeAnyQuery(t *testing.T) {
	store, _ := newTestStore(t)
	jobID, _ := runningJob(t, store, "dev-1")
	for name, ids := range map[string][2]string{
		"a NUL in the job":                  {jobID + "\x00", "dev-1"},
		"a NUL in the device":               {jobID, "dev\x001"},
		"a control character in the device": {jobID, "dev\n1"},
		"no device":                         {jobID, ""},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &recordingStore{JobStore: store}
			consumer := dispatch.NewResultConsumer(rec, quietLogger())
			if err := consumer.Handle(resultEvent(t, ids[0], ids[1], "completed", "")); err != nil {
				t.Fatalf("Handle = %v; a result that can never be stored was handed back", err)
			}
			if len(rec.reasons) != 0 {
				t.Error("a result whose IDs cannot name one reached the store")
			}
		})
	}
}

// TestResultConsumer_ANULInTheReasonKeepsTheOutcome: the reason is a
// device's own text, and PostgreSQL refuses a whole text value over one NUL
// (SQLSTATE 22021). The NUL is replaced, the rest of the reason is kept,
// and the result is recorded.
func TestResultConsumer_ANULInTheReasonKeepsTheOutcome(t *testing.T) {
	store, _ := newTestStore(t)
	jobID, _ := runningJob(t, store, "dev-1")
	rec := &recordingStore{JobStore: store}
	consumer := dispatch.NewResultConsumer(rec, quietLogger())
	if err := consumer.Handle(resultEvent(t, jobID, "dev-1", "failed", "exit 1: bad\x00byte")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(rec.reasons) != 1 || strings.ContainsRune(rec.reasons[0], 0) || !strings.HasPrefix(rec.reasons[0], "exit 1: bad") || !strings.HasSuffix(rec.reasons[0], "byte") {
		t.Fatalf("the store was asked to keep %q, want the reason with its NUL replaced", rec.reasons)
	}
	got, _, err := store.Get(context.Background(), jobID)
	// The job's one device reported, so the job ended: the result was
	// recorded, not lost.
	if err != nil || got.State != "completed" {
		t.Fatalf("job = %+v, %v; want it ended by the device's recorded result", got, err)
	}
}

// TestResultConsumer_ADataExceptionIsAcknowledged: a value the database
// refuses as data (SQLSTATE class 22) fails the same way on every delivery,
// so the result is acknowledged; a unique violation and a connection
// failure are still handed back. The server-issued forms of these errors
// are checked against PostgreSQL in internal/facts/entstore.
func TestResultConsumer_ADataExceptionIsAcknowledged(t *testing.T) {
	store, _ := newTestStore(t)
	jobID, _ := runningJob(t, store, "dev-1")
	for name, tc := range map[string]struct {
		err         error
		acknowledge bool
	}{
		"a NUL in text":   {&pq.Error{Code: "22021"}, true},
		"a unique clash":  {&pq.Error{Code: "23505"}, false},
		"a lost database": {errors.New("driver: bad connection"), false},
	} {
		t.Run(name, func(t *testing.T) {
			consumer := dispatch.NewResultConsumer(&recordingStore{JobStore: store, err: tc.err}, quietLogger())
			err := consumer.Handle(resultEvent(t, jobID, "dev-1", "completed", ""))
			if (err == nil) != tc.acknowledge {
				t.Fatalf("Handle = %v; acknowledged %v, want %v", err, err == nil, tc.acknowledge)
			}
		})
	}
}
