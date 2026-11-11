// Package telemetry: where the OpenTelemetry SDK's own error reports and
// internal log lines go.
package telemetry

import (
	"fmt"
	"io"
	"os"
	"sync"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/go-logr/logr"
)

// maxReportBytes caps one line the SDK's reports produce. An export error
// can quote the collector's whole reply, up to megabytes, and one line of
// that size is noise to an operator and a cost to every log pipeline.
const maxReportBytes = 2048

// truncatedMark ends a line that was cut at maxReportBytes, so a reader
// knows text is missing rather than that the report ended there.
const truncatedMark = " [truncated]"

// reportWriter writes the SDK's reports as single masked, escaped, capped
// lines. The masked error handler and the masked logger share one, so the
// two channels the SDK reports through follow one rule.
type reportWriter struct {
	// mu keeps two reports from interleaving inside one line: the masker
	// masks each Write as a unit, and a line split across two writers'
	// calls would be masked in halves.
	mu sync.Mutex
	// out is where the lines go, os.Stderr in production.
	out io.Writer
}

// newReportWriter returns a writer to w, or to os.Stderr when w is nil.
func newReportWriter(w io.Writer) *reportWriter {
	if w == nil {
		w = os.Stderr
	}
	return &reportWriter{out: w}
}

// line masks text against the shared ruleset first, while every secret is
// still whole for its pattern to match, then escapes every character a
// terminal or a line-oriented log reader would act on (newlines included),
// then caps the result, and writes it as one line. A failure to write is
// dropped: this is the error path of the error path, and nothing better is
// left to report it to.
func (r *reportWriter) line(text string) {
	text = termsafe.EscapeLine(redact.Shared().Text(nil, text))
	if len(text) > maxReportBytes {
		cut := maxReportBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + truncatedMark
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = io.WriteString(r.out, "telemetry: "+text+"\n")
}

// maskedErrorHandler is the otel.ErrorHandler Setup installs. The SDK
// reports a failed export, a dropped span and the like through it. The
// default handler already wrote through log.Print, which each binary masks
// with log.SetOutput; what this one adds is the line discipline: a
// collector's reply can run to megabytes and carry newlines and terminal
// escapes, and written raw it could forge log lines of its own.
type maskedErrorHandler struct {
	// w writes the lines.
	w *reportWriter
}

// newMaskedErrorHandler returns a handler writing to w, or to os.Stderr when
// w is nil.
func newMaskedErrorHandler(w io.Writer) *maskedErrorHandler {
	return &maskedErrorHandler{w: newReportWriter(w)}
}

// Handle writes err as one bounded line.
func (h *maskedErrorHandler) Handle(err error) {
	if err == nil {
		return
	}
	h.w.line(err.Error())
}

// newMaskedLogger returns the logger Setup installs with otel.SetLogger.
//
// The SDK's internal logger is a second channel beside the error handler,
// and the one the default leaves unmasked: a stdr logger built on its own
// log.New(os.Stderr), which the log.SetOutput each binary installs never
// reaches. otlptracehttp reports a malformed OTEL_EXPORTER_OTLP_HEADERS
// entry, a bad endpoint URL and the like through it, with the raw value
// attached as a key-value pair. This logger writes only the message and the
// error's Go type, masked and bounded, and drops every key-value pair.
func newMaskedLogger(w io.Writer) logr.Logger {
	return logr.New(&maskedSink{w: newReportWriter(w)})
}

// maskedSink is the logr.LogSink behind newMaskedLogger.
type maskedSink struct {
	// w writes the lines.
	w *reportWriter
}

// Init receives the caller depth logr computes; this sink records no call
// site, so it has nothing to do.
func (s *maskedSink) Init(logr.RuntimeInfo) {}

// Enabled reports whether a message at level is written: the SDK's errors
// and its level-0 messages, which is what the default logger shows. Its
// warnings, information and debug lines (levels 1, 4 and 8) stay off.
func (s *maskedSink) Enabled(level int) bool {
	return level <= 0
}

// Info writes msg, dropping keysAndValues, which hold the values.
func (s *maskedSink) Info(_ int, msg string, _ ...any) {
	s.w.line(msg)
}

// Error writes msg and err's Go type, dropping keysAndValues and err's
// text, either of which can quote the value that failed to parse.
func (s *maskedSink) Error(err error, msg string, _ ...any) {
	s.w.line(fmt.Sprintf("%s (%T)", msg, err))
}

// WithValues returns the sink unchanged: the values are dropped either way.
func (s *maskedSink) WithValues(...any) logr.LogSink {
	return s
}

// WithName returns the sink unchanged: a logger name adds nothing an
// operator needs beside the "telemetry:" prefix.
func (s *maskedSink) WithName(string) logr.LogSink {
	return s
}
