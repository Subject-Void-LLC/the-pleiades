// This file covers the log archive's drain: where it stops, what it does at
// the bound, and what it leaves in the file when it has been cut short.
//
// The drain is exercised against a message source rather than a broker, and
// that is deliberate rather than convenient. The decisions worth pinning are
// the bound, the truncation marker and the stop condition, and the
// interesting one is fifty thousand messages deep -- a depth no container
// test would reach without spending a minute filling a stream to prove a
// branch that is three lines long. The broker half is eight lines adapting a
// consumer.
//
// What that leaves unproven is stated on LogArchive itself rather than
// implied away here: no test in this repository drives Info or FetchNoWait
// against a real nats-server, so these cover the request this file builds
// and every decision it makes about the answer, and not that a real broker
// answers the way the fakes do.
package api

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// fixedBatches returns a fetcher that hands back each batch in turn and
// then reports itself drained, which is how FetchNoWait says there is
// nothing more.
func fixedBatches(batches ...[][]byte) fetchBatch {
	i := 0
	return func(int) ([][]byte, error) {
		if i >= len(batches) {
			return nil, nil
		}
		b := batches[i]
		i++
		return b, nil
	}
}

func bodies(lines ...string) [][]byte {
	out := make([][]byte, 0, len(lines))
	for _, l := range lines {
		out = append(out, []byte(l))
	}
	return out
}

func TestDrainInto_WritesOneMessagePerLineAndStopsWhenDrained(t *testing.T) {
	var buf bytes.Buffer
	fetch := fixedBatches(
		bodies(`{"a":1}`, `{"a":2}`),
		bodies(`{"a":3}`),
	)

	if err := drainInto(context.Background(), &buf, fetch); err != nil {
		t.Fatalf("drainInto: %v", err)
	}

	want := "{\"a\":1}\n{\"a\":2}\n{\"a\":3}\n"
	if buf.String() != want {
		t.Errorf("drain wrote %q, want %q", buf.String(), want)
	}
	// No marker: this file is the whole record, and saying otherwise
	// would make every complete download claim to be partial.
	if strings.Contains(buf.String(), "truncated") {
		t.Error("a complete drain wrote a truncation marker")
	}
}

func TestDrainInto_AnEmptyArchiveWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	if err := drainInto(context.Background(), &buf, fixedBatches()); err != nil {
		t.Fatalf("drainInto: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("an empty archive wrote %q", buf.String())
	}
}

