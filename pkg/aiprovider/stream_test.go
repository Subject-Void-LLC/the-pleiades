// What the streamed-answer reader refuses before it holds it.
package aiprovider

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// endlessEvent streams data lines of one event and never the blank line
// that ends it, until it has sent lines of them.
type endlessEvent struct {
	line  string
	lines int
	sent  int
}

// Read serves one data line per call, up to lines of them.
func (e *endlessEvent) Read(p []byte) (int, error) {
	if e.sent >= e.lines {
		return 0, io.EOF
	}
	e.sent++
	return copy(p, e.line), nil
}

// TestSSEEvents_AnEventOverTheLimitIsRefused: one event whose data lines
// add up past the limit is refused, rather than held until the call's time
// runs out; the refusal comes long before the stream ends.
func TestSSEEvents_AnEventOverTheLimitIsRefused(t *testing.T) {
	line := "data: " + strings.Repeat("x", 1<<20) + "\n"
	src := &endlessEvent{line: line, lines: 1000}
	err := sseEvents(src, func(string, []byte) error {
		t.Error("an unfinished event was handed over")
		return nil
	})
	var api *APIError
	if !errors.As(err, &api) || !api.Streamed {
		t.Fatalf("err = %v, want a streamed refusal", err)
	}
	if src.sent > 20 {
		t.Errorf("read %d MiB before refusing", src.sent)
	}
}
