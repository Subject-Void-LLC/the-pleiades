package redact_test

import (
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// The benchmarks AGENTS.md requires before a Release Gate.
//
// The number that matters here is BenchmarkAttrWithRuleset against
// BenchmarkAttrBaseline. Masking runs on every attribute of every log line
// in every binary, which is the hottest path this phase adds anything to,
// so the per-line cost has to be a measurement rather than a worry. If
// masking made logging visibly expensive somebody would eventually turn it
// off, and a masking control that gets turned off protects nothing.
//
// The industry comparison AGENTS.md asks for is awkward here and worth
// saying so plainly rather than inventing a figure: AWX has no equivalent
// component to measure. Its masking happens inside the Python callback
// plugin and inside the Django ORM's own field encryption, on entirely
// different paths, so any ratio would compare two things that do not do the
// same job. What is comparable is the baseline in this file: the same
// handler with no ReplaceAttr at all, which is what this platform did
// before Phase 22 and what the cost is measured against.
//
// Measured on an Intel i7-8700K, 300,000 iterations, three runs, stable to
// within one percent:
//
//	                          ns/op    B/op   allocs   vs baseline
//	AttrBaseline                976       0        0             1x
//	AttrWithRuleset            3410       8        1           3.5x
//	AttrWithLargeLiteralSet   50900       8        1            52x
//	TextWithNoLiterals          884       0        0             -
//	TextWithExplicitSecrets    1044      48        1             -
//
// A masked log line costs about 2.4 microseconds more than an unmasked one
// and allocates 8 bytes. That is the number worth defending, because a
// masking control expensive enough to notice is one somebody eventually
// turns off, and one that is off protects nothing.
//
// It did not start there. The first working version measured 25,207 ns/op
// with 46 allocations, a 26x tax on every log line in every binary, and the
// large-literal case ran at 424,033 ns/op. Three changes closed the gap,
// each recorded beside the code it changed:
//
//   - A prefilter on every pattern rule (rules.json, Masker.Text). A line
//     with no secret shape in it now skips all five regular expressions
//     after one lowercase pass and a few substring tests. This alone took
//     TextWithNoLiterals from 17,025 ns/op to 884.
//   - A cached sorted snapshot of the literal set (Literals.snapshotShared).
//     The set was being rebuilt and re-sorted once per masked string; it is
//     now sorted once per mutation.
//   - A zero-allocation pre-pass in maskLiterals. Almost every string
//     contains no secret, and the old code allocated a claim table and
//     built a new string before discovering that.
//
// AttrWithLargeLiteralSet is deliberately left slow and deliberately
// measured. A thousand simultaneously live secrets in one process is far
// outside any real deployment, and the 52x there is the honest shape of
// substring masking: the scan is linear in the size of the set, and no
// amount of prefiltering changes that. The number exists so an operator who
// is not calling Forget can see the cost before it becomes an incident.

// benchLine is a realistic log line: a device name, a status, and a message
// long enough that scanning it is not free.
const benchLine = "ssh dial failed for device router-core-07 in group edge-west after 3 attempts, last error was connection refused"

// newBenchMasker builds the masker once per benchmark, with a populated
// literal set, since an empty set would measure the early return rather
// than the scrub.
func newBenchMasker(b *testing.B, literals int) *redact.Masker {
	b.Helper()

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		b.Fatalf("NewMasker() failed: %v", err)
	}
	for i := 0; i < literals; i++ {
		m.Literals().Add(fmt.Sprintf("secret-value-%06d", i))
	}
	return m
}

// BenchmarkAttrBaseline is the control: the same handler with no
// ReplaceAttr, which is what every binary in this repository did before
// Phase 22.
func BenchmarkAttrBaseline(b *testing.B) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		logger.Info(benchLine, "device", "router-core-07", "attempt", 3)
	}
}

// BenchmarkAttrWithRuleset measures the real cost with a small literal set,
// which is the steady state for a controller running a handful of
// concurrent jobs.
func BenchmarkAttrWithRuleset(b *testing.B) {
	m := newBenchMasker(b, 8)
	logger := slog.New(slog.NewJSONHandler(io.Discard, m.HandlerOptions(slog.LevelDebug)))

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		logger.Info(benchLine, "device", "router-core-07", "attempt", 3)
	}
}

// BenchmarkAttrWithLargeLiteralSet measures the cost at 1000 live secrets,
// far past any real deployment, because the scrub is linear in the size of
// the set and a caller who never calls Forget should be able to see what
// that costs before it becomes an incident.
func BenchmarkAttrWithLargeLiteralSet(b *testing.B) {
	m := newBenchMasker(b, 1000)
	logger := slog.New(slog.NewJSONHandler(io.Discard, m.HandlerOptions(slog.LevelDebug)))

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		logger.Info(benchLine, "device", "router-core-07", "attempt", 3)
	}
}

// BenchmarkTextWithNoLiterals isolates the pattern rules, which run on
// every masked string whether or not any secret is registered. This is the
// floor: the cost a process pays for masking before it has dispatched
// anything at all.
func BenchmarkTextWithNoLiterals(b *testing.B) {
	m := newBenchMasker(b, 0)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = m.Text(nil, benchLine)
	}
}

// BenchmarkTextWithExplicitSecrets measures the shape every migrated call
// site uses: an adapter or executor masking its own captured output against
// the handful of secrets it just used.
func BenchmarkTextWithExplicitSecrets(b *testing.B) {
	m := newBenchMasker(b, 0)
	secrets := []string{"hunter2-password-value", "passphrase-value-here", "an-api-token-value"}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = m.Text(secrets, benchLine)
	}
}
