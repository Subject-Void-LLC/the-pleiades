package access

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
)

// ErrUnattributed is returned when a write arrives with no identifiable
// actor and the store is auditing.
//
// The write is refused rather than recorded against "unknown", and that is
// the whole posture. An audit trail containing unattributed rows is worse
// than one that is missing them: it looks complete, so a reader scanning it
// concludes those changes were reviewed when nobody knows who made them. A
// refusal is loud, is fixed in one line at the composition root, and cannot
// be mistaken for evidence.
//
// It is not a fail-closed audit in the strong sense. A path that genuinely
// has no user, a future scheduler firing an unattended run, supplies its
// own constant actor at the composition root, deliberately and visibly.
// What is refused is falling through to a blank.
var ErrUnattributed = errors.New("access: refusing an unattributed write to an audited store")

// auditedStore decorates a Store so that every write it accepts leaves a
// line in the activity stream.
//
// A decorator rather than recording calls placed in handlers, and the
// difference is not stylistic. This codebase has two write surfaces over
// these five entities: the JSON API's handlers, and the web UI's own
// writers, which reach the store directly and pass through no API handler
// at all. Recording from handlers would have covered one of them, and the
// gap would have been invisible in every test that exercised the covered
// one. Whatever holds the wrapped value is audited, including the surface
// nobody remembered.
//
// Every method is written out rather than promoted from an embedded
// interface. Embedding would compile forever: a method added to Store later
// would be delegated silently and unaudited, which is the same hole in a
// different shape. Declaring all of them means the day Store grows, this
// file stops compiling and somebody has to decide what the new method
// records.
//
// Known limitation, stated rather than hidden: the write and its recording
// are two operations, not one transaction. A crash between them leaves the
// change made and unrecorded. Closing that would mean both this store and
// the activity store resolving a transaction-scoped client from the context
// the way internal/inventory's repository already does, and every method
// here that opens its own transaction being reworked first, since a nested
// begin would be a second transaction rather than a participant. That is a
// real piece of work and it is not this one, so the gap is named here
// instead of being quietly left for a reader to discover.
type auditedStore struct {
	store    Store
	recorder activity.Recorder
	actors   activity.ActorSource
	logger   *slog.Logger
}

// NewAuditedStore wraps store so every accepted write is recorded.
//
// actors reports the acting subject for a context; see ErrUnattributed for
// what a blank one costs. A nil recorder or a nil actors source is a wiring
// mistake rather than a mode: this returns the undecorated store and says
// so, because silently auditing nothing is exactly the failure the
// decorator exists to prevent.
func NewAuditedStore(store Store, recorder activity.Recorder, actors activity.ActorSource, logger *slog.Logger) Store {
	if logger == nil {
		logger = slog.Default()
	}
	if store == nil || recorder == nil || actors == nil {
		logger.Warn("access: auditing is not wired, administrative writes will not be recorded")
		return store
	}
	return &auditedStore{store: store, recorder: recorder, actors: actors, logger: logger}
}

// actor resolves who is acting, before any write happens.
//
// Resolved first, deliberately. Refusing after the write would leave the
// change made and the caller told it failed, which is the one outcome worse
// than either.
func (s *auditedStore) actor(ctx context.Context) (string, error) {
	subject := strings.TrimSpace(s.actors(ctx))
	if subject == "" {
		return "", ErrUnattributed
	}
	return subject, nil
}

// record appends one entry for a write that already succeeded.
//
// A failed recording is logged and does not fail the write. The write has
// happened; returning an error would tell the caller their change was
// rejected when it was applied, and a caller that retried would then make
// it twice. The log line is at Error because a control plane that has
// stopped recording who changed what is a real incident, not a detail.
func (s *auditedStore) record(ctx context.Context, actor string, action activity.Action, kind string, id int, name string) {
	entry := activity.Entry{
		Actor:      actor,
		Action:     action,
		ObjectKind: kind,
		ObjectID:   id,
		ObjectName: name,
	}
	if err := s.recorder.Record(ctx, entry); err != nil {
		s.logger.ErrorContext(ctx, "access: the change was applied but not recorded in the activity stream",
			"error", err,
			"actor", actor,
			"action", string(action),
			"object_kind", kind,
			"object_id", id,
		)
	}
}

// Organizations.

func (s *auditedStore) CreateOrganization(ctx context.Context, org Organization) (Organization, error) {
	actor, err := s.actor(ctx)
	if err != nil {
		return Organization{}, err
	}
	created, err := s.store.CreateOrganization(ctx, org)
	if err != nil {
		return created, err
	}
	s.record(ctx, actor, activity.ActionCreated, activity.KindOrganization, created.ID, created.Name)
	return created, nil
}

