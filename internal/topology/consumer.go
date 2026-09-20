package topology

import (
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// consumerAckWait is how long JetStream waits for an Ack before considering
// a delivery timed out and eligible for redelivery. It bounds a handler
// that hangs without ever Ack/Nak/Term-ing its message.
const consumerAckWait = 30 * time.Second

// SubscribeConsumerConfig returns the durable, at-least-once consumer shape
// every event.Bus.Subscribe call uses: a named durable consumer (so
// multiple controller replicas calling Subscribe on the same topic share
// one consumer group and JetStream guarantees exactly one of them gets each
// message, per PLAN.md Section 26.4's split-brain mitigation), explicit
// acknowledgement (so an unhandled panic or a crash before Ack leaves the
// message redeliverable rather than silently lost), and MaxDeliverDefault
// as the redelivery ceiling internal/event's Dead Letter Queue mechanism
// enforces.
//
// durable is expected to already be a legal NATS durable name; callers
// building one from a caller-supplied string should route it through
// DurableName first.
func SubscribeConsumerConfig(durable, filterSubject string) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Durable:       durable,
		FilterSubject: filterSubject,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       consumerAckWait,
		MaxDeliver:    MaxDeliverDefault,
	}
}

// DispatchConsumerConfig returns the shared durable consumer every Runner
// replica pulls dispatch jobs from. Every replica that calls
// jetstream.CreateOrUpdateConsumer with this exact config joins the same
// consumer group, which is what makes horizontally-scaled, stateless
// Runners (PLAN.md Section 16) safe: JetStream, not application code,
// guarantees a given dispatch is handed to exactly one of them.
//
// Phase 101a gave the dispatch subject a device token and this consumer a
// trailing wildcard over it. The exactly-once guarantee above is UNCHANGED
// by that: the filter still matches every dispatch exactly once, so this
// remains one consumer group over the whole fleet.
//
// # The rule a second consumer has to obey
//
// EXACTLY ONE CONSUMER MAY MATCH ANY GIVEN DISPATCH SUBJECT. Two consumers
// matching one dispatch means two Runners execute the same unit of work.
// The per-device execution lease would serialize them, but a lease is a
// mitigation for a race, not a substitute for a routing decision.
//
// That rule has a consequence worth stating before somebody discovers it.
// NATS has no negative filter, so a scoped consumer CANNOT be carved out
// of this one's ">" wildcard by excluding devices from it. Introducing one
// is therefore a change to the PUBLISHER: those devices' dispatches have
// to go to a different subject prefix, which this package would declare,
// rather than to a cleverer filter here.
func DispatchConsumerConfig() jetstream.ConsumerConfig {
	return SubscribeConsumerConfig(DispatchDurableName, DispatchSubjectAll())
}

// CheckConsumerConfig returns the durable consumer every check-capable
// Runner replica shares for check dispatches. It obeys the rule above by
// construction: its filter, CheckSubjectAll, and DispatchSubjectAll share
// no subject, so exactly one of the two consumers matches any dispatch.
// That disjointness is also the whole of check mode's safety across a
// rolling upgrade: a Runner that predates check mode only ever creates
// DispatchConsumerConfig, so a check waits for a Runner that knows what
// one is, instead of reaching one that would run it for real.
func CheckConsumerConfig() jetstream.ConsumerConfig {
	return SubscribeConsumerConfig(CheckDurableName, CheckSubjectAll())
}

// LogViewerConsumerConfig returns an ephemeral, non-acknowledging consumer
// scoped to one job's log subject. It is deliberately not a durable,
// shared-group consumer: PLAN.md Section 26.4 names live log viewing as the
// one deliberate exception to Consumer Groups, since every viewer must see
// every line, and a shared acking consumer behind several per-client SSE
// streams would make two simultaneous viewers of the same job mutually
// exclusive instead of both seeing the full stream.
func LogViewerConsumerConfig(jobID string) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		FilterSubject: LogSubject(jobID),
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckNonePolicy,
	}
}
