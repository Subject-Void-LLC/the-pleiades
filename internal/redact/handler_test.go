package redact_test

import (
	"bytes"
	"log"
	"log/slog"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// The tests in this file are the executable form of an ordering constraint
// that was recorded in the specification before either mechanism existed:
// apply the masking ruleset through slog.HandlerOptions.ReplaceAttr, not
// inside a wrapping slog.Handler's Handle.
//
// A wrapping handler is the obvious design and the wrong one, and the two
// reasons are both invisible until a specific thing is tried. These tests
// try those specific things. Each one names, in its own comment, why a
// wrapper fails it, so the constraint survives as a fact somebody can
// verify rather than as advice somebody has to trust.

// newTestLogger returns a logger writing masked JSON into a buffer.
func newTestLogger(t *testing.T) (*slog.Logger, *bytes.Buffer, *redact.Masker) {
	t.Helper()

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}

	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, m.HandlerOptions(slog.LevelDebug))), &buf, m
}

// TestReplaceAttrMasksLoggerWithAttrs is the first reason a wrapping
// handler cannot do this job.
//
// slog.Logger.With returns a logger whose handler has already had WithAttrs
// called on it, and the standard library's commonHandler.withAttrs
// pre-formats those attributes into a byte buffer at that moment. By the
// time a wrapping handler's Handle runs, they are not attributes it can
// inspect: they are bytes already committed to the output format. A wrapper
// would pass this call through untouched and write the password verbatim.
//
// ReplaceAttr runs during that pre-formatting, so it sees them.
func TestReplaceAttrMasksLoggerWithAttrs(t *testing.T) {
	t.Parallel()

	const secret = "hunter2-with-attrs"

	logger, buf, _ := newTestLogger(t)
	logger.With("password", secret).Info("connecting")

	assertAbsent(t, buf.String(), secret)
	assertPresent(t, buf.String(), redact.Marker)
}

// TestReplaceAttrMasksNestedLoggerWithAttrs covers the same mechanism one
// level deeper, where a logger is decorated more than once. Each With call
// pre-formats again, so a mechanism that only caught the last one would
// leak everything added earlier.
func TestReplaceAttrMasksNestedLoggerWithAttrs(t *testing.T) {
	t.Parallel()

	const first = "first-secret-value"
	const second = "second-secret-value"

	logger, buf, _ := newTestLogger(t)
	logger.
		With("token", first).
		With("device", "router-1").
		With("api_key", second).
		Info("dispatching")

	assertAbsent(t, buf.String(), first)
	assertAbsent(t, buf.String(), second)
	// The non-secret attribute must survive: masking that deleted context
	// would make the logs useless and nobody would keep it on.
	assertPresent(t, buf.String(), "router-1")
}

// TestReplaceAttrMasksTheMessageBody is the second reason a wrapping
// handler cannot do this job.
//
// A wrapper sees slog.Record.Message as an opaque string on the Record
// struct. It could mask it, but only by rebuilding the Record, and more to
// the point the message is the sole channel for output bridged from the
// standard library's log package: a log.Printf that has been redirected
// into slog arrives entirely inside Message with no attributes at all.
//
// ReplaceAttr's documented contract passes the built-in msg attribute
// through it, with an empty groups slice, which is what makes the message
// body reachable by the same rules as everything else.
func TestReplaceAttrMasksTheMessageBody(t *testing.T) {
	t.Parallel()

	const secret = "sup3rs3cret-in-the-message"

	logger, buf, m := newTestLogger(t)
	m.Literals().Add(secret)

	logger.Info("ssh failed for " + secret + " on router-1")

	assertAbsent(t, buf.String(), secret)
	assertPresent(t, buf.String(), "router-1")
}

// TestReplaceAttrMasksGroupedAttrs proves the ruleset reaches inside a
// group.
//
// The standard library recurses into a group's leaves and calls ReplaceAttr
// for each one, so Attr returns a group untouched and relies on that
// recursion rather than walking the group itself. This test is what proves
// the assumption, since a wrong one would silently skip every grouped
// attribute.
func TestReplaceAttrMasksGroupedAttrs(t *testing.T) {
	t.Parallel()

	const secret = "grouped-secret-value"

	logger, buf, _ := newTestLogger(t)
	logger.Info("connecting",
		slog.Group("credential",
			slog.String("username", "admin"),
			slog.String("password", secret),
		),
	)

	assertAbsent(t, buf.String(), secret)
	assertPresent(t, buf.String(), "admin")
}

// TestReplaceAttrMasksGroupedAttrsAddedWithWithGroup covers the two
// mechanisms together, which is the combination most likely to fall
// through a gap between them.
func TestReplaceAttrMasksGroupedAttrsAddedWithWithGroup(t *testing.T) {
	t.Parallel()

	const secret = "withgroup-secret-value"

	logger, buf, _ := newTestLogger(t)
	logger.WithGroup("cred").With("client_secret", secret).Info("authenticating")

	assertAbsent(t, buf.String(), secret)
}

