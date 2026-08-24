package lock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/retry"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// minPositiveTTL is the smallest positive ttl natsLockManager accepts. Real
// per-key JetStream KV TTL requires whole-second granularity server-side: a
// sub-second positive ttl is rejected with a real API error
// (`err_code=10165 invalid per-message TTL`), confirmed empirically against
// a real nats-server while designing this phase (LESSONS_LEARNED.md).
// Checking this here turns that into a clear domain error instead of a
// harder-to-diagnose raw API error surfacing from deep inside a publish
// call. This is a real, deliberate asymmetry with inProcessManager (which
// has no such external granularity constraint and only rejects a negative
// ttl, via the shared validateTTL): natsLockManager is strictly the more
// restrictive of the two adapters on this one axis, stated explicitly
// rather than silently, the same way this package already states its other
// adapter-specific limits (see lockValue's own doc comment on shared-mode
// TTL tracking).
const minPositiveTTL = time.Second

type natsLockManager struct {
	nc *nats.Conn
	js jetstream.JetStream
	kv jetstream.KeyValue
}

// NewNatsLockManager connects to the NATS JetStream KeyValue store.
//
// This requires nats-server 2.11 or newer: real per-key TTL (which is what
// lets Acquire genuinely honor its ttl argument, and what lets an abandoned
// lock be reclaimed without any client comparing wall-clock timestamps
// across machines) depends on jetstream.KeyValueConfig.LimitMarkerTTL,
// which an older server rejects at bucket-creation time below.
//
// The dial goes through topology.Connect for the same reason every other
// dial in this module does, and this package specifically is not optional
// there. cmd/runner opens three independent NATS connections: the event
// bus, a raw one for the dispatch consumer, and this one. Giving the
// first two an unbounded reconnect budget while leaving this one on the
// nats.go default would be worse than changing none of them, because the
// lease KeepAlive this connection carries is what internal/runner reads
// as "the link to the Controller is gone". A lease connection that died
// permanently at 2m3s while the dispatch connection recovered would abort
// in-flight work on a link that had already come back.
//
// logger may be nil, in which case slog.Default() is used.
func NewNatsLockManager(ctx context.Context, url string, logger *slog.Logger) (Manager, error) {
	nc, err := topology.Connect(ctx, url, logger, "lock-manager")
	if err != nil {
		return nil, fmt.Errorf("failed to connect to nats: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to init jetstream: %w", err)
	}

	// Create or update the distributed lock bucket. The shape comes from
	// internal/topology, which owns every JetStream object's declared
	// configuration, rather than from a literal here: cmd/controller and
	// cmd/runner both run this constructor against one NATS, and a shape
	// written down in one place cannot disagree with itself across a
	// rolling upgrade the way two copies could.
	kv, err := js.CreateOrUpdateKeyValue(ctx, topology.LockBucketConfig())
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to init lock bucket: %w", err)
	}

	return &natsLockManager{nc: nc, js: js, kv: kv}, nil
}

func (m *natsLockManager) Close() error {
	m.nc.Close()
	return nil
}

// holderInfo is one caller's own slot within a lockValue. TTL is that
// holder's own originally-requested ttl: since a single JetStream KV key
// carries exactly one message-level TTL, a shared entry with more than one
// holder republishes at the maximum of every current holder's TTL (see
// maxHolderTTL) on every membership change, so no legitimate holder's
// window is ever cut short by a different holder joining, renewing, or
// leaving. This is a deliberate, named simplification, not independent
// per-holder expiry: PLAN.md Section 13 names shared mode for short-lived
// reads (simulate, show-info, fact queries), which do not need it.
type holderInfo struct {
	ID  string        `json:"id"`
	TTL time.Duration `json:"ttl"`
}

// lockValue is the JSON envelope stored as a lock key's value, replacing
// the previous decorative timestamp string. It carries enough to support
// ModeShared (more than one concurrent holder) and to let KeepAlive/Release
// on a shared entry preserve every other holder's membership across a
// read-modify-CAS-write cycle.
type lockValue struct {
	Mode    Mode         `json:"mode"`
	Holders []holderInfo `json:"holders"`
}

