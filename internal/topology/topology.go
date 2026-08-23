// Package topology is the single owner of every NATS JetStream subject,
// stream, consumer, key-value bucket, and retention/replica setting used
// by Pleiades.
//
// Before this package existed, three call sites each declared their own
// idea of what stream and subjects the mesh uses: event.NewNatsBus created
// a stream filtered to "pleiades.events.>", cmd/demo/main.go separately
// created a stream filtered to "jobs.logs.>", and api.Dispatcher published
// to the bare literal "runbooks.dispatch", which neither stream's subject
// filter covered, so that publish silently failed in production. Section 25
// of PLAN.md ("Shared Primitives") names "Messaging topology owner" as a
// Build-Once contract due by Phase 2 for exactly this reason: independent
// stream declarations drift, and retention/replica fixes must otherwise be
// applied in N places. Every subject, stream, consumer and bucket shape
// in the mesh is declared here and nowhere else.
//
// That last sentence used to be aspirational rather than true, and it is
// worth saying which way. The "Pleiades_Locks" KV bucket, which carries
// both the leader-election leases and the per-device execution leases,
// had its shape declared inline in internal/lock and provisioned from
// two composition roots, so this doc comment and internal/archtest's own
// repetition of it were both false. LockBucketConfig (dedup.go) is that
// shape now, and internal/archtest's
// TestOnlyTopologyDeclaresJetStreamShapes is what keeps this paragraph
// honest: it fails the build if any package outside this one writes a
// jetstream.StreamConfig, ConsumerConfig or KeyValueConfig literal.
package topology

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

const (
	// StreamName is the one JetStream stream every Pleiades subject lives
	// on. There is deliberately only one: splitting dispatch, logs, and
	// lifecycle events across several streams (as cmd/demo's now-removed
	// "JOBS" stream once did) is exactly the drift this package exists to
	// prevent.
	StreamName = "PLEIADES"

	// StreamSubjectRoot is the wildcard subject filter the stream is
	// configured with. Every subject this package builds falls under it.
	StreamSubjectRoot = "pleiades.>"

	eventSubjectPrefix  = "pleiades.events."
	dispatchSubject     = "pleiades.jobs.dispatch"
	logSubjectPrefix    = "pleiades.jobs.logs."
	resultSubjectPrefix = "pleiades.jobs.results."
	dlqSubjectPrefix    = "pleiades.dlq."
	// jobRequestedSubject is the one subject a Job launch (a later stage
	// in this session, replacing internal/api/dispatcher.go's synchronous
	// handler) publishes to, and internal/dispatch.Worker.HandleJobRequested
	// subscribes to, to hand off a persisted Job's durable fan-out. See
	// JobRequestedSubject.
	jobRequestedSubject = "pleiades.jobs.requested"

	// DispatchDurableName is the durable consumer name every Runner
	// replica shares when pulling dispatch jobs, so JetStream's own
	// "Consumer Group" semantics (PLAN.md Section 26.4) guarantee exactly
	// one Runner processes a given dispatch, no matter how many replicas
	// are running.
	DispatchDurableName = "runner-agent"

	// MaxDeliverDefault is the default redelivery ceiling before a message
	// is dead-lettered. Shared by consumer configuration and the DLQ
	// exhaustion check in internal/event so the two can never drift apart
	// (a consumer configured with one ceiling and a DLQ check enforcing
	// another would let messages either dead-letter too early or retry
	// forever).
	MaxDeliverDefault = 5

	// maxDurablePrefixLen bounds the human-readable portion of a generated
	// durable name; see DurableName.
	maxDurablePrefixLen = 48
)

// illegalDurableChars matches every byte NATS forbids in a durable
// consumer name: whitespace, ".", "*", ">", path separators, and (by virtue
// of this pattern being an allow-list of what's kept) any non-printable
// character.
var illegalDurableChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// EventSubject returns the subject a lifecycle event of the given type
// publishes to, e.g. EventSubject("device.created") is
// "pleiades.events.device.created". This is the subject
// internal/engine/executor.go's own workflow-status events already publish
// under; topology only formalizes it as the single place that spelling is
// decided.
func EventSubject(eventType string) string {
	return eventSubjectPrefix + eventType
}

// DispatchSubject returns the one subject a runbook dispatch is published
// to. It replaces the previous bare literal "runbooks.dispatch", which the
// stream this package now owns would not have matched.
func DispatchSubject() string {
	return dispatchSubject
}

// LogSubject returns the subject a given job's execution log lines publish
// to and stream from. It replaces the previous bare literal
// "jobs.logs.<id>".
func LogSubject(jobID string) string {
	return logSubjectPrefix + jobID
}

// ResultSubject returns the subject a given job's Runner-buffered
// execution result publishes to once internal/runner's Write-Ahead-Log
// flush succeeds (PLAN.md Section 16's State Desync Mitigation). It falls
// under StreamSubjectRoot exactly like every other subject this package
// declares, so no separate stream or EnsureStream change is needed for
// it.
func ResultSubject(jobID string) string {
	return resultSubjectPrefix + jobID
}

// JobRequestedSubject returns the one subject a persisted Job's launch
// publishes to in order to hand its durable fan-out off to a Worker (Phase
// 14, The Dispatcher; internal/dispatch). It replaces the old synchronous
// in-HTTP-handler fan-out internal/api/dispatcher.go's DispatchRunbook
// used to perform inline.
func JobRequestedSubject() string {
	return jobRequestedSubject
}

// DeadLetterSubject returns the subject a message is republished to once it
// has exhausted its redelivery attempts on originalSubject. Every dead
// letter for every original subject shares the "pleiades.dlq." prefix, so
// one operator-facing consumer can watch all of them at once with a single
// trailing-wildcard subscription.
func DeadLetterSubject(originalSubject string) string {
	return dlqSubjectPrefix + originalSubject
}

// DurableName maps an arbitrary logical subscriber name (typically the
// topic a caller passed to Bus.Subscribe) to a legal, stable NATS durable
// consumer name. It is deterministic: the same logical name always maps to
// the same durable name, which is what lets a durable consumer actually
// resume across restarts and lets multiple replicas share one consumer
// group.
//
// The input is not merely sanitized in place, because sanitization alone
// is lossy: two different logical names that differ only in characters
// illegal-in-NATS (e.g. "a.b" and "a/b") would otherwise collide on the
// same durable name and silently share a consumer group. Appending a short
// hash of the original, unsanitized input keeps the output legal while
// keeping distinct inputs distinct. This is a Schema/Injection Hardening
// concern, not just a naming convenience: a caller-controlled topic string
// (ultimately reachable from a workflow ID, per FAILURE_PATTERNS.md #18's
// precedent for the same class of bug) must never be able to construct a
// consumer name that collides with, or otherwise misroutes onto, another
// subscriber's consumer.
func DurableName(logical string) string {
	sanitized := illegalDurableChars.ReplaceAllString(logical, "_")
	sanitized = strings.Trim(sanitized, "_")
	if sanitized == "" {
		sanitized = "consumer"
	}
	if len(sanitized) > maxDurablePrefixLen {
		sanitized = sanitized[:maxDurablePrefixLen]
	}

	sum := sha256.Sum256([]byte(logical))
	return fmt.Sprintf("%s-%s", sanitized, hex.EncodeToString(sum[:])[:8])
}
