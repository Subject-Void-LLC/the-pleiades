package topology

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// streamMaxAge is a generous, single-node-dev-cluster-appropriate default
// retention window. No production deployment topology exists yet (Phase 2's
// own checklist scope is the bus mechanism and its composition roots, not
// clustering or capacity planning), so this is deliberately conservative
// rather than tuned; a later phase that needs a different value changes it
// here, once, rather than at each of the independent declarations this
// package replaces.
const streamMaxAge = 7 * 24 * time.Hour

// streamDuplicateWindow is how long JetStream remembers a message's
// Nats-Msg-Id (set via jetstream.WithMsgID on Publish) to reject a
// duplicate publish of the same idempotency key. Set explicitly rather
// than left at zero (which defers to the server's own default, currently 2
// minutes) so this codebase's own producer-side dedup guarantee does not
// silently change if the server's default ever does.
const streamDuplicateWindow = 2 * time.Minute

// StreamConfig returns the one JetStream stream configuration every
// Pleiades subject is published on. There is exactly one stream: see
// StreamName's doc comment for why splitting subjects across several
// streams (as this package's predecessor declarations did) is the drift
// this package exists to prevent.
func StreamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:       StreamName,
		Subjects:   []string{StreamSubjectRoot},
		Retention:  jetstream.LimitsPolicy,
		Storage:    jetstream.FileStorage,
		MaxAge:     streamMaxAge,
		Replicas:   1,
		Duplicates: streamDuplicateWindow,
	}
}

// StreamDrift names one declared field whose live value on the server
// differs from what this binary believes it should be.
type StreamDrift struct {
	// Field is the jetstream.StreamConfig field name, for example
	// "MaxAge".
	Field string
	// Live is the value the server currently holds, formatted.
	Live string
	// Declared is the value StreamConfig returns, formatted.
	Declared string
}

// String renders a drift as "MaxAge: live 24h0m0s, declared 168h0m0s".
func (d StreamDrift) String() string {
	return fmt.Sprintf("%s: live %s, declared %s", d.Field, d.Live, d.Declared)
}

// StreamConfigDrift reports every field this project DECLARES whose live
// value differs from the declared one.
//
// It compares only the seven fields StreamConfig sets, deliberately.
// jetstream.StreamConfig carries far more than that and the server fills
// in the remainder with its own defaults, so a whole-struct comparison
// would report drift against a perfectly healthy cluster that this code
// itself had just provisioned.
//
// Subjects compares as a set rather than a slice: the server is free to
// return them in its own order, and an ordering difference is not a
// configuration difference.
func StreamConfigDrift(live jetstream.StreamConfig) []StreamDrift {
	declared := StreamConfig()
	var drift []StreamDrift

	if live.Name != declared.Name {
		drift = append(drift, StreamDrift{"Name", live.Name, declared.Name})
	}
	if !sameSubjects(live.Subjects, declared.Subjects) {
		drift = append(drift, StreamDrift{"Subjects",
			strings.Join(live.Subjects, ","), strings.Join(declared.Subjects, ",")})
	}
	if live.Retention != declared.Retention {
		drift = append(drift, StreamDrift{"Retention",
			live.Retention.String(), declared.Retention.String()})
	}
	if live.Storage != declared.Storage {
		drift = append(drift, StreamDrift{"Storage",
			live.Storage.String(), declared.Storage.String()})
	}
	if live.MaxAge != declared.MaxAge {
		drift = append(drift, StreamDrift{"MaxAge",
			live.MaxAge.String(), declared.MaxAge.String()})
	}
	if live.Replicas != declared.Replicas {
		drift = append(drift, StreamDrift{"Replicas",
			strconv.Itoa(live.Replicas), strconv.Itoa(declared.Replicas)})
	}
	if live.Duplicates != declared.Duplicates {
		drift = append(drift, StreamDrift{"Duplicates",
			live.Duplicates.String(), declared.Duplicates.String()})
	}
	return drift
}

// sameSubjects reports whether two subject lists hold the same entries,
// ignoring order and duplicates.
func sameSubjects(live, declared []string) bool {
	if len(live) != len(declared) {
		return false
	}
	seen := make(map[string]bool, len(live))
	for _, s := range live {
		seen[s] = true
	}
	for _, s := range declared {
		if !seen[s] {
			return false
		}
	}
	return true
}

// ProvisionStream reconciles the Pleiades stream to the shape
// StreamConfig declares, creating it if it is absent and updating it in
// place if its shape has moved.
//
// This is the ONLY function in the module that can change the shape of an
// existing stream, and it belongs to cmd/controller alone.
// internal/archtest's TestOnlyTheControllerProvisionsTheStream is what
// keeps that true rather than merely written down.
//
// The asymmetry with AttachStream is the whole of Phase 96b. Before it,
// all three composition roots called CreateOrUpdateStream on every
// process start from compile-time constants, so an operator's retention
// choice was silently reverted by whichever binary restarted last, and
// the Runner is the binary most likely to be an older build deployed at
// the edge and upgraded last (FAILURE_PATTERNS.md #178).
//
// It deliberately does NOT narrow Subjects. MaxAge and Duplicates change
// online and harmlessly; removing a subject from a live stream orphans
// whatever was published under it, with no error. There is one wildcard
// subject today so this cannot currently trigger, which is exactly when
// the guard is cheap. Any drift found after reconciling is returned for
// the caller to log.
func ProvisionStream(ctx context.Context, js jetstream.JetStream) (jetstream.Stream, []StreamDrift, error) {
	desired := StreamConfig()

	if live, err := js.Stream(ctx, StreamName); err == nil {
		if orphaned := subjectsThatWouldBeOrphaned(live.CachedInfo().Config.Subjects, desired.Subjects); len(orphaned) > 0 {
			return nil, nil, fmt.Errorf(
				"refusing to reshape %s stream: it would stop capturing %s, orphaning anything already published there; widen StreamConfig's Subjects or migrate deliberately",
				StreamName, strings.Join(orphaned, ", "))
		}
	} else if !errors.Is(err, jetstream.ErrStreamNotFound) {
		return nil, nil, fmt.Errorf("failed to read %s stream before provisioning: %w", StreamName, err)
	}

	stream, err := js.CreateOrUpdateStream(ctx, desired)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to provision %s stream: %w", StreamName, err)
	}
	return stream, StreamConfigDrift(stream.CachedInfo().Config), nil
}