// maxHolderTTL returns the largest TTL among holders, or 0 if holders is
// empty or every entry's TTL is non-positive (meaning "rely on the bucket's
// own failsafe ceiling only").
func maxHolderTTL(holders []holderInfo) time.Duration {
	var max time.Duration
	for _, h := range holders {
		if h.TTL > max {
			max = h.TTL
		}
	}
	return max
}

// itemIDValid rejects an itemID the underlying nats.go client's own key
// validation (unexported keyValid, gated on validKeyRe) would accept but
// which produces an empty token in the "$KV.<bucket>.<itemID>" subject
// this package builds (kvSubject): two consecutive dots. validKeyRe
// (`^[-/_=\.a-zA-Z0-9]+$`) allows any run of dots, with no check that they
// never sit adjacent, so a key like "a..b" passes kv.Create's own
// validation silently. The resulting subject is not one JetStream will
// ever acknowledge, so the failure mode is not a clean error but a real,
// fuzz-caught multi-second "nats: no response from stream" timeout
// (LESSONS_LEARNED.md), confirmed empirically, not guessed. A real caller
// only ever passes an inventory device ID (a UUID) or the scheduler's own
// fixed key here, neither of which can produce this, but a defensive,
// fast, clear rejection costs nothing and closes a real Schema/Injection
// Hardening finding for this phase's own new boundary rather than leaving
// it to whatever the server happens to do with a malformed subject.
func itemIDValid(itemID string) bool {
	return !strings.Contains(itemID, "..")
}

// kvSubject returns the raw NATS subject a KV bucket stores itemID's
// revisions under. This is NATS's own documented KV subject convention
// ("$KV.<bucket>.<key>"), used only by publishWithTTL below to reach a
// capability (refreshing a per-key TTL on an existing revision) the public
// jetstream.KeyValue.Update method does not expose in this client version:
// its signature accepts no options and always publishes with ttl=0,
// confirmed by reading nats.go v1.52.0's own source before relying on it
// (LESSONS_LEARNED.md).
func kvSubject(itemID string) string {
	return fmt.Sprintf("$KV.%s.%s", topology.LockBucketName, itemID)
}

// publishWithTTL CAS-publishes value as itemID's next revision, expecting
// expectedRevision to still be current, and sets ttl as that revision's own
// per-key expiry (skipped when ttl <= 0, matching jetstream.KeyTTL's own
// convention of "no per-key TTL", relying on the bucket-wide failsafe
// ceiling alone). itemID must already have passed a successful kv.Create's
// own key validation before this is ever called, so there is no new
// injection surface in building the subject by hand here.
func (m *natsLockManager) publishWithTTL(ctx context.Context, itemID string, value []byte, expectedRevision uint64, ttl time.Duration) (uint64, error) {
	msg := nats.Msg{Subject: kvSubject(itemID), Header: nats.Header{}, Data: value}
	opts := []jetstream.PublishOpt{jetstream.WithExpectLastSequencePerSubject(expectedRevision)}
	if ttl > 0 {
		opts = append(opts, jetstream.WithMsgTTL(ttl))
	}
	pa, err := m.js.PublishMsg(ctx, &msg, opts...)
	if err != nil {
		return 0, err
	}
	return pa.Sequence, nil
}

// Acquire attempts to lock itemID according to opts. See Manager.Acquire.
func (m *natsLockManager) Acquire(ctx context.Context, itemID string, ttl time.Duration, opts AcquireOptions) (Lease, error) {
	return acquireWithContention(ctx, opts, func(ctx context.Context) (Lease, error) {
		return m.tryAcquireOnce(ctx, itemID, ttl, opts)
	})
}

// sharedJoinRetryBase and sharedJoinRetryMax bound the internal retry delay
// tryAcquireOnce uses when a ModeShared join loses its own CAS race against
// a different, concurrent, equally-compatible ModeShared join or release
// (see tryAcquireOnce's own doc comment on why this is not the same thing
// as ContentionPolicy contention).
const (
	sharedJoinRetryBase = 5 * time.Millisecond
	sharedJoinRetryMax  = 100 * time.Millisecond
)