// TestDrainInto_SaysInTheFileWhenItStopsAtTheBound is the branch that only
// exists at fifty thousand messages, and the reason this drain is testable
// without a broker at all.
//
// The marker goes in the FILE rather than only in a server log, because the
// person holding the file is the one who needs to know it is partial. A
// truncated log that looks complete is how somebody concludes a run
// finished cleanly when it did not.
func TestDrainInto_SaysInTheFileWhenItStopsAtTheBound(t *testing.T) {
	// A fetcher that never runs out, so the bound is the only thing that
	// can stop it.
	endless := fetchBatch(func(n int) ([][]byte, error) {
		out := make([][]byte, n)
		for i := range out {
			out[i] = []byte(`{"m":"x"}`)
		}
		return out, nil
	})

	var buf bytes.Buffer
	if err := drainInto(context.Background(), &buf, endless); err != nil {
		t.Fatalf("drainInto: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	// Exactly the bound, plus the marker line.
	if len(lines) != maxArchivedLogMessages+1 {
		t.Fatalf("the drain wrote %d lines, want %d messages plus one marker",
			len(lines), maxArchivedLogMessages)
	}
	marker := lines[len(lines)-1]
	if !strings.Contains(marker, "truncated") {
		t.Errorf("the last line is %q, want a truncation marker", marker)
	}
	// The marker names the number, so a reader knows whether they are
	// missing one message or a million.
	if !strings.Contains(marker, strconv.Itoa(maxArchivedLogMessages)) {
		t.Errorf("the marker does not say where it stopped: %q", marker)
	}
	// And it is still NDJSON: a file cut short must stay parseable, which
	// is the whole reason this is not a JSON array.
	if !strings.HasPrefix(marker, "{") || !strings.HasSuffix(marker, "}") {
		t.Errorf("the marker is not a JSON object, so the file no longer parses line by line: %q", marker)
	}
}

// TestDrainInto_StopsAtTheBoundMidBatch covers the inner break: a batch that
// straddles the limit must not overrun it.
func TestDrainInto_StopsAtTheBoundMidBatch(t *testing.T) {
	// One batch far larger than the bound, so the only way to stop in the
	// right place is the check inside the loop.
	oversized := fetchBatch(func(int) ([][]byte, error) {
		out := make([][]byte, maxArchivedLogMessages+500)
		for i := range out {
			out[i] = []byte(`{"m":"x"}`)
		}
		return out, nil
	})

	var buf bytes.Buffer
	if err := drainInto(context.Background(), &buf, oversized); err != nil {
		t.Fatalf("drainInto: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != maxArchivedLogMessages+1 {
		t.Errorf("a batch straddling the bound wrote %d lines, want %d plus a marker",
			len(lines), maxArchivedLogMessages)
	}
}

func TestDrainInto_ReportsAFetchFailure(t *testing.T) {
	boom := errors.New("the broker went away")
	var buf bytes.Buffer

	err := drainInto(context.Background(), &buf, func(int) ([][]byte, error) {
		return nil, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("drainInto returned %v, want the fetch error", err)
	}
}

// TestDrainInto_ReportsAWriteFailure matters because the writer here is an
// HTTP response: a client that disconnects mid-download makes every
// subsequent write fail, and the drain must stop rather than spend the rest
// of the bound writing into a closed socket.
func TestDrainInto_ReportsAWriteFailure(t *testing.T) {
	boom := errors.New("client went away")
	err := drainInto(context.Background(), failingWriter{err: boom},
		fixedBatches(bodies(`{"a":1}`)))
	if !errors.Is(err, boom) {
		t.Fatalf("drainInto returned %v, want the write error", err)
	}
}

// TestDrainInto_HonoursACancelledContext proves a long drain can be stopped.
//
// Checked between batches rather than between messages: a cancelled request
// should end the download promptly, and a per-message check would cost a
// context read fifty thousand times to end it a few hundred messages sooner.
func TestDrainInto_HonoursACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var buf bytes.Buffer
	first := true
	err := drainInto(ctx, &buf, func(int) ([][]byte, error) {
		if first {
			first = false
			cancel()
			return bodies(`{"a":1}`), nil
		}
		t.Error("the drain fetched again after its context was cancelled")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("drainInto returned %v, want context.Canceled", err)
	}
	// What it managed to write before being cancelled is kept rather than
	// discarded: the response has already gone out, so there is nothing to
	// take back.
	if buf.String() != "{\"a\":1}\n" {
		t.Errorf("the cancelled drain wrote %q", buf.String())
	}
}

type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }

// TestNewLogArchive_IsUsableWithoutABroker guards the constructor, which is
// the one line of this file a composition root calls directly.
func TestNewLogArchive_IsUsableWithoutABroker(t *testing.T) {
	if NewLogArchive(nil) == nil {
		t.Fatal("NewLogArchive returned nil")
	}
	// A nil JetStream handle reports nothing retained rather than
	// panicking, which is the fail-closed direction: the control is
	// withheld instead of offering a download that cannot be produced.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Retained panicked with no broker: %v", r)
		}
	}()
	if NewLogArchive(nil).Retained(context.Background(), "job-1") {
		t.Error("an archive with no broker reports output retained")
	}
}

// TestLogArchive_WithNoBrokerReportsRatherThanPanics covers the other
// caller of the nil guard.
//
// A download attempted on a composition with no broker must answer with an
// error naming the job, not a panic that takes out the request: the handler
// has already committed a 200 and a Content-Disposition by the time Write
// is called, so a crash here hands the operator a zero-byte file with a
// confident name.
func TestLogArchive_WithNoBrokerReportsRatherThanPanics(t *testing.T) {
	var buf bytes.Buffer
	err := NewLogArchive(nil).WriteTo(context.Background(), &buf, "job-1")
	if err == nil {
		t.Fatal("WriteTo with no broker returned no error")
	}
	if !strings.Contains(err.Error(), "job-1") {
		t.Errorf("the failure does not name the job: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("a failed drain wrote %q", buf.String())
	}
}

// fakeConsumer answers the two questions logConsumer names, so the
// decisions around them can be exercised without a broker.
type fakeConsumer struct {
	pending  uint64
	infoErr  error
	fetchErr error
}

func (f fakeConsumer) Info(context.Context) (*jetstream.ConsumerInfo, error) {
	if f.infoErr != nil {
		return nil, f.infoErr
	}
	return &jetstream.ConsumerInfo{NumPending: f.pending}, nil
}

func (f fakeConsumer) FetchNoWait(int) (jetstream.MessageBatch, error) {
	return nil, f.fetchErr
}

// withConsumer builds an archive whose broker call is answered by open.
func withConsumer(open func(context.Context, string) (logConsumer, error)) *LogArchive {
	a := NewLogArchive(nil)
	a.open = open
	return a
}

// TestLogArchive_RetainedIsTheControlsGate covers the question the page
// asks before it draws the download at all.
//
// Every failure answers "nothing retained", which withholds the control.
// The failure an operator can act on is a missing link; a link that
// produces an empty file is one they only discover after forwarding it.
func TestLogArchive_RetainedIsTheControlsGate(t *testing.T) {
	cases := []struct {
		name string
		open func(context.Context, string) (logConsumer, error)
		want bool
	}{
		{
			name: "the broker still holds output",
			open: func(context.Context, string) (logConsumer, error) {
				return fakeConsumer{pending: 12}, nil
			},
			want: true,
		},
		{
			// The window has passed. Not an error: the job is fine, its
			// output simply is not kept forever.
			name: "the window has passed",
			open: func(context.Context, string) (logConsumer, error) {
				return fakeConsumer{pending: 0}, nil
			},
			want: false,
		},
		{
			name: "the consumer cannot be created",
			open: func(context.Context, string) (logConsumer, error) {
				return nil, errors.New("broker unreachable")
			},
			want: false,
		},
		{
			name: "the consumer cannot be inspected",
			open: func(context.Context, string) (logConsumer, error) {
				return fakeConsumer{infoErr: errors.New("timed out")}, nil
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := withConsumer(tc.open).Retained(context.Background(), "job-1"); got != tc.want {
				t.Errorf("Retained = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestLogArchive_WriteToNamesTheJobWhenTheBrokerRefuses covers the two
// failures that happen before any byte is written, which are the only ones
// a caller can still be told about: the handler has already sent a 200 and
// a Content-Disposition by the time the body starts.
func TestLogArchive_WriteToNamesTheJobWhenTheBrokerRefuses(t *testing.T) {
	cases := map[string]func(context.Context, string) (logConsumer, error){
		"the consumer cannot be created": func(context.Context, string) (logConsumer, error) {
			return nil, errors.New("broker unreachable")
		},
		"the first fetch fails": func(context.Context, string) (logConsumer, error) {
			return fakeConsumer{fetchErr: errors.New("stream gone")}, nil
		},
	}

	for name, open := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			err := withConsumer(open).WriteTo(context.Background(), &buf, "job-1")
			if err == nil {
				t.Fatal("WriteTo returned no error")
			}
			if !strings.Contains(err.Error(), "job-1") {
				t.Errorf("the failure does not name the job, so a log line cannot be correlated to it: %v", err)
			}
			if buf.Len() != 0 {
				t.Errorf("a failed drain wrote %q", buf.String())
			}
		})
	}
}

// fakeMsg is a jetstream.Msg carrying only a body.
//
// The interface is embedded and left nil so every method this drain does
// not call panics rather than quietly answering: a test double that
// silently returns zero values for twelve methods hides the day one of them
// starts being called.
type fakeMsg struct {
	jetstream.Msg
	body []byte
}

func (m fakeMsg) Data() []byte { return m.body }

// fakeBatch is a jetstream.MessageBatch over a fixed set of bodies.
type fakeBatch struct {
	bodies [][]byte
	err    error
}

func (b fakeBatch) Messages() <-chan jetstream.Msg {
	ch := make(chan jetstream.Msg, len(b.bodies))
	for _, body := range b.bodies {
		ch <- fakeMsg{body: body}
	}
	close(ch)
	return ch
}

func (b fakeBatch) Error() error { return b.err }

// batchConsumer hands back each batch in turn, then reports itself drained.
type batchConsumer struct {
	fakeConsumer
	batches  [][][]byte
	at       int
	batchErr error
}

func (c *batchConsumer) FetchNoWait(int) (jetstream.MessageBatch, error) {
	if c.at >= len(c.batches) {
		return fakeBatch{err: c.batchErr}, nil
	}
	b := c.batches[c.at]
	c.at++
	return fakeBatch{bodies: b, err: c.batchErr}, nil
}

// TestLogArchive_WriteToDrainsARealBatchShape covers the adapter between a
// JetStream batch and the drain, which is the half of WriteTo that is not
// the loop.
func TestLogArchive_WriteToDrainsARealBatchShape(t *testing.T) {
	consumer := &batchConsumer{batches: [][][]byte{
		{[]byte(`{"a":1}`), []byte(`{"a":2}`)},
		{[]byte(`{"a":3}`)},
	}}
	archive := withConsumer(func(context.Context, string) (logConsumer, error) {
		return consumer, nil
	})

	var buf bytes.Buffer
	if err := archive.WriteTo(context.Background(), &buf, "job-1"); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	want := "{\"a\":1}\n{\"a\":2}\n{\"a\":3}\n"
	if buf.String() != want {
		t.Errorf("WriteTo wrote %q, want %q", buf.String(), want)
	}
}

// TestLogArchive_WriteToReportsABatchLevelFailure covers the error a batch
// carries after its messages rather than instead of them.
//
// JetStream reports some failures on the batch rather than on the fetch, so
// a drain that read Messages() and ignored Error() would end early and
// silently, handing back a short file that looks complete.
func TestLogArchive_WriteToReportsABatchLevelFailure(t *testing.T) {
	consumer := &batchConsumer{
		batches:  [][][]byte{{[]byte(`{"a":1}`)}},
		batchErr: errors.New("consumer deleted mid-drain"),
	}
	archive := withConsumer(func(context.Context, string) (logConsumer, error) {
		return consumer, nil
	})

	var buf bytes.Buffer
	err := archive.WriteTo(context.Background(), &buf, "job-1")
	if err == nil {
		t.Fatal("WriteTo ignored a batch-level error, so a short file would look complete")
	}
	if !strings.Contains(err.Error(), "job-1") {
		t.Errorf("the failure does not name the job: %v", err)
	}
}

// TestLogArchive_AZeroValueAnswersRatherThanPanics pins the guard on both
// entry points.
//
// A zero-value LogArchive has no opener at all, which is a different state
// from one built with no broker, and both reach these methods: the registrar
// converts a nil *LogArchive to an untyped nil interface, but a struct
// literal somewhere would not. Answering is the fail-closed direction --
// the control is withheld and the download reports -- where a panic takes
// out the page that merely wanted to know whether to draw a link.
func TestLogArchive_AZeroValueAnswersRatherThanPanics(t *testing.T) {
	for name, archive := range map[string]*LogArchive{
		"a nil archive":        nil,
		"a zero-value archive": {},
	} {
		t.Run(name, func(t *testing.T) {
			if archive.Retained(context.Background(), "job-1") {
				t.Error("Retained reported output held by an archive with no opener")
			}
			var buf bytes.Buffer
			if err := archive.WriteTo(context.Background(), &buf, "job-1"); err == nil {
				t.Error("WriteTo returned no error from an archive with no opener")
			}
		})
	}
}

// fakeJetStream answers only the one call consumerFor makes. The interface
// is embedded and nil, so anything else panics rather than quietly
// succeeding.
type fakeJetStream struct {
	jetstream.JetStream
	gotStream   string
	gotFilter   string
	gotAck      jetstream.AckPolicy
	hasDeadline bool
	err         error
}

func (f *fakeJetStream) CreateOrUpdateConsumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error) {
	f.gotStream = stream
	f.gotFilter = cfg.FilterSubject
	f.gotAck = cfg.AckPolicy
	_, f.hasDeadline = ctx.Deadline()
	return nil, f.err
}

// TestLogArchive_ConsumerForAsksForTheRightThing covers the call that
// actually reaches a broker, which is the last piece of this file a
// container would otherwise be needed for.
//
// What it pins is the shape of the request rather than the broker's answer:
// the stream, the subject scoped to ONE job, the non-acknowledging policy
// that makes a download harmless to run while somebody is watching, and the
// deadline that stops a broker which accepts and then goes quiet from
// holding the response open.
func TestLogArchive_ConsumerForAsksForTheRightThing(t *testing.T) {
	js := &fakeJetStream{err: errors.New("not today")}
	archive := NewLogArchive(js)

	// The error is expected; what matters is what was asked for on the way.
	if archive.Retained(context.Background(), "job-42") {
		t.Error("Retained reported output when the consumer could not be created")
	}

	if js.gotStream != topology.StreamName {
		t.Errorf("asked stream %q, want %q", js.gotStream, topology.StreamName)
	}
	// Scoped to one job. A consumer that filtered nothing would let a
	// download of one job return another's output, which is the bug the
	// SSE viewer's own doc comment records having had.
	if want := topology.LogSubject("job-42"); js.gotFilter != want {
		t.Errorf("asked subject %q, want %q", js.gotFilter, want)
	}
	// Non-acknowledging, so a download tracks nothing and cannot advance a
	// position a live viewer depends on.
	if js.gotAck != jetstream.AckNonePolicy {
		t.Errorf("asked ack policy %v, want AckNonePolicy", js.gotAck)
	}
	if !js.hasDeadline {
		t.Error("the consumer request carries no deadline, so an accepting-then-silent broker holds the response open")
	}
}
