// Package activity is the activity stream: an append-only record of who
// changed which managed object, readable by an operator and queryable by an
// auditor.
//
// It exists because the platform already knew all of this and kept none of
// it. internal/auth's Recorder writes every admission decision to log/slog,
// which answers "was this request allowed" for whoever is reading the
// container's stdout at the time; nothing anywhere recorded the
// administrative writes themselves. "Who put that subject in the admin team
// last Thursday" had no answer in the system at all.
//
// The split with internal/auth's Recorder is the same one internal/access
// draws with internal/auth generally. That records a *decision* about a
// request, evaluated on every request, and its natural sink is a log. This
// records a *change* to a managed object, which happens rarely, has to
// survive process restarts and log rotation, and has to be queryable by
// object and by actor. One is telemetry and the other is evidence.
//
// The stream is written by a store decorator (internal/access's audited
// store), never by calls placed in handlers. A decorator cannot be
// half-wired: whatever holds the wrapped store is covered, including the
// web UI's writers, which never pass through an API handler at all.
// Sprinkled recording calls are how an audit trail ends up with holes
// nobody notices until the day somebody looks.
package activity

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ErrInvalidEntry is returned when an entry could not be evidence of
// anything: no actor, no action, no object.
var ErrInvalidEntry = errors.New("activity: entry does not describe a change")

// ErrNotFound is returned when an id names no entry.
var ErrNotFound = errors.New("activity: entry not found")

// Action is what happened to an object.
//
// A closed vocabulary, unlike announce.Level, because the only writer is
// this module's own decorator. A level outside announce's set means a newer
// version knows a word this one does not, which is worth rendering
// neutrally; an action outside this set means the writer has a bug, and
// recording it would put a word in the audit trail that no reader and no
// filter can interpret.
type Action string

// The actions, in the order they appear in an object's life.
const (
	ActionCreated  Action = "created"
	ActionUpdated  Action = "updated"
	ActionDeleted  Action = "deleted"
	ActionAttested Action = "attested"
)

// validActions is the membership test a write applies.
var validActions = map[Action]bool{
	ActionCreated:  true,
	ActionUpdated:  true,
	ActionDeleted:  true,
	ActionAttested: true,
}

// Valid reports whether a is one of the declared actions.
func (a Action) Valid() bool { return validActions[a] }

// The object kinds this module's own decorator writes.
//
// Open, unlike Action, and that asymmetry is deliberate. A new kind arrives
// with every phase that makes a new object administrable, and requiring an
// edit here first would mean either a phase that cannot audit its own
// writes or a constant added months before anything writes it. What a write
// refuses is a *blank* kind, which describes nothing.
const (
	KindOrganization = "organization"
	KindTeam         = "team"
	KindUser         = "user"
	KindBinding      = "role binding"
	KindContact      = "contact"

	// KindEncryptionKey is a master encryption key, named by its short
	// fingerprint and never by its value (internal/keyregistry).
	KindEncryptionKey = "encryption key"
)

// Entry is one recorded change.
//
// Every field is captured at the moment of the change rather than resolved
// when the entry is read, including ObjectName. That is the opposite of how
// a list renders a reference to another record, and both are right: a list
// describes what is true now, so a rename should show through, while this
// describes what was true then, so it must not.
type Entry struct {
	ID int

	// Actor is the authenticated subject that made the change. Never
	// blank: see ErrUnattributed in internal/access for why an entry with
	// no actor is refused rather than recorded as "unknown".
	Actor string

	Action Action

	// ObjectKind, ObjectID and ObjectName identify what changed. ObjectID
	// is kept even for a deleted object, because "role binding 12" is still
	// the identifier the rest of the trail refers to it by.
	ObjectKind string
	ObjectID   int
	ObjectName string

	// At is when it happened, from the database's own insert time.
	At time.Time
}

// Describe renders the entry as one sentence, the same wording the stream
// and any future notification would use, so the two cannot drift.
func (e Entry) Describe() string {
	subject := e.ObjectKind
	if e.ObjectName != "" {
		subject += " " + e.ObjectName
	}
	return e.Actor + " " + string(e.Action) + " " + subject
}

// Validate reports whether an entry is evidence of a change.
//
// Called by the store rather than by the decorator, so that a second writer
// added later cannot skip it.
func (e Entry) Validate() error {
	switch {
	case strings.TrimSpace(e.Actor) == "":
		return ErrInvalidEntry
	case !e.Action.Valid():
		return ErrInvalidEntry
	case strings.TrimSpace(e.ObjectKind) == "":
		return ErrInvalidEntry
	case e.ObjectID <= 0:
		return ErrInvalidEntry
	}
	return nil
}

// ActorSource reports who is acting in ctx.
//
// A function rather than an interface because there is one method and every
// implementation is a closure over something a composition root already
// holds. The Controller's reads the authenticated identity the auth
// middleware placed in the request context; a background path that has no
// user supplies its own constant, deliberately and visibly, rather than
// falling through to a blank.
type ActorSource func(ctx context.Context) string

// Query is a list request over the stream.
type Query struct {
	// After is a keyset cursor: the lowest id already seen. The stream
	// reads newest first, so paging walks downward from it. Zero starts at
	// the newest entry.
	After int

	// Limit bounds the page. Zero means the store's default.
	Limit int

	// Actor narrows to one subject's changes. Empty means every subject.
	Actor string

	// ObjectKind and ObjectID narrow to one object's history. ObjectID is
	// only applied alongside a kind, since an id alone means nothing across
	// tables.
	ObjectKind string
	ObjectID   int
}

// Recorder is the write half, which is all the decorator needs. Kept
// separate from Store so that a package wiring auditing does not gain the
// ability to read every other tenant's history as a side effect.
type Recorder interface {
	// Record appends one entry.
	//
	// It returns an error rather than swallowing one, so a caller can
	// decide what a failed recording means. The audited store logs it and
	// lets the write stand, because the write already happened and
	// reporting it as failed would be a worse lie than a missing audit
	// line. That decision belongs at the call site, in the open, not
	// hidden behind a signature that cannot express failure.
	Record(ctx context.Context, e Entry) error
}

// Store is the whole port: append, and read back.
type Store interface {
	Recorder

	// List returns entries newest first.
	List(ctx context.Context, q Query) ([]Entry, error)

	// Get loads one entry.
	Get(ctx context.Context, id int) (Entry, error)
}
