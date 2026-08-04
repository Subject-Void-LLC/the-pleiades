package topology

import (
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