// tryAcquireOnce is a single, non-retrying acquire attempt from the calling
// AcquireOptions.Policy's point of view: PolicyReject's entire behavior,
// and the primitive acquireWithContention retries under
// PolicyQueue/PolicyPriority when this returns ErrLockHeld. It implements
// the atomic Compare-and-Swap lock acquisition using JetStream KV's Create
// (for a fresh itemID) and, for a shared join against an existing shared
// entry, a CAS Update via publishWithTTL.
//
// A shared join's own CAS can lose a race against a completely different,
// concurrent ModeShared join or release touching the same itemID, even
// though both operations are mutually compatible and both should
// ultimately succeed: this is optimistic-concurrency noise, not real
// contention in the AcquireOptions.Policy sense (nothing here is
// exclusive, and no live holder is being asked to yield anything), so it
// is retried internally in a bounded loop below regardless of Policy,
// rather than surfaced as ErrLockHeld for PolicyReject to fail on. A
// real, test-caught defect during this phase's own development: an
// earlier draft surfaced this as ErrLockHeld unconditionally, which made
// most of a burst of simultaneous shared joins fail outright under the
// default PolicyReject even though every one of them was requesting
// something the lock fully permits (FAILURE_PATTERNS.md).
func (m *natsLockManager) tryAcquireOnce(ctx context.Context, itemID string, ttl time.Duration, opts AcquireOptions) (Lease, error) {
	if err := validateTTL(ttl); err != nil {
		return nil, err
	}
	if ttl > 0 && ttl < minPositiveTTL {
		return nil, fmt.Errorf("ttl must be 0 or at least %v, got %v", minPositiveTTL, ttl)
	}
	if !itemIDValid(itemID) {
		return nil, fmt.Errorf("invalid itemID %q: must not contain consecutive dots", itemID)
	}
	holderID := uuid.New().String()

	var createOpts []jetstream.KVCreateOpt
	if ttl > 0 {
		createOpts = append(createOpts, jetstream.KeyTTL(ttl))
	}

	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		data, err := json.Marshal(lockValue{Mode: opts.Mode, Holders: []holderInfo{{ID: holderID, TTL: ttl}}})
		if err != nil {
			return nil, fmt.Errorf("failed to encode lock value for %s: %w", itemID, err)
		}

		// NATS JetStream KV's Create method only succeeds if the key DOES
		// NOT exist. This provides our atomic Compare-and-Swap lock
		// mechanism for the common, uncontended case.
		rev, err := m.kv.Create(ctx, itemID, data, createOpts...)
		if err == nil {
			return &natsLease{mgr: m, itemID: itemID, holderID: holderID, mode: opts.Mode, revision: rev, ttl: ttl, deadline: time.Now().Add(ttl)}, nil
		}
		if !errors.Is(err, jetstream.ErrKeyExists) {
			return nil, fmt.Errorf("failed to acquire lock for %s: %w", itemID, err)
		}

		// itemID already has a value. Only a shared request joining an
		// existing shared entry can succeed against it; every other
		// combination is real contention. Priority never overrides this:
		// it only changes how fast a caller retries in
		// acquireWithContention, never what a single attempt is allowed
		// to do to a live holder (see queue.go's own doc comment for why).
		if opts.Mode != ModeShared {
			return nil, ErrLockHeld
		}

		entry, getErr := m.kv.Get(ctx, itemID)
		if getErr != nil {
			if errors.Is(getErr, jetstream.ErrKeyNotFound) {
				// Raced with a concurrent release between Create's
				// failure above and this Get: itemID may be free again,
				// so retry the whole attempt (top of loop) rather than
				// reporting contention that may no longer be real.
				if waitErr := retry.Sleep(ctx, sharedJoinRetryBase, sharedJoinRetryMax, attempt); waitErr != nil {
					return nil, waitErr
				}
				continue
			}
			return nil, fmt.Errorf("failed to inspect lock for %s: %w", itemID, getErr)
		}

		var current lockValue
		if err := json.Unmarshal(entry.Value(), &current); err != nil {
			return nil, fmt.Errorf("failed to decode lock value for %s: %w", itemID, err)
		}
		if current.Mode != ModeShared {
			return nil, ErrLockHeld
		}

		joined := make([]holderInfo, 0, len(current.Holders)+1)
		joined = append(joined, current.Holders...)
		joined = append(joined, holderInfo{ID: holderID, TTL: ttl})
		joinedData, err := json.Marshal(lockValue{Mode: ModeShared, Holders: joined})
		if err != nil {
			return nil, fmt.Errorf("failed to encode lock value for %s: %w", itemID, err)
		}

		newRev, updateErr := m.publishWithTTL(ctx, itemID, joinedData, entry.Revision(), maxHolderTTL(joined))
		if updateErr == nil {
			return &natsLease{mgr: m, itemID: itemID, holderID: holderID, mode: ModeShared, revision: newRev, ttl: ttl, deadline: time.Now().Add(ttl)}, nil
		}
		if !errors.Is(updateErr, jetstream.ErrKeyExists) {
			return nil, fmt.Errorf("failed to join shared lock for %s: %w", itemID, updateErr)
		}

		// Lost the CAS race against a different, concurrent, compatible
		// modification (another join or a release): retry with fresh
		// state rather than failing a request the lock fundamentally
		// permits.
		if waitErr := retry.Sleep(ctx, sharedJoinRetryBase, sharedJoinRetryMax, attempt); waitErr != nil {
			return nil, waitErr
		}
	}
}