func (s *auditedStore) UpdateOrganization(ctx context.Context, org Organization) error {
	actor, err := s.actor(ctx)
	if err != nil {
		return err
	}
	if err := s.store.UpdateOrganization(ctx, org); err != nil {
		return err
	}
	s.record(ctx, actor, activity.ActionUpdated, activity.KindOrganization, org.ID, org.Name)
	return nil
}

func (s *auditedStore) DeleteOrganization(ctx context.Context, id int) error {
	actor, err := s.actor(ctx)
	if err != nil {
		return err
	}
	// Read the name before the row is gone. Best effort: a failed read
	// must not block a legitimate delete, and an entry naming
	// "organization 7" with no name is still evidence the delete happened.
	name := ""
	if org, err := s.store.GetOrganization(ctx, id); err == nil {
		name = org.Name
	}
	if err := s.store.DeleteOrganization(ctx, id); err != nil {
		return err
	}
	s.record(ctx, actor, activity.ActionDeleted, activity.KindOrganization, id, name)
	return nil
}

func (s *auditedStore) AttestOrganization(ctx context.Context, id int, subject string) error {
	actor, err := s.actor(ctx)
	if err != nil {
		return err
	}
	name := ""
	if org, err := s.store.GetOrganization(ctx, id); err == nil {
		name = org.Name
	}
	if err := s.store.AttestOrganization(ctx, id, subject); err != nil {
		return err
	}
	// The stream records the caller resolved from the context, the same
	// actor every other line carries, not the subject argument. They are
	// the same value on every real path, because the composition root
	// takes both from the authenticated identity. If they ever diverge,
	// the honest thing for an audit trail to say is who called.
	s.record(ctx, actor, activity.ActionAttested, activity.KindOrganization, id, name)
	return nil
}

func (s *auditedStore) GetOrganization(ctx context.Context, id int) (Organization, error) {
	return s.store.GetOrganization(ctx, id)
}

func (s *auditedStore) ListOrganizations(ctx context.Context, q Query) ([]Organization, error) {
	return s.store.ListOrganizations(ctx, q)
}

// Teams.

func (s *auditedStore) CreateTeam(ctx context.Context, team Team) (Team, error) {
	actor, err := s.actor(ctx)
	if err != nil {
		return Team{}, err
	}
	created, err := s.store.CreateTeam(ctx, team)
	if err != nil {
		return created, err
	}
	s.record(ctx, actor, activity.ActionCreated, activity.KindTeam, created.ID, created.Name)
	return created, nil
}

func (s *auditedStore) UpdateTeam(ctx context.Context, team Team) error {
	actor, err := s.actor(ctx)
	if err != nil {
		return err
	}
	if err := s.store.UpdateTeam(ctx, team); err != nil {
		return err
	}
	s.record(ctx, actor, activity.ActionUpdated, activity.KindTeam, team.ID, team.Name)
	return nil
}

func (s *auditedStore) DeleteTeam(ctx context.Context, id int) error {
	actor, err := s.actor(ctx)
	if err != nil {
		return err
	}
	name := ""
	if team, err := s.store.GetTeam(ctx, id); err == nil {
		name = team.Name
	}
	if err := s.store.DeleteTeam(ctx, id); err != nil {
		return err
	}
	s.record(ctx, actor, activity.ActionDeleted, activity.KindTeam, id, name)
	return nil
}

func (s *auditedStore) AttestTeam(ctx context.Context, id int, subject string) error {
	actor, err := s.actor(ctx)
	if err != nil {
		return err
	}
	name := ""
	if team, err := s.store.GetTeam(ctx, id); err == nil {
		name = team.Name
	}
	if err := s.store.AttestTeam(ctx, id, subject); err != nil {
		return err
	}
	s.record(ctx, actor, activity.ActionAttested, activity.KindTeam, id, name)
	return nil
}

func (s *auditedStore) GetTeam(ctx context.Context, id int) (Team, error) {
	return s.store.GetTeam(ctx, id)
}

func (s *auditedStore) ListTeams(ctx context.Context, q TeamQuery) ([]Team, error) {
	return s.store.ListTeams(ctx, q)
}

// Users.

func (s *auditedStore) CreateUser(ctx context.Context, user User) (User, error) {
	actor, err := s.actor(ctx)
	if err != nil {
		return User{}, err
	}
	created, err := s.store.CreateUser(ctx, user)
	if err != nil {
		return created, err
	}
	s.record(ctx, actor, activity.ActionCreated, activity.KindUser, created.ID, created.Email)
	return created, nil
}

func (s *auditedStore) UpdateUser(ctx context.Context, user User) error {
	actor, err := s.actor(ctx)
	if err != nil {
		return err
	}
	if err := s.store.UpdateUser(ctx, user); err != nil {
		return err
	}
	s.record(ctx, actor, activity.ActionUpdated, activity.KindUser, user.ID, user.Email)
	return nil
}

