package event

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/retry"
	"github.com/nats-io/nats.go/jetstream"
)

// dlqBaseDelay and dlqMaxDelay bound the jittered exponential backoff
// applied between redelivery attempts, reusing pkg/retry.Backoff (the same
// formula runner.Agent.calculateBackoff already uses) rather than a second,
// independently tuned copy.
const (
	dlqBaseDelay = 250 * time.Millisecond
	dlqMaxDelay  = 30 * time.Second
)

// DeadLetterEnvelope is what a message becomes once it is republished to
// its dead letter subject: which message it was and why it never
// succeeded, never what it carried.
//
// It holds identifiers only, by allowlist (Phase 103a). It used to copy the
// whole message, and a dispatch carries its credential, its fields, its
// extra variables and its injected values, so every dead letter doubled
// the secrets the stream held, for the same retention. Naming what is kept
// rather than what is stripped means a secret-bearing field added to a
// payload later cannot reach a dead letter. Dead letters written before
// this change still hold payloads until the stream's retention removes
// them.
//
// A consumer learns the job and device a dead dispatch was for from
// IdempotencyKey, which for a dispatch is topology.DispatchIdentity, read
// back by topology.ParseDispatchIdentity beside it, and from
// OriginalSubject; never by splitting either by hand.
type DeadLetterEnvelope struct {
	// OriginalSubject is the subject the message was originally published
	// to, before redelivery exhaustion routed it here.
	OriginalSubject string `json:"original_subject"`
	// Reason is the last handler failure's error text (or a description of
	// a recovered panic), masked as every log line is before it leaves the
	// process, since an error can quote what a device said.
	Reason string `json:"reason"`
	// FailedAt is when the message was dead-lettered, in UTC.
	FailedAt time.Time `json:"failed_at"`
	// NumDelivered is how many delivery attempts the message had received
	// by the time it was dead-lettered.
	NumDelivered uint64 `json:"num_delivered"`

	// The Event envelope's identifiers, empty for a message that was not
	// one. Its Data never travels here.
	EventID        string `json:"event_id,omitempty"`
	EventType      string `json:"event_type,omitempty"`
	CorrelationID  string `json:"correlation_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	Actor          string `json:"actor,omitempty"`
}

// eventIdentifiers is the part of an Event a dead letter may keep. Decoding
// into it rather than into Event means the payload is never even held.
type eventIdentifiers struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	CorrelationID  string `json:"correlation_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Actor          string `json:"actor"`
}

// deadLetter is msg's dead letter: its identifiers, and reason masked.
func deadLetter(msg jetstream.Msg, numDelivered uint64, reason string) DeadLetterEnvelope {
	var ids eventIdentifiers
	// A message that is not an Event keeps no identifiers, which is the
	// whole of what can be said about it without copying it.
	_ = json.Unmarshal(msg.Data(), &ids)
	return DeadLetterEnvelope{
		OriginalSubject: msg.Subject(),
		Reason:          redact.Text(nil, reason),
		FailedAt:        time.Now().UTC(),
		NumDelivered:    numDelivered,
		EventID:         ids.ID,
		EventType:       ids.Type,
		CorrelationID:   ids.CorrelationID,
		IdempotencyKey:  ids.IdempotencyKey,
		Actor:           ids.Actor,
	}
}

// HandleDeliveryFailure is the single implementation of PLAN.md Section
// 26.3's Dead Letter Queue mechanism: "This lives in the bus adapter,
// never in a consumer." natsBus.Subscribe calls it internally for every
// handler failure. runner.Agent, which by deliberate design
// (PATTERNS.md's "Push vs Pull Execution Model" entry) pulls from its own
// raw jetstream.Consumer rather than going through Bus.Subscribe, calls
// this same exported function directly instead of hand-rolling a second
// DLQ mechanism, per PLAN.md Section 25's Build-Once rule. The mechanism
// still lives in this package either way; only its caller differs.
//
// Below maxDeliver redeliveries, the message is negatively acknowledged
// with a jittered backoff delay derived from its own delivery count (never
// a bare Nak, which JetStream redelivers instantly and ignores the
// consumer's own AckWait/backoff configuration for). At or above
// maxDeliver, the message's identifiers are marshaled into a
// DeadLetterEnvelope (never its payload), published
// to topology.DeadLetterSubject(msg.Subject()), and terminated so it is
// never redelivered again.
func HandleDeliveryFailure(ctx context.Context, js jetstream.JetStream, msg jetstream.Msg, maxDeliver int, reason string) error {
	meta, err := msg.Metadata()
	if err != nil {
		// Metadata is only unavailable for a non-JetStream message, which
		// cannot happen for anything Subscribe or Agent hands us; failing
		// safe with a bare Nak rather than panicking or dead-lettering
		// blind is the conservative choice.
		return msg.Nak()
	}

	// maxDeliver <= 0 is treated as already exhausted rather than
	// converted to uint64 directly: a negative int converted to uint64
	// wraps around to a huge positive number, which would make the
	// comparison below always true and retry forever instead of failing
	// safe. Callers only ever pass a small positive constant today
	// (topology.MaxDeliverDefault), so this branch is defensive, not
	// reachable in practice, but it is what makes the uint64 conversion
	// on the next line provably safe rather than merely unlikely to
	// matter.
	if maxDeliver > 0 && meta.NumDelivered < uint64(maxDeliver) {
		delay := retry.Backoff(dlqBaseDelay, dlqMaxDelay, int(meta.NumDelivered)) // #nosec G115 -- NumDelivered is a JetStream redelivery counter, bounded in practice by a consumer's own small MaxDeliver (topology.MaxDeliverDefault is 5); it cannot realistically approach int overflow range.
		return msg.NakWithDelay(delay)
	}

	data, err := json.Marshal(deadLetter(msg, meta.NumDelivered, reason))
	if err != nil {
		return fmt.Errorf("failed to marshal dead letter envelope for %s: %w", msg.Subject(), err)
	}

	dlqSubject := topology.DeadLetterSubject(msg.Subject())
	if _, err := js.Publish(ctx, dlqSubject, data); err != nil {
		return fmt.Errorf("failed to publish dead letter for %s to %s: %w", msg.Subject(), dlqSubject, err)
	}

	return msg.Term()
}
