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

	eventSubjectPrefix = "pleiades.events."
	// dispatchSubjectPrefix ends in the separator because a dispatch
	// subject carries a device token after it. Until Phase 101a this was
	// the whole subject, with no trailing dot and nothing after it; see
	// DispatchSubject for what the token buys and what it does not.
	dispatchSubjectPrefix = "pleiades.jobs.dispatch."
	logSubjectPrefix      = "pleiades.jobs.logs."
	resultSubjectPrefix   = "pleiades.jobs.results."
	dlqSubjectPrefix      = "pleiades.dlq."
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

	// maxSubjectTokenPrefixLen bounds the human-readable portion of a
	// generated subject token; see SubjectToken.
	//
	// It is 48 for a stated reason rather than by copying the line above:
	// a device id is very often a 36-character UUID, and a bound that cut
	// one in half would make a subject unreadable at exactly the moment
	// somebody is reading it to work out which device a dispatch was for.
	// The two bounds are separate constants so either can move without
	// dragging the other with it.
	maxSubjectTokenPrefixLen = 48
)

// illegalIdentifierChars matches every byte this package refuses inside a
// generated NATS identifier, whether that identifier is a durable consumer
// name or a single subject token. Being an allow-list of what is KEPT, it
// covers whitespace, ".", "*", ">", path separators and every
// non-printable character in one pattern.
//
// It is deliberately NARROWER than either NATS rule strictly requires. A
// subject token may legally carry "/" and several other characters this
// drops. Permitting them would buy nothing, and it would cost the property
// that every identifier this package emits survives a shell, a `nats sub`
// argument and a log line without quoting.
var illegalIdentifierChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// legalIdentifier is the one sanitize-and-hash implementation in this
// package. DurableName and SubjectToken are both this function, differing
// only in their length bound and in what an empty input falls back to.
//
// The input is not merely sanitized in place, because sanitization alone
// is lossy: two different inputs differing only in characters this drops
// (e.g. "a.b" and "a/b") would otherwise collapse onto one output and
// silently share a consumer group, or a dispatch subject. Appending a
// short hash of the ORIGINAL, unsanitized input keeps the output legal
// while keeping distinct inputs distinct.
//
// This is a Schema/Injection Hardening concern, not a naming convenience.
// A caller-supplied string (a device id an operator wrote in a YAML
// inventory, a topic ultimately reachable from a workflow ID per
// FAILURE_PATTERNS.md #18) must never be able to construct an identifier
// that collides with, or misroutes onto, another one.
//
// Slicing sanitized by bytes is safe rather than lucky: everything the
// allow-list keeps is single-byte ASCII, so no multibyte rune can be cut
// in half here.
func legalIdentifier(input, fallback string, maxPrefixLen int) string {
	sanitized := illegalIdentifierChars.ReplaceAllString(input, "_")
	sanitized = strings.Trim(sanitized, "_")
	if sanitized == "" {
		sanitized = fallback
	}
	if len(sanitized) > maxPrefixLen {
		sanitized = sanitized[:maxPrefixLen]
	}

	sum := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%s-%s", sanitized, hex.EncodeToString(sum[:])[:8])
}

// SubjectToken maps an arbitrary caller-supplied string onto one legal
// NATS subject token: a value with no ".", no "*" and no ">" in it, so it
// occupies exactly one position in a subject and cannot smuggle extra
// tokens or a wildcard into the middle of one.
//
// The hazard this closes is specific and was live rather than theoretical.
// A device id is operator-supplied and opaque (pkg/inventory.DeviceID), so
// a perfectly ordinary one like "router1.example.com" would otherwise
// expand a three-token subject into a six-token one, which no filter
// subject in this package would match and no permission would describe.
//
// It is deterministic, so the publisher and the consumer of a subject
// derive the identical token without coordinating, which matters because
// they run in different processes: the Runner publishes a job's log
// subject and the Controller subscribes to it.
func SubjectToken(s string) string {
	return legalIdentifier(s, "unnamed", maxSubjectTokenPrefixLen)
}

// EventSubject returns the subject a lifecycle event of the given type
// publishes to, e.g. EventSubject("device.created") is
// "pleiades.events.device.created". This is the subject
// internal/engine/executor.go's own workflow-status events already publish
// under; topology only formalizes it as the single place that spelling is
// decided.
func EventSubject(eventType string) string {
	return eventSubjectPrefix + eventType
}

