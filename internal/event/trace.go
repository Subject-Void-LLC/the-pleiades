// This file carries W3C trace context across the event bus, the boundary
// PLAN.md Section 19 names explicitly: "Trace IDs must propagate through
// the entire lifecycle (API Request -> Event Bus -> Lock Manager -> Runner
// Execution -> Device Result) to provide a single distributed trace for
// every action."
//
// The Event envelope's own TraceID field is not enough for that. A trace
// ID alone cannot parent a span: a child needs its parent's span ID and
// the sampling flag as well, which is exactly what the W3C `traceparent`
// header encodes and a bare ID does not. The envelope field stays as the
// human-readable audit value; these functions carry the machine-readable
// context alongside it, in the message's own headers, where it is
// out of band from the payload and readable by a non-Go consumer too.
package event

import (
	"context"
	"strings"

	"github.com/SubjectVoidLLC/the-pleiades/internal/telemetry"
	"github.com/nats-io/nats.go"
)

// wirePropagator is the encoding for trace context on the bus. It is
// telemetry.Propagator's, not a second choice made here: PLAN.md Section
// 25's build-once rule means the HTTP edge and the bus must agree on one
// wire format, or a trace stops at whichever boundary disagrees.
var wirePropagator = telemetry.Propagator()

// natsHeaderCarrier adapts nats.Header to OpenTelemetry's TextMapCarrier.
//
// nats.Header and http.Header share an underlying type, so a plain
// conversion compiles and appears to work. It is still wrong: http.Header's
// own Get and Set canonicalize keys ("traceparent" becomes "Traceparent"),
// while nats.Header does not canonicalize on the wire. Two Go processes
// would agree, and a consumer in any other language looking for the
// lowercase key the W3C specification mandates would silently find
// nothing. This carrier writes the exact lowercase key and reads
// case-insensitively, so it interoperates in both directions.
type natsHeaderCarrier nats.Header

// Get returns the first value for key, matching case-insensitively.
func (c natsHeaderCarrier) Get(key string) string {
	if values, ok := c[key]; ok && len(values) > 0 {
		return values[0]
	}
	for k, values := range c {
		if strings.EqualFold(k, key) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

// Set stores value under the exact key given, replacing any existing
// entry that differs only by case so a re-injection cannot leave two
// conflicting spellings on one message.
func (c natsHeaderCarrier) Set(key, value string) {
	for k := range c {
		if k != key && strings.EqualFold(k, key) {
			delete(c, k)
		}
	}
	c[key] = []string{value}
}

// Keys returns every header name present.
func (c natsHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// InjectTraceContext writes the trace context active in ctx into hdr. It
// is a no-op when ctx carries no valid span context, so a publish made
// outside any trace produces a message with no misleading half-filled
// trace headers.
func InjectTraceContext(ctx context.Context, hdr nats.Header) {
	wirePropagator.Inject(ctx, natsHeaderCarrier(hdr))
}

// ExtractTraceContext returns a copy of ctx carrying the remote trace
// context found in hdr, so a consumer can start a span that continues the
// publisher's trace rather than beginning an unrelated one. It returns ctx
// unchanged when hdr carries no trace context.
func ExtractTraceContext(ctx context.Context, hdr nats.Header) context.Context {
	if hdr == nil {
		return ctx
	}
	return wirePropagator.Extract(ctx, natsHeaderCarrier(hdr))
}
