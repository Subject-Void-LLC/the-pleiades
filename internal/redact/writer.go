package redact

import (
	"io"
	"sync"
)

// Writer decorates w so every write is masked first.
//
// This exists for the terminal writers slog does not own. The standard
// library's log package is the important one: a log.Fatalf or a log.Printf
// bypasses slog entirely and still reaches an operator's terminal, and
// cmd/runner alone had a dozen such call sites when this was written. The
// package comment's corollary applies here, that every terminal writer must
// carry the same ruleset including any os.Stderr fallback, or the failure
// path emits unmasked exactly when things are going wrong.
//
// # The boundary limitation, stated rather than papered over
//
// Each Write call is masked as a unit. A secret split across two Write
// calls is not caught, because the two halves are never in this function at
// the same time.
//
// That is correct for the callers this is built for and wrong for a general
// stream scrubber, so it is worth being precise about which is which. The
// standard library's log package assembles a whole line and issues exactly
// one Write per line, so a secret in a log line is always entirely inside
// one call. A subprocess's piped stdout is the opposite: it arrives in
// arbitrary chunks, and a secret can straddle any of them.
//
// Line buffering was considered and rejected. Holding bytes until a newline
// would fix the split-line case and break a worse one: the PEM private key
// rule matches across newlines by design, and a scrubber that emitted each
// line as it completed could never match a multi-line block at all. Trading
// a whole-key leak for a split-token leak is a bad trade. A general stream
// scrubber needs a sliding window sized to the longest pattern, which is a
// real piece of work and belongs to whichever phase first pipes a
// subprocess through here. Nothing does today: the adapters mask their
// captured output with Masker.Text, on complete strings, where no boundary
// exists.
func (m *Masker) Writer(w io.Writer) io.Writer {
	return &maskingWriter{masker: m, out: w}
}

// maskingWriter is the io.Writer Writer returns.
type maskingWriter struct {
	masker *Masker
	out    io.Writer

	// mu serializes writes. The standard library's log package holds its
	// own lock, so it would not need this, but an io.Writer handed to
	// several goroutines is an ordinary thing to do and interleaved
	// partial writes would corrupt output rather than only reorder it.
	mu sync.Mutex
}

// Write masks p and forwards the result.
//
// It reports len(p) rather than the number of bytes actually forwarded.
// Masking changes length, and io.Writer's contract reads a short count as
// an error, so returning the post-masking length would make every write
// containing a secret look like a failure to the caller.
func (w *maskingWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	masked := w.masker.Text(nil, string(p))
	if _, err := w.out.Write([]byte(masked)); err != nil {
		// The caller cannot act on a partial count here, since the count
		// is in masked bytes and the caller thinks in original ones.
		// Report nothing written alongside the real error.
		return 0, err
	}
	return len(p), nil
}
