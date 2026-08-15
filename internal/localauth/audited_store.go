// The audit decorator over the credential store.
//
// A decorator rather than record calls placed inside entStore or in the
// handlers above it, for the reason internal/access's own audited store
// already gives at length: there are two write surfaces over a local
// credential, the controller's administrative subcommands and the web UI's
// account page, and they share no handler. Recording from either would
// cover one and leave a gap invisible in every test that exercised the
// covered one. Whatever holds the wrapped value is audited, including the
// surface nobody remembered.
//
// Every method is written out rather than promoted from an embedded
// interface. Embedding would compile forever, and a method added to Store
// later would be delegated silently and unaudited, which is the same hole
// in a different shape. Declaring all of them means the day Store grows,
// this file stops compiling and somebody has to decide what the new method
// records.
//
// # What is recorded, and what is deliberately not
//
// Recorded: that a password was SET, CHANGED or that an account was
// UNLOCKED, by whom, and for which subject. Never the password, never the
// hash, and never the parameters it was derived at. The projection this
// package returns has no field either could occupy, so there is nothing
// here that could leak one by accident, which is the point of that design
// rather than a happy coincidence.
//
// NOT recorded here: a failed sign-in, or an account locking. Those are
// high-frequency, attacker-triggerable events, and an unauthenticated
// caller who can append an unbounded number of rows to a durable audit
// table has a denial of service against the database rather than an alarm.
// They go to the structured log instead, which is rate-bounded by the
// logging path and is where an operator watching for credential stuffing
// would look. The activity stream is a record of what OPERATORS did.
package localauth

import (
	"context"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
)

// auditedStore decorates a Store so every credential write leaves a line in
// the activity stream.
type auditedStore struct {
	store    Store
	recorder activity.Recorder
	actors   activity.ActorSource
	logger   *slog.Logger
}

// NewAuditedStore wraps store so its writes are recorded.
//
// actors resolves who is making the change from the context, the same
// source internal/access's audited store uses, so an administrative
// subcommand and a web request are attributed by the same rule rather than
// by two.
func NewAuditedStore(store Store, recorder activity.Recorder, actors activity.ActorSource, logger *slog.Logger) Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &auditedStore{store: store, recorder: recorder, actors: actors, logger: logger}
}

// Authenticate is NOT audited. See this file's own doc for why a
// high-frequency, attacker-triggerable event does not go in a durable
// table.
func (s *auditedStore) Authenticate(ctx context.Context, email, password string) (*Account, error) {
	return s.store.Authenticate(ctx, email, password)
}

// Account is a read and records nothing.
func (s *auditedStore) Account(ctx context.Context, email string) (*Account, error) {
	return s.store.Account(ctx, email)
}

// SetPassword records an administrative write.
func (s *auditedStore) SetPassword(ctx context.Context, email, password string, mustChange bool) error {
	if err := s.store.SetPassword(ctx, email, password, mustChange); err != nil {
		return err
	}
	s.record(ctx, activity.ActionUpdated, email, "password set")
	return nil
}

// ChangePassword records a self-service write.
//
// Recorded as a distinct note from SetPassword, because who chose the
// password is exactly what an access review needs to know: a password an
// administrator set is one somebody else has seen.
func (s *auditedStore) ChangePassword(ctx context.Context, email, oldPassword, newPassword string) error {
	if err := s.store.ChangePassword(ctx, email, oldPassword, newPassword); err != nil {
		return err
	}
	s.record(ctx, activity.ActionUpdated, email, "password changed by its owner")
	return nil
}

// Unlock records the out-of-band recovery.
//
// Worth a durable line more than most: an unlock is somebody with host
// access intervening on an account that was under attack or locked out, and
// it is the one event here that says both things at once.
func (s *auditedStore) Unlock(ctx context.Context, email string) error {
	if err := s.store.Unlock(ctx, email); err != nil {
		return err
	}
	s.record(ctx, activity.ActionUpdated, email, "account unlocked")
	return nil
}

// record appends one entry, logging rather than failing on a recording
// error.
//
// The write already happened by the time this runs, so reporting it as
// failed would be a worse lie than a missing audit line. That is the same
// decision internal/access's audited store makes, in the open, for the same
// reason.
//
// The object is the USER, not the credential row. An operator reading the
// stream is looking for what happened to an account, and the credential's
// own row id is not an identifier anything else in the system refers to.
// That also satisfies activity.Entry.Validate, which requires a positive
// ObjectID: an entry naming no object is not evidence of a change.
//
// The id costs one extra read on a write path that has already spent tens
// of milliseconds in Argon2id, which is why it is fetched here rather than
// threaded through every method's return type.
func (s *auditedStore) record(ctx context.Context, action activity.Action, subject, note string) {
	account, err := s.store.Account(ctx, subject)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to resolve an account for the activity stream",
			slog.String("subject", subject), slog.String("note", note), slog.String("error", err.Error()))
		return
	}

	entry := activity.Entry{
		Actor:      s.actors(ctx),
		Action:     action,
		ObjectKind: activity.KindUser,
		ObjectID:   account.UserID,
		ObjectName: subject + " (" + note + ")",
	}
	if err := s.recorder.Record(ctx, entry); err != nil {
		s.logger.ErrorContext(ctx, "failed to record a credential change in the activity stream",
			slog.String("subject", subject), slog.String("note", note), slog.String("error", err.Error()))
	}
}
