package event

import (
	"context"
	"sync"
	"time"
)

// inProcessDedupStore is a map-backed DedupStore with lazy TTL expiry: an
// entry is only actually evicted the next time it (or any other key) is
// looked up or written, not by a background goroutine. This is sufficient
// for its two real uses (NewInProcessBus, tests): nothing here needs a
// bounded memory ceiling under sustained load the way a long-running NATS
// deployment would.
type inProcessDedupStore struct {
	mu      sync.Mutex
	seenAt  map[string]time.Time
	expires map[string]time.Time
}

// NewInProcessDedupStore constructs a DedupStore backed by an in-memory
// map, for use with NewInProcessBus or in tests that do not need a real
// NATS KV bucket.
func NewInProcessDedupStore() DedupStore {
	return &inProcessDedupStore{
		seenAt:  make(map[string]time.Time),
		expires: make(map[string]time.Time),
	}
}

func (s *inProcessDedupStore) SeenRecently(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	expiresAt, ok := s.expires[key]
	if !ok {
		return false, nil
	}
	if time.Now().After(expiresAt) {
		delete(s.seenAt, key)
		delete(s.expires, key)
		return false, nil
	}
	return true, nil
}

func (s *inProcessDedupStore) MarkSeen(_ context.Context, key string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.seenAt[key] = now
	s.expires[key] = now.Add(ttl)
	return nil
}