// AttachStream binds to the Pleiades stream, creating it only when it does
// not exist yet, and never reshaping one that does.
//
// Every process except cmd/controller uses this. The create-if-absent half
// is not a compromise: it is what preserves two properties the alternative
// ("attach, and refuse to start if it is missing") would have destroyed.
// A stream lost to a NATS volume failure or an operator's `nats stream rm`
// is still recreated by whichever process arrives first, with no human,
// because a running Controller provisions once at startup and never
// re-asserts. And no start ordering is introduced between the Controller
// and the Runner, which neither docker-compose.yml nor the Helm chart
// expresses and which docs/10-running-in-production.md explicitly
// promises is unnecessary.
//
// What it removes is the ability to CHANGE a shape that already exists,
// which is the actual defect: an older Runner can no longer revert an
// operator's retention budget, because it no longer has a code path that
// rewrites a live stream.
//
// Any drift between the live shape and this binary's declared one is
// returned rather than corrected. During a mixed rollout that is the only
// signal there is: the old binary is no longer reverting anything, but it
// is also not applying the new shape, and nothing else in the system would
// say so.
func AttachStream(ctx context.Context, js jetstream.JetStream) (jetstream.Stream, []StreamDrift, error) {
	stream, err := js.Stream(ctx, StreamName)
	if err == nil {
		return stream, StreamConfigDrift(stream.CachedInfo().Config), nil
	}
	if !errors.Is(err, jetstream.ErrStreamNotFound) {
		return nil, nil, fmt.Errorf("failed to read %s stream: %w", StreamName, err)
	}

	// Absent, so bootstrap it. A concurrent creator racing here is not an
	// error: CreateOrUpdateStream converges on the same declared shape,
	// and every binary that can reach this line declares the same one.
	created, err := js.CreateOrUpdateStream(ctx, StreamConfig())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create the missing %s stream: %w", StreamName, err)
	}
	return created, nil, nil
}

// subjectsThatWouldBeOrphaned returns the live subjects that desired no
// longer covers.
//
// The comparison is exact rather than wildcard-aware. A live "pleiades.>"
// against a desired "pleiades.jobs.>" is genuinely a narrowing and should
// be refused; treating the wildcard as covering would silently permit the
// destructive case this exists to prevent.
func subjectsThatWouldBeOrphaned(live, desired []string) []string {
	keep := make(map[string]bool, len(desired))
	for _, s := range desired {
		keep[s] = true
	}
	var orphaned []string
	for _, s := range live {
		if !keep[s] {
			orphaned = append(orphaned, s)
		}
	}
	return orphaned
}

// StreamRole is what a process is permitted to do to the shared stream.
//
// It is a required constructor argument rather than a functional option,
// following LESSONS_LEARNED.md #154: a decision a component cannot make
// for itself belongs in the signature, because an option is what every
// caller except its author forgets, and its absence is silent. Getting
// this wrong is exactly the defect Phase 96b exists to close, so the
// compiler asks every call site rather than defaulting one in.
//
// The zero value is deliberately not a role. Counting from iota+1 means a
// caller who writes StreamRole(0), or forgets a struct field, gets a
// rejection instead of silently receiving whichever behaviour happened to
// be first. internal/schedule/rrule.Frequency uses the same construction
// for the same reason.
type StreamRole int

const (
	// StreamProvisioner may reconcile the stream to the declared shape,
	// creating or reshaping it. It belongs to cmd/controller alone, which
	// internal/archtest enforces.
	StreamProvisioner StreamRole = iota + 1

	// StreamReader may bind to the stream, and may create it when it is
	// absent, but can never change the shape of one that already exists.
	// Every other process uses this.
	StreamReader
)

// String renders a role for logs and errors.
func (r StreamRole) String() string {
	switch r {
	case StreamProvisioner:
		return "provisioner"
	case StreamReader:
		return "reader"
	default:
		return "invalid"
	}
}

// BindStream applies role to the shared stream and returns any drift
// between the live shape and this binary's declared one.
//
// It is the one place the two entry points are selected between, so a
// caller states its authority once and cannot accidentally reach the
// wrong half.
func BindStream(ctx context.Context, js jetstream.JetStream, role StreamRole) (jetstream.Stream, []StreamDrift, error) {
	switch role {
	case StreamProvisioner:
		return ProvisionStream(ctx, js)
	case StreamReader:
		return AttachStream(ctx, js)
	default:
		return nil, nil, fmt.Errorf("invalid stream role %d: use topology.StreamProvisioner or topology.StreamReader", int(role))
	}
}
