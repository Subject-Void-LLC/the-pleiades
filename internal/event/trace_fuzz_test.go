package event_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/nats-io/nats.go"
)

// FuzzExtractTraceContext drives arbitrary header values through the trace
// context parser.
//
// This is the one place in the bus path that parses attacker-influenced
// bytes before anything else looks at a message: a publisher on a subject
// this process consumes chooses these header values, and extraction
// happens before the payload is even unmarshaled. A panic here would take
// down a consumer loop on demand, so "never panics, and never invents a
// valid span context out of garbage" is the property under test.
func FuzzExtractTraceContext(f *testing.F) {
	f.Add("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	f.Add("traceparent", "")
	f.Add("traceparent", "not-a-traceparent")
	f.Add("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7")
	f.Add("traceparent", "ff-ffffffffffffffffffffffffffffffff-ffffffffffffffff-ff")
	f.Add("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	f.Add("tracestate", "vendor=value")
	f.Add("baggage", "key=value;prop=1")
	f.Add("", "")

	f.Fuzz(func(t *testing.T, key, value string) {
		hdr := nats.Header{key: []string{value}}
		event.ExtractTraceContext(context.Background(), hdr)

		// Injecting into the same header afterwards must also stay safe:
		// Set has to reconcile a case-variant key the fuzzer may have
		// planted.
		event.InjectTraceContext(context.Background(), hdr)
	})
}
