// Tests for how RunOnce hands over a container's output: never written to
// once RunOnce has returned, and a writer that refuses it ends the run.
package containerengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// slowWriter takes each write slowly and holds no lock, as a caller's
// buffer that it reads once RunOnce returns needs none.
type slowWriter struct {
	delay time.Duration
	buf   bytes.Buffer
}

// Write keeps p after a delay.
func (w *slowWriter) Write(p []byte) (int, error) {
	time.Sleep(w.delay)
	return w.buf.Write(p)
}

// TestRunOnce_NothingIsWrittenOnceItReturns: a writer slower than the
// grace RunOnce gives a container's output after the container ends is
// never written to once RunOnce has returned, so a caller may read what
// it holds without a lock (the race detector holds that), and the result
// says the output was cut short.
func TestRunOnce_NothingIsWrittenOnceItReturns(t *testing.T) {
	was := outputGrace
	outputGrace = 50 * time.Millisecond
	t.Cleanup(func() { outputGrace = was })
	var parts []any
	for i := 0; i < 20; i++ {
		parts = append(parts, byte(1), fmt.Sprintf("line %d\n", i))
	}
	e := &runEngine{imagePresent: true, stream: frames(parts...)}
	w := &slowWriter{delay: 20 * time.Millisecond}
	res, err := runSession(t, e).RunOnce(context.Background(), RunOnceOptions{Image: runImage, Cmd: []string{"report"}, Timeout: time.Minute, Output: w})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OutputCut {
		t.Errorf("an output cut short is not said to be: %+v, %d bytes", res, w.buf.Len())
	}
	held := w.buf.Len()
	time.Sleep(200 * time.Millisecond)
	if w.buf.Len() != held {
		t.Errorf("the output was written to after RunOnce returned: %d bytes, then %d", held, w.buf.Len())
	}
}

// refusingWriter refuses a write past max bytes, as a caller bounding a
// report does.
type refusingWriter struct{ max, n int }

// Write keeps p, or refuses it past max.
func (w *refusingWriter) Write(p []byte) (int, error) {
	if w.n+len(p) > w.max {
		return 0, errors.New("past the bound")
	}
	w.n += len(p)
	return len(p), nil
}

// TestRunOnce_AWriterThatRefusesTheOutputEndsTheRun: once the writer
// refuses the output (past its bound), nothing reads what the container
// writes, and it would block writing until its timeout; RunOnce kills it
// at once and returns the refusal.
func TestRunOnce_AWriterThatRefusesTheOutputEndsTheRun(t *testing.T) {
	e := &runEngine{imagePresent: true, stream: frames(byte(1), strings.Repeat("x", 4096)), waitFor: 3 * time.Second}
	start := time.Now()
	_, err := runSession(t, e).RunOnce(context.Background(), RunOnceOptions{Image: runImage, Cmd: []string{"report"}, Timeout: time.Minute, Output: &refusingWriter{max: 1024}})
	if err == nil || !strings.Contains(err.Error(), "past the bound") {
		t.Errorf("a refused output = %v", err)
	}
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Errorf("the run went on for %v after its output was refused", took)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.killed != 1 || e.removed != 1 {
		t.Errorf("killed %d, removed %d; want once each", e.killed, e.removed)
	}
}
