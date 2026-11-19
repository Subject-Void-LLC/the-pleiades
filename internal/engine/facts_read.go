// Package engine: the read side of Phase 41's facts. A run reads a device's
// stored facts through FactReader, set beside FactRecorder, so the template
// root and the limit matcher never touch a store directly (Phase 41e builds
// on it).
package engine

import (
	"context"
	"errors"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// FactReader reads what a store holds. Each tier's store is behind it: the
// Controller's database, the Crawl CLI's files. It must be safe for concurrent
// use.
type FactReader interface {
	// Current is each of device's keys, by the newest value confirmed.
	Current(ctx context.Context, device string) ([]facts.Entry, error)
	// History is every value device's key has had, oldest first.
	History(ctx context.Context, device, key string) ([]facts.Entry, error)
}

// WithFactReader gives the run a way to read stored facts. Without it a read
// reports that no store is wired, never an empty answer.
func WithFactReader(fr FactReader) ExecutorOption {
	return func(x *Executor) {
		x.factReader = fr
	}
}

// ErrNoFactReader is returned by a read when the executor was built without
// WithFactReader: an unwired store must not look like a device with no facts.
var ErrNoFactReader = errors.New("engine: no fact reader is wired, so stored facts cannot be read")

// StoredFact is one fact as a run reads it: its value and how old it is, which
// every read carries (Phase 41e's age rule). Age is measured from when the
// value was last confirmed, not when it changed.
type StoredFact struct {
	Key   string
	Value any
	Age   time.Duration
	Known bool
}

// ReadFact returns device's current stored value for key, with its age at now.
// A key the device has no fact for is Known false, never an error.
func (x *Executor) ReadFact(ctx context.Context, device, key string, now time.Time) (StoredFact, error) {
	if x.factReader == nil {
		return StoredFact{}, ErrNoFactReader
	}
	entries, err := x.factReader.Current(ctx, device)
	if err != nil {
		return StoredFact{}, err
	}
	for _, e := range entries {
		if e.Key == key {
			return StoredFact{Key: key, Value: e.Value, Age: now.Sub(e.LastSeenAt), Known: true}, nil
		}
	}
	return StoredFact{Key: key}, nil
}