func (s *auditedStore) DeleteUser(ctx context.Context, id int) error {
	actor, err := s.actor(ctx)
	if err != nil {
		return err
	}
	name := ""
	if user, err := s.store.GetUser(ctx, id); err == nil {
		name = user.Email
	}
	if err := s.store.DeleteUser(ctx, id); err != nil {
		return err
	}
	s.record(ctx, actor, activity.ActionDeleted, activity.KindUser, id, name)
	return nil
}

func (s *auditedStore) GetUser(ctx context.Context, id int) (User, error) {
	return s.store.GetUser(ctx, id)
}

func (s *auditedStore) ListUsers(ctx context.Context, q Query) ([]User, error) {
	return s.store.ListUsers(ctx, q)
}

// Bindings.

func (s *auditedStore) CreateBinding(ctx context.Context, binding Binding) (Binding, error) {
	actor, err := s.actor(ctx)
	if err != nil {
		return Binding{}, err
	}
	created, err := s.store.CreateBinding(ctx, binding)
	if err != nil {
		return created, err
	}
	s.record(ctx, actor, activity.ActionCreated, activity.KindBinding, created.ID, describeBinding(created))
	return created, nil
}

func (s *auditedStore) UpdateBinding(ctx context.Context, binding Binding) error {
	actor, err := s.actor(ctx)
	if err != nil {
		return err
	}
	if err := s.store.UpdateBinding(ctx, binding); err != nil {
		return err
	}
	// Re-read so the recorded description carries the resolved team and
	// scope names rather than the bare ids the submitted struct holds. A
	// failed read leaves the description to the submission, which is worse
	// but still true.
	described := describeBinding(binding)
	if stored, err := s.store.GetBinding(ctx, binding.ID); err == nil {
		described = describeBinding(stored)
	}
	s.record(ctx, actor, activity.ActionUpdated, activity.KindBinding, binding.ID, described)
	return nil
}

func (s *auditedStore) DeleteBinding(ctx context.Context, id int) error {
	actor, err := s.actor(ctx)
	if err != nil {
		return err
	}
	described := ""
	if binding, err := s.store.GetBinding(ctx, id); err == nil {
		described = describeBinding(binding)
	}
	if err := s.store.DeleteBinding(ctx, id); err != nil {
		return err
	}
	s.record(ctx, actor, activity.ActionDeleted, activity.KindBinding, id, described)
	return nil
}

func (s *auditedStore) GetBinding(ctx context.Context, id int) (Binding, error) {
	return s.store.GetBinding(ctx, id)
}

func (s *auditedStore) ListBindings(ctx context.Context, q BindingQuery) ([]Binding, error) {
	return s.store.ListBindings(ctx, q)
}

// Contacts.

func (s *auditedStore) CreateContact(ctx context.Context, contact Contact) (Contact, error) {
	actor, err := s.actor(ctx)
	if err != nil {
		return Contact{}, err
	}
	created, err := s.store.CreateContact(ctx, contact)
	if err != nil {
		return created, err
	}
	s.record(ctx, actor, activity.ActionCreated, activity.KindContact, created.ID, created.Name)
	return created, nil
}

func (s *auditedStore) UpdateContact(ctx context.Context, contact Contact) error {
	actor, err := s.actor(ctx)
	if err != nil {
		return err
	}
	if err := s.store.UpdateContact(ctx, contact); err != nil {
		return err
	}
	s.record(ctx, actor, activity.ActionUpdated, activity.KindContact, contact.ID, contact.Name)
	return nil
}

func (s *auditedStore) DeleteContact(ctx context.Context, id int) error {
	actor, err := s.actor(ctx)
	if err != nil {
		return err
	}
	name := ""
	if contact, err := s.store.GetContact(ctx, id); err == nil {
		name = contact.Name
	}
	if err := s.store.DeleteContact(ctx, id); err != nil {
		return err
	}
	s.record(ctx, actor, activity.ActionDeleted, activity.KindContact, id, name)
	return nil
}

func (s *auditedStore) GetContact(ctx context.Context, id int) (Contact, error) {
	return s.store.GetContact(ctx, id)
}

func (s *auditedStore) ListContacts(ctx context.Context, q ContactQuery) ([]Contact, error) {
	return s.store.ListContacts(ctx, q)
}

// describeBinding renders a grant as the sentence an auditor reads.
//
// A binding is the one entity here with no name of its own, and "role
// binding 12" tells a reader nothing about what was granted. The names come
// from the stored binding, which already resolves both, so this costs
// nothing beyond the read the delete and update paths already do.
func describeBinding(b Binding) string {
	team := b.TeamName
	if team == "" {
		team = fmt.Sprintf("team %d", b.TeamID)
	}

	target := string(b.ScopeType)
	switch {
	case b.SystemWide():
		target = "system"
	case b.ScopeName != "":
		target += " " + b.ScopeName
	default:
		target += fmt.Sprintf(" %d", b.ScopeID)
	}

	return fmt.Sprintf("%s %s for %s at %s", b.Effect, b.Role, team, target)
}