// DispatchSubject returns the subject a runbook dispatch for ONE device is
// published to.
//
// The device token is what makes the dispatch path scopeable at all.
// Before Phase 101a this function took no parameters and returned the flat
// literal "pleiades.jobs.dispatch", so every dispatch for every device
// shared one subject and nothing (no filter, no permission, no consumer)
// could tell two of them apart. Its siblings LogSubject and ResultSubject
// were already parameterized; the dispatch path specifically was not.
//
// Read what the token does and does not buy carefully, because the next
// stage of this work depends on the distinction:
//
//   - It scopes what a Runner may PUBLISH, so a Runner holding a
//     credential for one device cannot forge a dispatch for another.
//   - It makes a filtered consumer expressible, because FilterSubject is
//     the only mechanism that actually scopes delivery.
//
// It does NOT on its own restrict what a Runner RECEIVES. A pull consumer
// fetches through $JS.API.CONSUMER.MSG.NEXT.<stream>.<consumer> and its
// messages arrive on a reply inbox, so a subject permission never sees
// them and cannot gate them. What gates delivery is which consumer a
// Runner may bind. See DispatchConsumerConfig for the rule that follows.
func DispatchSubject(deviceID string) string {
	return dispatchSubjectPrefix + SubjectToken(deviceID)
}

// DispatchSubjectAll returns the wildcard matching every device's dispatch
// subject, which is what the shared fleet consumer filters on.
//
// It is declared here rather than spelled inline at the one consumer that
// needs it, for the same reason every other subject shape in this package
// is: a wildcard written at a call site is a subject declaration that has
// escaped the package whose whole job is owning them.
func DispatchSubjectAll() string {
	return dispatchSubjectPrefix + ">"
}

// LogSubject returns the subject a given job's execution log lines publish
// to and stream from. It replaces the previous bare literal
// "jobs.logs.<id>".
//
// The job id goes through SubjectToken rather than being concatenated
// straight in. Job ids are uuid.New().String() today, so nothing illegal
// reaches here in practice, which is exactly why the sanitizing belongs
// INSIDE this function rather than at its callers: the hazard is latent,
// and a latent hazard is the kind a future caller reintroduces without
// noticing. Doing it here also means the Runner that publishes a job's log
// line and the Controller that subscribes to it derive the same subject
// with no agreement between them beyond calling this function.
func LogSubject(jobID string) string {
	return logSubjectPrefix + SubjectToken(jobID)
}

// ResultSubject returns the subject a given job's Runner-buffered
// execution result publishes to once internal/runner's Write-Ahead-Log
// flush succeeds (PLAN.md Section 16's State Desync Mitigation). It falls
// under StreamSubjectRoot exactly like every other subject this package
// declares, so no separate stream or EnsureStream change is needed for
// it.
//
// The job id goes through SubjectToken for the reason LogSubject's own
// comment gives.
func ResultSubject(jobID string) string {
	return resultSubjectPrefix + SubjectToken(jobID)
}

// LogSubjectAll and ResultSubjectAll return the wildcards matching every
// job's log and result subjects.
//
// They exist because of a consequence of sanitizing INSIDE the builders
// that is easy to walk into and hard to see afterwards: LogSubject(">")
// does not return a wildcard, it returns "pleiades.jobs.logs.unnamed-62b67e1f",
// because ">" is not a legal token and SubjectToken duly encodes it. A
// caller wanting the whole space (a permission grant, an operator
// subscription, a filtered consumer over all jobs) therefore cannot build
// it by passing a wildcard through the per-job builder and must be given
// it, the same way DispatchSubjectAll is given rather than derived.
//
// The alternative, letting the builders pass a wildcard through
// unsanitized, was rejected: it would mean the one function that decides
// whether a string is a token or a pattern has to guess from the string,
// and a device id of ">" would silently become a subscription to
// everything.
func LogSubjectAll() string {
	return logSubjectPrefix + ">"
}

// ResultSubjectAll is LogSubjectAll's sibling for the result subject; see
// its doc comment for why these are declared rather than derived.
func ResultSubjectAll() string {
	return resultSubjectPrefix + ">"
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
	return legalIdentifier(logical, "consumer", maxDurablePrefixLen)
}
