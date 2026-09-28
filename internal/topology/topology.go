// Package topology is the single owner of every NATS JetStream subject,
// stream, consumer, key-value bucket, and retention/replica setting used
// by The Pleiades.
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
	// checkSubjectPrefix carries dispatches that must only ever be
	// checked (Phase 46's Walk-tier check mode). It is a prefix of its own,
	// and not a field on a dispatch, for one reason: a Runner built before
	// check mode existed ignores a field it does not know, and would run a
	// check for real. Such a Runner's consumer filters dispatchSubjectPrefix
	// only, so it never receives anything published here. See
	// CheckConsumerConfig.
	checkSubjectPrefix  = "pleiades.jobs.check."
	logSubjectPrefix    = "pleiades.jobs.logs."
	resultSubjectPrefix = "pleiades.jobs.results."
	// journalSubjectPrefix carries the run journal a Runner produces
	// while executing a dispatch (Phase 40). It sits beside the log and
	// result prefixes rather than under either, because it is neither:
	// a log line is operator-facing text, a result is one dispatch's
	// outcome, and a journal entry is a per-task audit record with its
	// own durable consumer and its own table.
	journalSubjectPrefix = "pleiades.jobs.journal."
	// controlSubjectPrefix carries per-job control signals, of which there
	// is one today: stop. It sits beside the log, result and journal
	// prefixes rather than under any of them because it is the only
	// subject in this package that travels the other way, from the
	// Controller to a Runner already working, and the only one that is
	// not JetStream. See ControlSubject.
	controlSubjectPrefix = "pleiades.jobs.control."
	dlqSubjectPrefix     = "pleiades.dlq."
	// jobRequestedSubject is the one subject a Job launch (a later stage
	// in this session, replacing internal/api/dispatcher.go's synchronous
	// handler) publishes to, and internal/dispatch.Worker.HandleJobRequested
	// subscribes to, to hand off a persisted Job's durable fan-out. See
	// JobRequestedSubject.
	jobRequestedSubject = "pleiades.jobs.requested"

	// meshRenewSubject is where a process asks the control plane for a
	// fresh mesh credential.
	//
	// THE HYPHEN IS LOAD BEARING and is the whole reason this constant
	// does not read "pleiades.mesh.renew". The stream is configured with
	// StreamSubjectRoot, "pleiades.>", which matches any subject whose
	// FIRST TOKEN is exactly "pleiades", so every subject under that root
	// is durably stored whether or not anyone wanted it to be.
	// "pleiades-mesh" is a different first token, so this one is not.
	// See MeshRenewSubject for why that matters here specifically.
	meshRenewSubject = "pleiades-mesh.renew"

	// DispatchDurableName is the durable consumer name every Runner
	// replica shares when pulling dispatch jobs, so JetStream's own
	// "Consumer Group" semantics (PLAN.md Section 26.4) guarantee exactly
	// one Runner processes a given dispatch, no matter how many replicas
	// are running.
	DispatchDurableName = "runner-agent"

	// CheckDurableName is the durable consumer every Runner replica that
	// can run checks shares for check dispatches (checkSubjectPrefix), the
	// same consumer-group semantics as DispatchDurableName over a disjoint
	// subject prefix.
	CheckDurableName = "runner-check"

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
// Nothing in this module calls this function today. It has no publisher
// and no subscriber, and internal/meshid deliberately grants no rights over
// the space it names. That is worth knowing before building on it: the
// subject is a declared convention rather than a live event bus, so the
// first caller also has to add the grant.
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

// CheckSubject returns the subject a check of deviceID is published to:
// the same device token as DispatchSubject, under checkSubjectPrefix. A
// Runner takes anything arriving here as a check whatever the payload
// says (routing.CheckOnly), and a Runner that predates check mode never
// receives it, so a check can never be run for real by either.
func CheckSubject(deviceID string) string {
	return checkSubjectPrefix + SubjectToken(deviceID)
}

