// The audit decorator that records a newly registered key in the activity
// stream.
package keyregistry

import (
	"context"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
)

// auditedStore records every newly registered key in the activity stream,
// the same decorator shape internal/access and internal/localauth use for
// their own writes.
type auditedStore struct {
	store    Store
	recorder activity.Recorder
	actors   activity.ActorSource
	logger   *slog.Logger
}

// NewAuditedStore wraps store so that a Register which creates a row also
// writes an activity entry naming the key by its short fingerprint.
//
// A Register that finds the key already recorded writes nothing, because
// nothing happened. A failure to record the entry is logged rather than
// returned, matching every other audited store here: the key is registered
// either way, and refusing the write because the stream could not be told
// would turn an audit fault into an outage.
func NewAuditedStore(store Store, recorder activity.Recorder, actors activity.ActorSource, logger *slog.Logger) Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &auditedStore{store: store, recorder: recorder, actors: actors, logger: logger}
}

// Register implements Store.
func (s *auditedStore) Register(ctx context.Context, r Record) (Record, bool, error) {
	stored, created, err := s.store.Register(ctx, r)
	if err != nil || !created {
		return stored, created, err
	}
	entry := activity.Entry{
		Actor:      s.actors(ctx),
		Action:     activity.ActionCreated,
		ObjectKind: activity.KindEncryptionKey,
		ObjectID:   stored.ID,
		ObjectName: stored.Describe(),
	}
	if err := s.recorder.Record(ctx, entry); err != nil {
		s.logger.ErrorContext(ctx, "failed to record an encryption key in the activity stream",
			slog.String("key", Short(stored.Fingerprint)), slog.String("error", err.Error()))
	}
	return stored, created, nil
}

// Get implements Store.
func (s *auditedStore) Get(ctx context.Context, fingerprint string) (Record, error) {
	return s.store.Get(ctx, fingerprint)
}
