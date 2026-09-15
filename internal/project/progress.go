// This file is the live output of a running clone: the fan-out that lets
// somebody watch a sync happen instead of waiting for a badge to change.
//
// # Why this is in memory rather than on the broker
//
// A job's log stream goes through JetStream, because a job runs on a runner
// in another process and its output has to cross a machine boundary to be
// read. A project sync does not: it runs on this process's own Runner, so
// its output originates exactly where a reader's request lands. Putting it
// on the broker would add a subject, a consumer and a durability story to
// move bytes from one goroutine to another in the same binary.
//
// The honest cost of that choice is the one the Runner already has: in a
// multi-replica deployment a reader whose request lands on a replica that is
// not running the clone sees no output. It is the same limitation, in the
// same place, rather than a new one.
package project

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

const (
	// progressKeep bounds the lines retained for a reader who opens the
	// page mid-clone. Enough to show what is happening, bounded so a long
	// clone cannot grow without limit.
	progressKeep = 200

	// subscriberBuffer is how far a reader may fall behind before lines are
	// dropped for them. A clone must never block on a slow browser.
	subscriberBuffer = 128

	// maxPartialLine caps an unterminated line, so output with no newline
	// in it cannot grow the buffer without bound.
	maxPartialLine = 4096
)

// Progress fans a running clone's output out to whoever is watching it.
//
// One stream per project, because a project has one working tree and
// therefore one clone at a time, which BeginSync's claim is what guarantees.
type Progress struct {
	mu      sync.Mutex
	streams map[int]*progressStream
}

// progressStream is one clone's output and its readers.
type progressStream struct {
	lines []string
	subs  map[chan string]struct{}
	done  bool
}

// NewProgress returns an empty fan-out.
func NewProgress() *Progress {
	return &Progress{streams: map[int]*progressStream{}}
}

// Writer returns the writer a clone of projectID reports into, starting a
// fresh stream.
//
// Starting fresh is the point: the previous attempt's output belongs to the
// previous attempt, and a reader watching THIS clone must not be shown the
// last one's lines as though they were live. Readers of the previous stream
// are closed, because what they were watching has ended.
func (p *Progress) Writer(projectID int) io.Writer {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closeLocked(projectID)
	p.streams[projectID] = &progressStream{subs: map[chan string]struct{}{}}
	return &progressWriter{progress: p, projectID: projectID}
}

// Subscribe returns this project's clone output: the lines already seen,
// then live ones, with the channel closed when the clone finishes.
//
// A project with no clone running replays whatever the last one left and
// closes at once, so a reader who arrives late sees the tail rather than an
// empty page that looks like an outage.
func (p *Progress) Subscribe(projectID int) (<-chan string, func()) {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch := make(chan string, subscriberBuffer)
	st := p.streams[projectID]
	if st != nil {
		for _, line := range st.lines {
			select {
			case ch <- line:
			default:
			}
		}
	}
	if st == nil || st.done {
		close(ch)
		return ch, func() {}
	}

	st.subs[ch] = struct{}{}
	return ch, func() { p.unsubscribe(projectID, ch) }
}

// Finish ends a clone's stream, closing every reader watching it. The lines
// are kept, so a reader arriving afterwards still sees how it went.
func (p *Progress) Finish(projectID int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeLocked(projectID)
}

// emit records one line and hands it to every reader.
func (p *Progress) emit(projectID int, line string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	st := p.streams[projectID]
	if st == nil || st.done {
		return
	}
	st.lines = append(st.lines, line)
	if len(st.lines) > progressKeep {
		st.lines = st.lines[len(st.lines)-progressKeep:]
	}
	for ch := range st.subs {
		select {
		case ch <- line:
		default:
			// A reader that cannot keep up loses this line rather than
			// stalling the clone. The clone is what matters.
		}
	}
}

// unsubscribe drops one reader. The channel is closed by whoever removes it
// from the set, under the lock, so it is closed exactly once however a
// reader and a finishing clone race.
func (p *Progress) unsubscribe(projectID int, ch chan string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	st := p.streams[projectID]
	if st == nil {
		return
	}
	if _, ok := st.subs[ch]; ok {
		delete(st.subs, ch)
		close(ch)
	}
}

// closeLocked ends a stream and its readers. The caller holds the lock.
func (p *Progress) closeLocked(projectID int) {
	st := p.streams[projectID]
	if st == nil {
		return
	}
	st.done = true
	for ch := range st.subs {
		delete(st.subs, ch)
		close(ch)
	}
}

// progressWriter turns a clone's byte stream into lines.
//
// It splits on carriage returns as well as newlines, because that is how git
// reports progress: an in-place counter rewrites one line with \r rather
// than ending it, and a splitter that only knew \n would show nothing at all
// until the transfer finished.
type progressWriter struct {
	progress  *Progress
	projectID int

	mu  sync.Mutex
	buf []byte
}

func (w *progressWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buf = append(w.buf, b...)
	for {
		i := bytes.IndexAny(w.buf, "\r\n")
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
		if line != "" {
			w.progress.emit(w.projectID, line)
		}
	}
	if len(w.buf) > maxPartialLine {
		w.buf = w.buf[:0]
	}
	return len(b), nil
}
