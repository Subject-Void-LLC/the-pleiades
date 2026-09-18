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
	"errors"
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

// logConsumer is the pair of questions this file asks a JetStream consumer:
// how much is left, and give me the next batch.
//
// jetstream.Consumer satisfies it as declared. Naming the two methods used
// rather than taking the whole interface is what lets the decisions below
// -- whether to offer the control, what to do when the broker refuses --
// be exercised without a container, while the calls that actually reach a
// broker stay one line each and are proven by the SSE viewer against the
// same stream and the same consumer configuration.
type logConsumer interface {
	Info(ctx context.Context) (*jetstream.ConsumerInfo, error)
	FetchNoWait(batch int) (jetstream.MessageBatch, error)
}

// LogArchive drains one job's retained log output.
type LogArchive struct {
	js jetstream.JetStream

	// open obtains the consumer for one job. It is the real broker call in
	// every composition and is replaced only by this package's own tests,
	// which is why it is unexported and has no setter: a caller that could
	// swap it could make a download read a subject nobody authorised.
	open func(ctx context.Context, jobID string) (logConsumer, error)
}

// NewLogArchive constructs an archive over the shared JetStream handle
// topology.EnsureStream has already provisioned the stream on.
func NewLogArchive(js jetstream.JetStream) *LogArchive {
	a := &LogArchive{js: js}
	a.open = a.consumerFor
	return a
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
	if a == nil || a.open == nil {
		return false
	}
	consumer, err := a.open(ctx, jobID)
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
	if a == nil || a.open == nil {
		return fmt.Errorf("api: no broker is wired, so job %q has no log archive", jobID)
	}
	consumer, err := a.open(ctx, jobID)
	if err != nil {
		return fmt.Errorf("api: opening the log archive for job %q: %w", jobID, err)
	}

	if err := drainInto(ctx, w, func(n int) ([][]byte, error) {
		batch, err := consumer.FetchNoWait(n)
		if err != nil {
			return nil, err
		}
		var out [][]byte
		for msg := range batch.Messages() {
			out = append(out, msg.Data())
		}
		return out, batch.Error()
	}); err != nil {
		return fmt.Errorf("api: draining the log archive for job %q: %w", jobID, err)
	}
	return nil
}

// fetchBatch pulls up to n messages, returning their bodies.
//
// A function rather than the jetstream.Consumer itself, so the drain below
// can be exercised against a message source that is not a broker. What that
// buys is not convenience: the decisions in drainInto -- where it stops,
// what it does at the bound, what it writes when it has been cut short --
// are real behaviour a reader needs pinned, and they are not observable
// through a container that would have to be persuaded to hold fifty
// thousand messages to reach the interesting branch.
//
// The eight lines that adapt a real consumer to this shape stay in WriteTo,
// where they are thin enough to read as obviously correct.
//
// KNOWN GAP, stated rather than implied. Nothing in this repository drives
// these two broker calls against a real broker. This file's tests fake the
// consumer, and internal/api's SSE viewer -- which an earlier version of
// this comment claimed covered the broker half -- fakes JetStream too
// (mockJetStreamForLogs in logs_test.go) and calls Consume rather than the
// Info and FetchNoWait used here. So what IS proven is the request this
// file builds and every decision it makes about the answer; what is NOT
// proven is that a real nats-server replies the way the fakes do. Closing
// it means adding internal/api to the container packages, which moves a
// fast parallel package into the serial docker group: a change to the
// gate's shape rather than to this file.
type fetchBatch func(n int) ([][]byte, error)

// drainInto writes every message a fetcher will give, up to the bound.
//
// Reaching the bound is not the same question as being truncated, and this
// used to conflate them: an archive holding exactly maxArchivedLogMessages
// drained completely and was then stamped partial, so a whole log file said
// of itself that it was not one. Which of the two happened is decided
// below, from evidence rather than from the count.
func drainInto(ctx context.Context, w io.Writer, fetch fetchBatch) error {
	written := 0
	for {
		bodies, err := fetch(archiveFetchBatch)
		if err != nil {
			return err
		}

		// A drained-but-empty fetch is how FetchNoWait says there is
		// nothing more right now, which for a finished job is the end.
		if len(bodies) == 0 {
			return nil
		}

		for i, body := range bodies {
			if _, err := w.Write(append(body, '\n')); err != nil {
				return err
			}
			written++
			if written < maxArchivedLogMessages {
				continue
			}
			// Bodies still in hand are proof that more exists, and are
			// free. Only the exact boundary has to go and ask.
			if i+1 < len(bodies) {
				return writeTruncationMarker(w)
			}
			return confirmTruncated(w, fetch)
		}

		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// confirmTruncated asks whether anything is left, and says so.
//
// Reached only when the bound fell exactly on a batch boundary, which is
// the one case where the drain holds no evidence either way. A short batch
// is legal -- FetchNoWait returns what is there at that instant -- so
// inferring "complete" from a batch that did not fill would hand back a
// genuinely partial file that looked whole, which is the failure the marker
// exists to prevent and is much worse than the one it replaced.
//
// The extra round trip costs one FetchNoWait on the rare drain that reaches
// fifty thousand messages, and nothing at all on every other download. Its
// bodies are discarded: this is a question, not a read.
func confirmTruncated(w io.Writer, fetch fetchBatch) error {
	more, err := fetch(1)
	if err != nil || len(more) > 0 {
		// An error here is "unknown", and unknown fails toward partial.
		// A file that wrongly warns costs a reader a second look; one
		// that wrongly looks complete is how somebody concludes a run
		// finished cleanly when it did not.
		return writeTruncationMarker(w)
	}
	return nil
}

// writeTruncationMarker says in the FILE that it is partial.
//
// In the file rather than only in a log, because the person holding it is
// the one who needs to know.
func writeTruncationMarker(w io.Writer) error {
	_, err := fmt.Fprintf(w,
		"{\"pleiades\":\"truncated\",\"reason\":\"this download stops at %d messages; the rest is in the log stream\"}\n",
		maxArchivedLogMessages)
	return err
}

// consumerFor builds the ephemeral, non-acknowledging consumer scoped to
// one job, the same one the live viewer uses.
//
// Ephemeral and AckNone is what makes a download harmless to run twice and
// harmless to run while somebody is watching: it tracks nothing, so it
// cannot advance a position another reader depends on. The timeout is
// applied here rather than around the whole drain, so a large but healthy
// archive is not cut off partway for being large.
func (a *LogArchive) consumerFor(ctx context.Context, jobID string) (logConsumer, error) {
	// An archive with no broker answers rather than panicking, and the
	// answer is the refusing one. A composition without JetStream is real
	// -- every test harness in this package is one -- and the two callers
	// both treat a failure here as "there is nothing to download", which
	// withholds the control instead of crashing the page that draws it.
	if a == nil || a.js == nil {
		return nil, errors.New("api: no broker is wired, so no log archive exists")
	}
	ctx, cancel := context.WithTimeout(ctx, archiveFetchTimeout)
	defer cancel()
	return a.js.CreateOrUpdateConsumer(ctx, topology.StreamName, topology.LogViewerConsumerConfig(jobID))
}