// TestReplaceAttrRunsAfterLogValuerResolution covers the third property the
// specification's ordering note calls out.
//
// The standard library resolves a slog.LogValuer before calling
// ReplaceAttr, with its own comment saying so, which means a type cannot
// route around the ruleset by hiding its secret behind a LogValue method.
// That matters here because internal/credential.Credential is exactly such
// a type.
func TestReplaceAttrRunsAfterLogValuerResolution(t *testing.T) {
	t.Parallel()

	const secret = "logvaluer-secret-value"

	logger, buf, _ := newTestLogger(t)
	logger.Info("connecting", "password", secretValuer{value: secret})

	assertAbsent(t, buf.String(), secret)
}

// secretValuer hides a value behind slog.LogValuer, the way a domain type
// with its own redaction would.
type secretValuer struct{ value string }

// LogValue returns the wrapped value, deliberately unredacted, so this test
// measures the handler's masking rather than the type's own.
func (s secretValuer) LogValue() slog.Value { return slog.StringValue(s.value) }

// TestReplaceAttrKeepsTheBuiltInAttributes guards against the sharpest
// failure available here.
//
// Returning an Attr with an empty key tells the standard library to drop
// the attribute entirely. A masking function that got that wrong would not
// leak anything; it would silently delete the timestamp, the level, or the
// message from every log line, and the damage would be discovered during
// an incident.
func TestReplaceAttrKeepsTheBuiltInAttributes(t *testing.T) {
	t.Parallel()

	logger, buf, _ := newTestLogger(t)
	logger.Warn("something happened", "device", "router-1")

	out := buf.String()
	for _, want := range []string{`"time":`, `"level":"WARN"`, `"msg":"something happened"`, `"device":"router-1"`} {
		assertPresent(t, out, want)
	}
}

// TestPatternRulesReachTheLogWithoutAnyRegisteredSecret proves the by-shape
// channel: a PEM block is masked even though nothing told this process it
// was a secret. That is the channel that catches a key belonging to
// somebody else's subsystem.
func TestPatternRulesReachTheLogWithoutAnyRegisteredSecret(t *testing.T) {
	t.Parallel()

	const pem = "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----"

	logger, buf, _ := newTestLogger(t)
	logger.Error("failed to parse key", "detail", pem)

	assertAbsent(t, buf.String(), "b3BlbnNzaC1rZXktdjEAAAAA")
}

// TestWriterMasksStandardLibraryLogOutput covers the corollary: a
// log.Fatalf bypasses slog entirely and still reaches an operator's
// terminal, so every terminal writer has to carry the same ruleset.
//
// This is the mechanism cmd/ binaries install with log.SetOutput.
func TestWriterMasksStandardLibraryLogOutput(t *testing.T) {
	t.Parallel()

	const secret = "fatal-path-secret-value"

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}
	m.Literals().Add(secret)

	var buf bytes.Buffer
	// A private logger rather than log.SetOutput, so this test does not
	// mutate process-wide state that runs in parallel with others.
	l := log.New(m.Writer(&buf), "", 0)
	l.Printf("could not connect using %s", secret)

	assertAbsent(t, buf.String(), secret)
}

// TestWriterReportsTheCallersByteCount pins an io.Writer contract detail
// that is easy to get wrong and fails loudly when it is.
//
// Masking changes length. io.Writer reads a short count as an error, so a
// decorator reporting post-masking bytes would make every write containing
// a secret look like a failed write to its caller.
func TestWriterReportsTheCallersByteCount(t *testing.T) {
	t.Parallel()

	const secret = "count-check-secret-value"

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}
	m.Literals().Add(secret)

	var buf bytes.Buffer
	w := m.Writer(&buf)

	payload := []byte("value is " + secret + "\n")
	n, err := w.Write(payload)
	if err != nil {
		t.Fatalf("Write() failed: %v", err)
	}
	if n != len(payload) {
		t.Errorf("Write() = %d, want %d, the caller's own byte count", n, len(payload))
	}
	if buf.Len() == len(payload) {
		t.Error("the output was the same length as the input, so nothing was masked")
	}
}

// assertAbsent fails if haystack contains needle, reporting the haystack so
// a failure is diagnosable. The needle is a test-local constant rather than
// a real credential, so printing it here discloses nothing.
func assertAbsent(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("output still contains %q:\n%s", needle, haystack)
	}
}

// assertPresent fails if haystack does not contain needle.
func assertPresent(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("output does not contain %q:\n%s", needle, haystack)
	}
}
