// A run journal batch whose job or device ID cannot name one is
// acknowledged before any query (FAILURE_PATTERNS 924). The same batches
// against a real PostgreSQL, where such an ID fails every query, are in
// internal/facts/entstore's TestConsumers_ARefusedValueIsAcknowledgedOnPostgres.
package journal_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// TestSubscriberDropsABatchWhoseIDsCannotNameOne: neither the admission
// check, which queries with both IDs in production, nor the store is
// reached, and a batch naming no device is still taken.
func TestSubscriberDropsABatchWhoseIDsCannotNameOne(t *testing.T) {
	for name, tc := range map[string]struct {
		job, device string
		admitted    bool
	}{
		"a NUL in the job":                  {"job\x001", "device-1", false},
		"a NUL in the device":               {"job-1", "device\x001", false},
		"a control character in the device": {"job-1", "device\n1", false},
		"no device":                         {"job-1", "", true},
	} {
		t.Run(name, func(t *testing.T) {
			store, path := newEntStore(t)
			admitted := false
			sub := journal.NewSubscriber(store, discardLogger(), journal.WithAdmission(func(context.Context, string, string) (journal.Admission, error) {
				admitted = true
				return journal.AdmitNotDispatched, nil
			}))
			batch := journal.Batch{JobID: tc.job, DeviceID: tc.device, Entries: []engine.JournalEntry{walkEntry(tc.job, tc.device, 0, 1, "tasks[0]")}}
			if err := sub.Handle(batchEvent(t, batch)); err != nil {
				t.Fatalf("Handle = %v; the batch was handed back to be redelivered", err)
			}
			if admitted != tc.admitted {
				t.Errorf("admission check reached = %v, want %v", admitted, tc.admitted)
			}
			if rows := rawQuery(t, path, "SELECT COUNT(*) FROM journal_entries"); rows[0] != "0" {
				t.Errorf("the batch wrote %v rows", rows)
			}
		})
	}
}
