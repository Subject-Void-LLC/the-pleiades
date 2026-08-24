package topology

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const (
	// DedupBucketName is the JetStream KV bucket internal/event's
	// NATS-backed Idempotent Consumer dedup store uses to remember which
	// idempotency keys it has already processed.
	DedupBucketName = "Pleiades_Dedup"

	// DedupDefaultTTL bounds how long a "seen" marker is remembered. It
	// only needs to outlive the longest realistic redelivery window
	// (bounded by MaxDeliverDefault redeliveries at consumerAckWait
	// apiece), not forever; unlike internal/lock's bucket, which uses its
	// TTL only as an absolute failsafe, this TTL is load-bearing for
	// correctness (see internal/event's dedup decorator), so it is sized
	// generously above that window rather than left at a failsafe scale.
	DedupDefaultTTL = 24 * time.Hour
)

// DedupBucketConfig returns the JetStream KV bucket configuration for the
// Idempotent Consumer's dedup store (PLAN.md Section 26.2), mirroring the
// bucket-per-concern pattern internal/lock/nats.go already established for
// its own, unrelated "Pleiades_Locks" bucket.
func DedupBucketConfig() jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{
		Bucket: DedupBucketName,
		TTL:    DedupDefaultTTL,
	}
}

const (
	// LockBucketName is the JetStream KV bucket internal/lock's
	// NATS-backed Manager uses for both halves of PLAN.md Section 13's
	// locking: the leader-election leases cmd/controller holds, and the
	// per-device execution leases cmd/runner takes before executing
	// against a device.
	LockBucketName = "Pleiades_Locks"

	// LockMarkerTTL bounds how long a delete/expiry marker persists in
	// the bucket's underlying stream once a key is removed.
	// jetstream.KeyValueConfig requires a non-zero LimitMarkerTTL before
	// it will honor any per-key TTL at all (jetstream.KeyTTL), which is
	// what lets a lock genuinely expire rather than be reclaimed by
	// clients comparing wall clocks. The value itself only affects
	// watcher notification, never lock correctness, so a modest fixed
	// duration is enough.
	//
	// Setting it is also this bucket's real minimum server-version
	// requirement: a nats-server older than 2.11 rejects LimitMarkerTTL
	// outright ("limit marker TTLs not supported by server"), confirmed
	// empirically against a real nats:2.10 container, so
	// lock.NewNatsLockManager simply fails to construct against one
	// rather than silently falling back to a mode that cannot honor ttl.
	LockMarkerTTL = time.Minute

	// LockBucketTTL is the bucket-wide absolute failsafe ceiling: no
	// lock can outlive it regardless of what ttl a caller requests, or
	// of any bug in internal/lock's own per-key TTL logic. Unlike
	// DedupDefaultTTL above, which is load-bearing for correctness, this
	// one is only a backstop.
	LockBucketTTL = 24 * time.Hour
)

// LockBucketConfig returns the JetStream KV bucket configuration for the
// distributed lock store.
//
// It lives here rather than inline in internal/lock for the reason this
// package exists at all: cmd/controller and cmd/runner each construct
// their own lock Manager on startup, and each construction issues a
// CreateOrUpdateKeyValue that rewrites this bucket's underlying stream
// configuration. Several roots reshaping one JetStream object on startup
// is this codebase's established pattern, not a defect (EnsureStream
// above does the same for the main stream from three roots), but it only
// stays safe while there is exactly one place the shape is written down.
// This was the last JetStream object in the module whose shape was
// declared outside this package, which made both this package's own doc
// comment and internal/archtest's ("internal/topology ... is the one
// place jetstream.StreamConfig/ConsumerConfig/KeyValueConfig shapes are
// declared") false at the time they were written.
// TestOnlyTopologyDeclaresJetStreamShapes is what makes them true.
func LockBucketConfig() jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{
		Bucket:         LockBucketName,
		TTL:            LockBucketTTL,
		LimitMarkerTTL: LockMarkerTTL,
	}
}

// BindLockBucket applies role to the distributed lock bucket, mirroring
// BindStream exactly.
//
// The bucket gets the same treatment as the stream, and its case is the
// more urgent of the two. It is reshaped from both cmd/controller and
// cmd/runner on every process start through
// js.CreateOrUpdateKeyValue(ctx, LockBucketConfig()), which is the same
// multi-writer provisioning defect FAILURE_PATTERNS.md #178 records for
// the stream, but the consequence is worse than a wrong retention window:
// this bucket carries every leader-election lease and every per-device
// execution lease, and internal/archtest's own comment already records
// that a lowered TTL here "lets two runners execute against one device".
// Fixing the retention budget while leaving the safety-critical bucket on
// the defective pattern would have been the wrong order of priority.
//
// A reader creates the bucket when it is absent, for the same reason
// AttachStream does: a Runner that cannot acquire a device lease because
// nobody has provisioned a bucket yet is unavailable for no benefit, and
// nothing orders the Controller first.
func BindLockBucket(ctx context.Context, js jetstream.JetStream, role StreamRole) (jetstream.KeyValue, error) {
	switch role {
	case StreamProvisioner:
		kv, err := js.CreateOrUpdateKeyValue(ctx, LockBucketConfig())
		if err != nil {
			return nil, fmt.Errorf("failed to provision the %s bucket: %w", LockBucketName, err)
		}
		return kv, nil

	case StreamReader:
		kv, err := js.KeyValue(ctx, LockBucketName)
		if err == nil {
			return kv, nil
		}
		if !errors.Is(err, jetstream.ErrBucketNotFound) {
			return nil, fmt.Errorf("failed to read the %s bucket: %w", LockBucketName, err)
		}
		created, err := js.CreateOrUpdateKeyValue(ctx, LockBucketConfig())
		if err != nil {
			return nil, fmt.Errorf("failed to create the missing %s bucket: %w", LockBucketName, err)
		}
		return created, nil

	default:
		return nil, fmt.Errorf("invalid stream role %d: use topology.StreamProvisioner or topology.StreamReader", int(role))
	}
}
