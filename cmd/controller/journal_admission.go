// Package main: the run journal consumer's admission check, composed over
// the dispatch record, since internal/journal does not import
// internal/dispatch.
package main

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// journalAdmission answers the journal consumer from jobs: a batch is
// admitted only for a device a Runner was handed.
func journalAdmission(jobs dispatch.JobStore) journal.AdmissionCheck {
	return func(ctx context.Context, jobID, deviceID string) (journal.Admission, error) {
		state, err := jobs.DispatchState(ctx, jobID, deviceID)
		if err != nil {
			return journal.AdmitUnrecorded, err
		}
		switch state {
		case dispatch.DispatchSent:
			return journal.AdmitDispatched, nil
		case dispatch.DispatchNotSent:
			return journal.AdmitNotDispatched, nil
		default:
			return journal.AdmitUnrecorded, nil
		}
	}
}
