package event

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// natsDedupStore is the NATS-KV-backed DedupStore adapter, following the
// same bucket-per-concern idiom internal/lock/nats.go already established
// for its own, unrelated "Pleiades_Locks" bucket.
//
// Unlike inProcessDedupStore, MarkSeen's ttl parameter is not honored
// per-call: JetStream KV's TTL is configured once, bucket-wide, at bucket
// creation (see topology.DedupBucketConfig), not per key on Put. Every key
// in this bucket expires after that one configured TTL regardless of what
// ttl a given MarkSeen call was passed. This is stated here explicitly
// rather than silently ignored, matching this codebase's convention of
// naming a real, permanent adapter limitation instead of implying a
// per-call guarantee that does not exist.
type natsDedupStore struct {
	kv jetstream.KeyValue
}

// NewNatsDedupStore wraps an already-provisioned JetStream KV bucket (see
// topology.DedupBucketConfig, topology.DedupBucketName) as a DedupStore.
func NewNatsDedupStore(kv jetstream.KeyValue) DedupStore {
	return &natsDedupStore{kv: kv}
}

func (s *natsDedupStore) SeenRecently(ctx context.Context, key string) (bool, error) {
	_, err := s.kv.Get(ctx, key)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return false, nil
	}
	return false, fmt.Errorf("dedup store get failed for %s: %w", key, err)
}

func (s *natsDedupStore) MarkSeen(ctx context.Context, key string, _ time.Duration) error {
	if _, err := s.kv.Put(ctx, key, []byte(time.Now().UTC().String())); err != nil {
		return fmt.Errorf("dedup store put failed for %s: %w", key, err)
	}
	return nil
}
