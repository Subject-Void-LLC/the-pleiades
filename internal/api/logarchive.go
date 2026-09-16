// This file is the download side of the job log stream: the same messages
// the SSE viewer pushes, drained once and written to a file.
//
// It is a separate type from LogStreamer rather than a method on it,
// because the two want opposite things from the same subject. A viewer
// wants everything as it arrives and never ends; a download wants what is
// there right now and must end, or it hands back a file that is still being
// written. Sharing a type would mean one object with a mode flag deciding
// which of two incompatible contracts it was honouring.
//
// What it CANNOT do is tell a complete run from a partial one, and that is
// a property of the stream rather than of this code. There is no
// end-of-stream marker on a job's log subject: a drain stops when the
// broker says nothing is pending, which for a job still running means "you
// have caught up for now" and for a finished one means "that was all of
// it". The caller is what refuses a running job, because the caller is what
// knows the job's state.
package api

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// maxArchivedLogMessages bounds one drain.
//
// A job that logged more than this is one whose output belongs in a log
// pipeline rather than in a browser download, and an unbounded drain is a
// request that can be made to run for as long as the broker has data. The
// file says when it stopped rather than simply ending, because a truncated
// log that looks complete is how somebody concludes a run finished cleanly
// when it did not.
const maxArchivedLogMessages = 50000

// archiveFetchBatch is how many messages one FetchNoWait asks for, matching
// the Runner's own pull loop rather than being tuned here.
const archiveFetchBatch = 256

// archiveFetchTimeout bounds a single batch, so a broker that accepts the
// request and then goes quiet fails the download instead of holding the
// response open.
const archiveFetchTimeout = 10 * time.Second

// LogArchive drains one job's retained log output.
type LogArchive struct {
	js jetstream.JetStream
}

// NewLogArchive constructs an archive over the shared JetStream handle
// topology.EnsureStream has already provisioned the stream on.
func NewLogArchive(js jetstream.JetStream) *LogArchive {
	return &LogArchive{js: js}
}

// Retained reports whether the broker still holds output for this job.
//
// It asks the broker rather than inferring from the job's age, and the
// difference matters: the retention window is derived from an operator's
// outage budget and ranges from hours to months across deployments, so a
// calculation here would be this package's second opinion about a number
// internal/topology already owns. Asking costs one consumer create and one
// info call, on a page render.
//
// A broker that cannot be reached reports false, which withholds the
// control. The failure an operator can act on is a missing link, never a
// file that turns out to be empty after they have sent it to somebody.
func (a *LogArchive) Retained(ctx context.Context, jobID string) bool {
	consumer, err := a.consumerFor(ctx, jobID)
	if err != nil {
		return false
	}
	info, err := consumer.Info(ctx)
	if err != nil {
		return false
	}
	return info.NumPending > 0
}

// WriteTo drains what is retained, oldest first, one JSON object per line.
//
// Newline-delimited JSON rather than a JSON array, because the file is
// produced by streaming and an array would need its closing bracket written
// after the last message -- which a drain that fails partway cannot do,
// leaving a file that no parser will read at all. A truncated NDJSON file
// loses its last line and stays readable, which is the right failure for an
// artefact somebody is saving in order to investigate something.
func (a *LogArchive) WriteTo(ctx context.Context, w io.Writer, jobID string) error {
	consumer, err := a.consumerFor(ctx, jobID)
	if err != nil {
		return fmt.Errorf("api: opening the log archive for job %q: %w", jobID, err)
	}

	written := 0
	for written < maxArchivedLogMessages {
		batch, err := consumer.FetchNoWait(archiveFetchBatch)
		if err != nil {
			return fmt.Errorf("api: draining the log archive for job %q: %w", jobID, err)
		}

		got := 0
		for msg := range batch.Messages() {
			got++
			if _, err := w.Write(append(msg.Data(), '\n')); err != nil {
				return fmt.Errorf("api: writing the log archive for job %q: %w", jobID, err)
			}
			written++
			if written >= maxArchivedLogMessages {
				break
			}
		}
		if err := batch.Error(); err != nil {
			return fmt.Errorf("api: draining the log archive for job %q: %w", jobID, err)
		}

		// A drained-but-empty fetch is how FetchNoWait says there is
		// nothing more right now, which for a finished job is the end.
		if got == 0 {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	if written >= maxArchivedLogMessages {
		// Said in the file rather than only in a log, because the person
		// holding the file is the one who needs to know it is partial.
		if _, err := fmt.Fprintf(w,
			"{\"pleiades\":\"truncated\",\"reason\":\"this download stops at %d messages; the rest is in the log stream\"}\n",
			maxArchivedLogMessages); err != nil {
			return err
		}
	}
	return nil
}

// consumerFor builds the ephemeral, non-acknowledging consumer scoped to
// one job, the same one the live viewer uses.
//
// Ephemeral and AckNone is what makes a download harmless to run twice and
// harmless to run while somebody is watching: it tracks nothing, so it
// cannot advance a position another reader depends on. The timeout is
// applied here rather than around the whole drain, so a large but healthy
// archive is not cut off partway for being large.
func (a *LogArchive) consumerFor(ctx context.Context, jobID string) (jetstream.Consumer, error) {
	ctx, cancel := context.WithTimeout(ctx, archiveFetchTimeout)
	defer cancel()
	return a.js.CreateOrUpdateConsumer(ctx, topology.StreamName, topology.LogViewerConsumerConfig(jobID))
}
