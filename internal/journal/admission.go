// Package journal: which Runner batches the Controller stores.
//
// Every Runner may publish on every job's journal subject (the mesh grants
// the fleet JournalSubjectAll), so a batch arriving says nothing on its own
// about whether its sender ran what it describes. The consumer therefore
// asks the Controller's own record before storing one: a batch must name a
// (job, device) the Controller handed to a Runner, and every entry in it
// must name that same job and device.
//
// This narrows what a compromised Runner can write; it does not close it.
// A Runner that pulled a dispatch can still publish a batch for it with
// values of its choosing. Binding a publication to the Runner that holds
// the dispatch is Phase 105's signing work, recorded there, and a key in
// the dispatch payload was rejected because it would be a new secret on
// the stream that any Runner could read by pulling the dispatch.
package journal

import "context"

// Admission is what the Controller's record says about a batch's job and
// device.
type Admission int

// The three answers an AdmissionCheck gives.
const (
	// AdmitUnrecorded means the record has no such job, or no row for the
	// device yet. The consumer asks for the batch again later, since an
	// unwindowed fan-out publishes a dispatch before it records it; a job
	// that never existed is dead-lettered after the delivery limit.
	AdmitUnrecorded Admission = iota
	// AdmitDispatched means a Runner was handed the device's dispatch.
	AdmitDispatched
	// AdmitNotDispatched means no Runner was: the device was skipped,
	// failed at dispatch, or waits in a forks window. A batch for it
	// describes a run that did not happen, and is dropped.
	AdmitNotDispatched
)

// AdmissionCheck answers for one job and device. The Controller composes
// it over internal/dispatch's record (JobStore.DispatchState).
type AdmissionCheck func(ctx context.Context, jobID, deviceID string) (Admission, error)

// SubscriberOption configures a Subscriber.
type SubscriberOption func(*Subscriber)

// WithAdmission has the consumer store only batches check admits. With
// none, every well-formed batch is stored, which is what a test of the
// store alone wants and what no Controller should run with.
func WithAdmission(check AdmissionCheck) SubscriberOption {
	return func(s *Subscriber) {
		s.admit = check
	}
}