// CheckSubjectAll returns the wildcard matching every device's check
// subject, which is what the shared check consumer filters on.
func CheckSubjectAll() string {
	return checkSubjectPrefix + ">"
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

// JournalSubject returns the subject a given job's run journal entries
// publish to (Phase 40). It falls under StreamSubjectRoot exactly like
// every other subject this package declares, so no stream change is
// needed for it.
//
// The job id goes through SubjectToken for the reason LogSubject's own
// comment gives.
func JournalSubject(jobID string) string {
	return journalSubjectPrefix + SubjectToken(jobID)
}

// JournalSubjectAll is LogSubjectAll's sibling for the journal subject;
// see its doc comment for why these are declared rather than derived.
func JournalSubjectAll() string {
	return journalSubjectPrefix + ">"
}

// ControlSubject returns the subject a running job's control signals are
// published on and subscribed to. There is one signal today: a cancel.
//
// This subject is deliberately CORE NATS rather than JetStream, and it is
// the only one in this package that is. Two reasons, and the first is
// decisive. event.Bus.Subscribe creates a DURABLE consumer named after the
// topic, so every subscriber to a subject joins one consumer group and
// exactly one of them receives each message; a cancel delivered to one
// arbitrary Runner instead of the one holding the job is worse than no
// cancel at all. Second, at-least-once redelivery is the wrong promise
// here: a control signal means something only to whoever is listening at
// the moment it is sent, and a cancel redelivered later would be a
// cancellation arriving for a job that has long since ended.
//
// It still falls under StreamSubjectRoot, so the single stream captures a
// copy and holds it for MaxAge. That is deliberate and harmless rather than
// an oversight: the body carries a job id and nothing else, and core
// subscribers never replay what the stream retained. What it buys is that
// this subject needs no exception anywhere else, in a package whose whole
// premise is that every subject lives under one root.
//
// The job id goes through SubjectToken for the reason LogSubject's own
// comment gives.
func ControlSubject(jobID string) string {
	return controlSubjectPrefix + SubjectToken(jobID)
}

// ControlSubjectAll is LogSubjectAll's sibling for the control subject;
// see its doc comment for why these are declared rather than derived.
//
// A Runner subscribes to ONE job's control subject at a time, never this
// wildcard: it knows which job it is executing. This exists because a
// permission grant cannot know that in advance, so the Runner's subscribe
// grant has to name the whole space even though the subscription never
// does.
func ControlSubjectAll() string {
	return controlSubjectPrefix + ">"
}

// JobRequestedSubject returns the one subject a persisted Job's launch
// publishes to in order to hand its durable fan-out off to a Worker (Phase
// 14, The Dispatcher; internal/dispatch). It replaces the old synchronous
// in-HTTP-handler fan-out internal/api/dispatcher.go's DispatchRunbook
// used to perform inline.
func JobRequestedSubject() string {
	return jobRequestedSubject
}

// MeshRenewSubject returns the subject a process asks for a fresh mesh
// credential on.
//
// # Why this is request/reply and not an HTTP endpoint
//
// Because there is no other option, and that is a fact about this
// codebase rather than a preference. internal/runner has no HTTP client,
// no controller URL and no database; every byte a Runner moves goes over
// this bus. So the only way a Runner can ask the control plane for
// anything is to publish and wait for a reply.
//
// That was also the reason this could not be built before mesh identity.
// A renewal endpoint on an unauthenticated bus hands a working credential
// to anything that can reach the broker, which is strictly worse than
// having no endpoint at all. It becomes safe exactly when publishing to
// this subject requires a credential, which is what makes it this phase's
// to build and nobody else's.
//
// # What it does not put on a stream, and why the name looks odd
//
// Neither half of this exchange is durably stored, and getting there
// took a measurement rather than an assumption.
//
// The reply was never a problem: it travels to the requester's own
// _INBOX, which is outside the stream's subject space and which only that
// client subscribes to, so the credential itself exists on the wire and
// nowhere else. That was measured: an _INBOX publish leaves the stream's
// message count unchanged.
//
// The REQUEST was a problem, and the first version of this subject had
// it. Written as "pleiades.mesh.renew" it fell under StreamSubjectRoot,
// "pleiades.>", and a plain core publish to it took the stream from zero
// messages to one. Every renewal in the fleet would have been retained
// for the outage budget's window, which is seven days by default and 168
// days at the maximum, on the same stream this phase exists to stop
// treating as a safe place for identity material. Nothing secret is in a
// request today, and that is exactly the kind of thing that changes
// quietly later.
//
// So the first token is "pleiades-mesh" rather than "pleiades", which is
// a different token and therefore outside the root. It reads like a typo
// and it is not; it is the only part of the name doing any work.
//
// # Scope
//
// One subject, not one per device. A Runner's credential is fleet
// scoped, so renewing it is not a per-device question, and a renewal
// grants nothing the caller did not already hold: only a process that
// can already publish here has an identity to renew.
func MeshRenewSubject() string {
	return meshRenewSubject
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