// natsLease represents a single successful Acquire against
// natsLockManager. holderID identifies this lease's own slot within
// itemID's lockValue; more than one lease can share an itemID under
// ModeShared, so itemID alone does not uniquely identify a lease.
type natsLease struct {
	mgr      *natsLockManager
	itemID   string
	holderID string
	mode     Mode
	revision uint64
	ttl      time.Duration
	deadline time.Time // this lease's own locally-computed deadline, from this process's own monotonic-carrying time.Now(); never read back from the stored value, and never compared against a timestamp produced by another process
}

func (l *natsLease) ID() string {
	return l.itemID
}

// KeepAlive proves this lease is still alive and refreshes itemID's
// per-key TTL. It first checks this lease's own locally-computed deadline
// (see natsLease.deadline) and fails fast, with no network call, if that
// has already passed: Section 16's monotonic-time clock-drift mitigation
// made concrete, since a process that has itself observed it is running
// past its own deadline self-fences immediately rather than only ever
// discovering staleness from a server-side CAS rejection.
//
// For an exclusive lease, this republishes the same single-holder value at
// its own tracked revision: no other party can have modified an exclusive
// entry, so no read-before-write is needed. For a shared lease, this reads
// the current entry first, confirms this lease's holderID is still
// present, and republishes the value it just read unchanged (so a
// concurrent join or another holder's own KeepAlive is never clobbered),
// with the TTL recomputed as the maximum across every current holder (see
// maxHolderTTL).
func (l *natsLease) KeepAlive(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if time.Now().After(l.deadline) {
		return fmt.Errorf("lease %s is no longer current: local deadline already passed", l.itemID)
	}

	if l.mode == ModeExclusive {
		data, err := json.Marshal(lockValue{Mode: ModeExclusive, Holders: []holderInfo{{ID: l.holderID, TTL: l.ttl}}})
		if err != nil {
			return fmt.Errorf("failed to keep alive lease %s: %w", l.itemID, err)
		}
		rev, err := l.mgr.publishWithTTL(ctx, l.itemID, data, l.revision, l.ttl)
		if err != nil {
			return fmt.Errorf("failed to keep alive lease %s: %w", l.itemID, err)
		}
		l.revision = rev
		l.deadline = time.Now().Add(l.ttl)
		return nil
	}

	// Shared mode: read-modify-CAS-write, retried on losing the CAS race
	// against a different, concurrent, equally-valid modification (another
	// holder's own join/KeepAlive/Release) rather than failing outright,
	// exactly like tryAcquireOnce's own shared-join retry loop and for the
	// identical reason: that is optimistic-concurrency noise, not evidence
	// this lease is actually stale.
	for attempt := 0; ; attempt++ {
		entry, err := l.mgr.kv.Get(ctx, l.itemID)
		if err != nil {
			return fmt.Errorf("failed to keep alive lease %s: %w", l.itemID, err)
		}
		var current lockValue
		if err := json.Unmarshal(entry.Value(), &current); err != nil {
			return fmt.Errorf("failed to keep alive lease %s: %w", l.itemID, err)
		}
		if !holderPresent(current.Holders, l.holderID) {
			return fmt.Errorf("lease %s is no longer current", l.itemID)
		}

		rev, err := l.mgr.publishWithTTL(ctx, l.itemID, entry.Value(), entry.Revision(), maxHolderTTL(current.Holders))
		if err == nil {
			l.revision = rev
			l.deadline = time.Now().Add(l.ttl)
			return nil
		}
		if !errors.Is(err, jetstream.ErrKeyExists) {
			return fmt.Errorf("failed to keep alive lease %s: %w", l.itemID, err)
		}
		if waitErr := retry.Sleep(ctx, sharedJoinRetryBase, sharedJoinRetryMax, attempt); waitErr != nil {
			return waitErr
		}
	}
}

