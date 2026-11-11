// Package telemetry: the fixed ways a span may report a failure.
package telemetry

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Outcome is one fixed way an operation can fail, as a span reports it.
//
// Its fields are unexported so that no package but this one can build an
// Outcome holding text of its own. That makes "a span's failure text is a
// fixed phrase" a property the compiler checks rather than one a reviewer
// has to notice, and adding a phrase is a reviewed diff to this file.
type Outcome struct {
	// category is the low-cardinality identifier recorded as error.type.
	category string

	// phrase is the span status description.
	phrase string
}

// The outcomes a span may report. The phrases are the three status
// descriptions internal/runner's handleMessage already used, unchanged, so a
// dashboard filter written against them keeps matching.
var (
	// OutcomeAdapterFailed is an adapter execution that ran and failed.
	OutcomeAdapterFailed = Outcome{category: "adapter_execution_failed", phrase: "adapter execution failed"}

	// OutcomeNoAdapter is a dispatch of a kind this runner cannot route.
	OutcomeNoAdapter = Outcome{category: "no_execution_adapter", phrase: "no execution adapter for this kind"}

	// OutcomePayloadVersion is a dispatch payload newer than this runner
	// reads.
	OutcomePayloadVersion = Outcome{category: "payload_version_unsupported", phrase: "dispatch payload version not supported"}
)

// outcomeUnclassified is what a zero Outcome is recorded as, so a caller
// that passes one still marks the span failed with a fixed phrase.
var outcomeUnclassified = Outcome{category: "unclassified", phrase: "unclassified failure"}

// declaredOutcomes lists every Outcome, for the policy's closed set of
// status descriptions. A new Outcome must be added here or the exporter
// withholds its phrase; TestDeclaredOutcomesCoverEveryOutcome holds the two
// in step.
func declaredOutcomes() []Outcome {
	return []Outcome{OutcomeAdapterFailed, OutcomeNoAdapter, OutcomePayloadVersion, outcomeUnclassified}
}

// RecordFailure marks span as failed with a fixed category and phrase.
//
// It replaces span.RecordError followed by span.SetStatus. RecordError
// copies err.Error() into an exception event, and an adapter error's text
// may quote device output, which is a value and so must not leave the
// process on a span. The error's text belongs in the masked log line beside
// the trace_id; the span says only which kind of failure it was.
func RecordFailure(span trace.Span, outcome Outcome) {
	if outcome.category == "" {
		outcome = outcomeUnclassified
	}
	span.SetAttributes(attribute.String("error.type", outcome.category))
	span.SetStatus(codes.Error, outcome.phrase)
}