// Release frees this lease's own slot in itemID. For an exclusive lease
// (always the sole holder), this is an atomic delete at the lease's own
// tracked revision. For a shared lease, this reads the current entry,
// removes exactly this lease's holderID, and either republishes the
// reduced holder list (preserving every other holder, with the TTL
// recomputed via maxHolderTTL) or, once the last holder is gone, deletes
// the key outright.
func (l *natsLease) Release(ctx context.Context) error {
	if l.mode == ModeExclusive {
		if err := l.mgr.kv.Delete(ctx, l.itemID, jetstream.LastRevision(l.revision)); err != nil {
			return fmt.Errorf("failed to release lease %s: %w", l.itemID, err)
		}
		return nil
	}

	// Shared mode: read-modify-CAS-write, retried on losing the CAS race
	// against a different, concurrent, equally-valid modification, the
	// same reasoning as KeepAlive's own retry loop above.
	for attempt := 0; ; attempt++ {
		entry, err := l.mgr.kv.Get(ctx, l.itemID)
		if err != nil {
			return fmt.Errorf("failed to release lease %s: %w", l.itemID, err)
		}
		var current lockValue
		if err := json.Unmarshal(entry.Value(), &current); err != nil {
			return fmt.Errorf("failed to release lease %s: %w", l.itemID, err)
		}

		remaining, removed := removeHolder(current.Holders, l.holderID)
		if !removed {
			return fmt.Errorf("lease %s is no longer current", l.itemID)
		}

		if len(remaining) == 0 {
			err := l.mgr.kv.Delete(ctx, l.itemID, jetstream.LastRevision(entry.Revision()))
			if err == nil {
				return nil
			}
			if !errors.Is(err, jetstream.ErrKeyExists) {
				return fmt.Errorf("failed to release lease %s: %w", l.itemID, err)
			}
			if waitErr := retry.Sleep(ctx, sharedJoinRetryBase, sharedJoinRetryMax, attempt); waitErr != nil {
				return waitErr
			}
			continue
		}

		data, err := json.Marshal(lockValue{Mode: ModeShared, Holders: remaining})
		if err != nil {
			return fmt.Errorf("failed to encode lock value for %s: %w", l.itemID, err)
		}
		if _, err := l.mgr.publishWithTTL(ctx, l.itemID, data, entry.Revision(), maxHolderTTL(remaining)); err != nil {
			if !errors.Is(err, jetstream.ErrKeyExists) {
				return fmt.Errorf("failed to release lease %s: %w", l.itemID, err)
			}
			if waitErr := retry.Sleep(ctx, sharedJoinRetryBase, sharedJoinRetryMax, attempt); waitErr != nil {
				return waitErr
			}
			continue
		}
		return nil
	}
}

// holderPresent reports whether id appears in holders.
func holderPresent(holders []holderInfo, id string) bool {
	for _, h := range holders {
		if h.ID == id {
			return true
		}
	}
	return false
}

// removeHolder returns holders with id removed (as a fresh slice, so the
// caller's own copy is never mutated in place) and whether id was found.
func removeHolder(holders []holderInfo, id string) ([]holderInfo, bool) {
	out := make([]holderInfo, 0, len(holders))
	found := false
	for _, h := range holders {
		if h.ID == id {
			found = true
			continue
		}
		out = append(out, h)
	}
	return out, found
}
