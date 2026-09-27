package access

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entorg "github.com/Subject-Void-LLC/the-pleiades/internal/ent/organization"
	entbinding "github.com/Subject-Void-LLC/the-pleiades/internal/ent/rolebinding"
	entsession "github.com/Subject-Void-LLC/the-pleiades/internal/ent/session"
	entteam "github.com/Subject-Void-LLC/the-pleiades/internal/ent/team"
	entuser "github.com/Subject-Void-LLC/the-pleiades/internal/ent/user"
)

// defaultPageSize bounds a list with no explicit limit, and maxPageSize is
// the ceiling a caller cannot raise. Both belong to the store rather than to
// the request, for the reason every other paged port here states: a page
// size read from a query parameter with no cap is a request to hold every
// row in memory.
const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// entStore is the ent-backed Store.
type entStore struct{ client *ent.Client }

// NewEntStore builds a Store over an already-open ent client.
func NewEntStore(client *ent.Client) Store { return &entStore{client: client} }

// boundLimit applies the store's own paging bounds.
func boundLimit(limit int) int {
	if limit <= 0 {
		return defaultPageSize
	}
	return min(limit, maxPageSize)
}

// CreateOrganization persists a new tenancy boundary.
func (s *entStore) CreateOrganization(ctx context.Context, org Organization) (Organization, error) {
	name, err := normalizeName("organization", org.Name)
	if err != nil {
		return Organization{}, err
	}

	if err := validateClassification(org.Classification); err != nil {
		return Organization{}, err
	}

	created, err := s.client.Organization.Create().
		SetName(name).
		SetDescription(strings.TrimSpace(org.Description)).
		SetClassification(string(org.Classification)).
		SetChangeWindow(strings.TrimSpace(org.ChangeWindow)).
		SetFrozen(org.Frozen).
		SetFreezeReason(strings.TrimSpace(org.FreezeReason)).
		SetCostCentre(strings.TrimSpace(org.CostCentre)).
		SetTicketKey(strings.TrimSpace(org.TicketKey)).
		SetCmdbID(strings.TrimSpace(org.CMDBID)).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return Organization{}, fmt.Errorf("%w: %q", ErrExists, name)
		}
		return Organization{}, fmt.Errorf("access: creating organization %q: %w", name, err)
	}
	return hydrateOrganization(created), nil
}

// AttestOrganization records that subject confirmed this tenant's ownership
// information is current.
//
// The subject is a parameter rather than a field on Organization, so there is
// no shape of this call in which it could have come from a request body. An
// empty one is refused rather than stored: an attestation by nobody is the
// exact thing this is meant to make impossible.
func (s *entStore) AttestOrganization(ctx context.Context, id int, subject string) error {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return fmt.Errorf("%w: an attestation names who made it", ErrInvalidInput)
	}
	if _, err := s.GetOrganization(ctx, id); err != nil {
		return err
	}
	err := s.client.Organization.UpdateOneID(id).
		SetAttestedBy(subject).
		SetAttestedAt(time.Now().UTC()).
		Exec(ctx)
	return mapWriteError(err, "organization", id, "")
}

// GetOrganization loads one organization.
func (s *entStore) GetOrganization(ctx context.Context, id int) (Organization, error) {
	row, err := s.client.Organization.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return Organization{}, fmt.Errorf("%w: organization %d", ErrNotFound, id)
		}
		return Organization{}, fmt.Errorf("access: loading organization %d: %w", id, err)
	}
	return hydrateOrganization(row), nil
}

// ListOrganizations returns a page of organizations, in id order.
func (s *entStore) ListOrganizations(ctx context.Context, q Query) ([]Organization, error) {
	query := s.client.Organization.Query().
		Order(ent.Asc(entorg.FieldID)).
		Limit(boundLimit(q.Limit))
	if q.After > 0 {
		query = query.Where(entorg.IDGT(q.After))
	}
	if q.Search != "" {
		query = query.Where(entorg.NameContainsFold(q.Search))
	}

	rows, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("access: listing organizations: %w", err)
	}
	out := make([]Organization, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrateOrganization(row))
	}
	return out, nil
}

// UpdateOrganization renames an organization.
func (s *entStore) UpdateOrganization(ctx context.Context, org Organization) error {
	// Read first, so editing something that does not exist is a 404 rather
	// than whichever body complaint the validation happened to reach first.
	if _, err := s.GetOrganization(ctx, org.ID); err != nil {
		return err
	}
	name, err := normalizeName("organization", org.Name)
	if err != nil {
		return err
	}
	if err := validateClassification(org.Classification); err != nil {
		return err
	}

	// The attestation is deliberately absent from this builder. It is set
	// only by AttestOrganization, from the caller's identity, so no edit
	// path can carry a claim about who confirmed what.
	err = s.client.Organization.UpdateOneID(org.ID).
		SetName(name).
		SetDescription(strings.TrimSpace(org.Description)).
		SetClassification(string(org.Classification)).
		SetChangeWindow(strings.TrimSpace(org.ChangeWindow)).
		SetFrozen(org.Frozen).
		SetFreezeReason(strings.TrimSpace(org.FreezeReason)).
		SetCostCentre(strings.TrimSpace(org.CostCentre)).
		SetTicketKey(strings.TrimSpace(org.TicketKey)).
		SetCmdbID(strings.TrimSpace(org.CMDBID)).
		Exec(ctx)
	return mapWriteError(err, "organization", org.ID, name)
}

// DeleteOrganization removes an organization.
//
// The rows that pointed at it are not deleted. ent clears the edge instead,
// which for a Device means it becomes untenanted rather than destroyed:
// deleting an administrative grouping must never delete somebody's hardware,
// the same posture inventory.Set takes about its own members.
func (s *entStore) DeleteOrganization(ctx context.Context, id int) error {
	err := s.client.Organization.DeleteOneID(id).Exec(ctx)
	return mapWriteError(err, "organization", id, "")
}

// CreateTeam persists a new team inside an organization.
func (s *entStore) CreateTeam(ctx context.Context, team Team) (Team, error) {
	name, err := normalizeName("team", team.Name)
	if err != nil {
		return Team{}, err
	}
	if team.OrganizationID <= 0 {
		// Refused rather than defaulted, for the reason inventory.Set gives:
		// a team belonging to no organization would resolve against no
		// organization scope, and whether that made its grants reach
		// everything or nothing would depend on which way the resolver
		// failed.
		return Team{}, fmt.Errorf("%w: a team must belong to an organization", ErrInvalidInput)
	}

	created, err := s.client.Team.Create().
		SetName(name).
		SetDescription(strings.TrimSpace(team.Description)).
		SetOrganizationID(team.OrganizationID).
		AddUserIDs(team.UserIDs...).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return Team{}, fmt.Errorf("%w: %q", ErrExists, name)
		}
		return Team{}, fmt.Errorf("access: creating team %q: %w", name, err)
	}
	return s.GetTeam(ctx, created.ID)
}

// GetTeam loads one team with its membership.
func (s *entStore) GetTeam(ctx context.Context, id int) (Team, error) {
	row, err := s.client.Team.Query().
		Where(entteam.IDEQ(id)).
		WithOrganization().
		WithUsers().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Team{}, fmt.Errorf("%w: team %d", ErrNotFound, id)
		}
		return Team{}, fmt.Errorf("access: loading team %d: %w", id, err)
	}
	return hydrateTeam(row), nil
}

// ListTeams returns a page of teams.
func (s *entStore) ListTeams(ctx context.Context, q TeamQuery) ([]Team, error) {
	query := s.client.Team.Query().
		WithOrganization().
		WithUsers().
		Order(ent.Asc(entteam.FieldID)).
		Limit(boundLimit(q.Limit))
	if len(q.OrganizationIDs) > 0 {
		query = query.Where(entteam.HasOrganizationWith(entorg.IDIn(q.OrganizationIDs...)))
	}
	if q.After > 0 {
		query = query.Where(entteam.IDGT(q.After))
	}
	if q.Search != "" {
		query = query.Where(entteam.NameContainsFold(q.Search))
	}

	rows, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("access: listing teams: %w", err)
	}
	out := make([]Team, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrateTeam(row))
	}
	return out, nil
}

// UpdateTeam renames a team and replaces its membership.
//
// The organization is not updatable. Moving a team between tenants would
// silently re-scope every binding it holds, which is a migration rather than
// an edit, exactly as it is for an inventory.
func (s *entStore) UpdateTeam(ctx context.Context, team Team) error {
	if _, err := s.GetTeam(ctx, team.ID); err != nil {
		return err
	}
	name, err := normalizeName("team", team.Name)
	if err != nil {
		return err
	}
	// No attestation here, for the reason UpdateOrganization gives: it is
	// set only by AttestTeam, from the caller's identity.
	err = s.client.Team.UpdateOneID(team.ID).
		SetName(name).
		SetDescription(strings.TrimSpace(team.Description)).
		ClearUsers().
		AddUserIDs(team.UserIDs...).
		Exec(ctx)
	return mapWriteError(err, "team", team.ID, name)
}

// AttestTeam records that subject confirmed this team's ownership
// information is current. Same rule as AttestOrganization about where the
// subject comes from.
func (s *entStore) AttestTeam(ctx context.Context, id int, subject string) error {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return fmt.Errorf("%w: an attestation names who made it", ErrInvalidInput)
	}
	if _, err := s.GetTeam(ctx, id); err != nil {
		return err
	}
	err := s.client.Team.UpdateOneID(id).
		SetAttestedBy(subject).
		SetAttestedAt(time.Now().UTC()).
		Exec(ctx)
	return mapWriteError(err, "team", id, "")
}

// DeleteTeam removes a team and, with it, every grant it held.
//
// The bindings go because a binding names a team and means nothing without
// one. Leaving them would leave rows granting access to a principal that no
// longer exists, which is the definition of an orphaned permission.
//
// All of it in one transaction, and the reason is a defect this shipped
// with. The first version deleted the bindings in a bare loop and then the
// team. A team holding the deployment's last system-scope grant would have
// its other grants deleted one by one, then trip guardLastSystemGrant on
// that last one, and the whole call would return "refusing to delete the
// last system-scope grant" - which an operator reads as "nothing happened"
// while the grants already deleted were gone for good, with no record of
// which ones they had been.
//
// Two changes make that impossible. The refusal is decided before anything
// is written, by looking at what the team holds rather than by discovering
// it partway through. And every write happens inside one transaction, so
// any later failure, including one nobody predicted, takes the whole
// operation back with it.
func (s *entStore) DeleteTeam(ctx context.Context, id int) error {
	held, err := s.bindingsForTeam(ctx, id)
	if err != nil {
		return err
	}

	// Decided up front. Deleting a team removes every grant it holds at
	// once, so the question is whether the deployment would still have a
	// system-scope Allow afterwards, not whether each grant is individually
	// removable.
	if err := s.guardTeamDeletion(ctx, id, held); err != nil {
		return err
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("access: opening a transaction to delete team %d: %w", id, err)
	}
	for _, b := range held {
		if err := tx.RoleBinding.DeleteOneID(b.ID).Exec(ctx); err != nil {
			return rollback(tx, mapWriteError(err, "role binding", b.ID, ""))
		}
	}
	if err := tx.Team.DeleteOneID(id).Exec(ctx); err != nil {
		return rollback(tx, mapWriteError(err, "team", id, ""))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("access: committing the deletion of team %d: %w", id, err)
	}
	return nil
}

// bindingsForTeam reads every grant a team holds, without the page cap.
//
// Unpaged deliberately. The listing method is bounded because a caller
// reading a page wants a page; this caller needs the complete set, and
// stopping at a page boundary would delete exactly that many grants and then
// fail on a foreign key, which is the second half of the same defect.
func (s *entStore) bindingsForTeam(ctx context.Context, teamID int) ([]Binding, error) {
	rows, err := s.client.RoleBinding.Query().
		Where(entbinding.HasTeamWith(entteam.IDEQ(teamID))).
		WithTeam().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("access: listing the grants held by team %d: %w", teamID, err)
	}
	out := make([]Binding, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrateBinding(row))
	}
	return out, nil
}

// guardTeamDeletion refuses a deletion that would leave the deployment with
// no system-scope Allow, before anything is written.
func (s *entStore) guardTeamDeletion(ctx context.Context, teamID int, held []Binding) error {
	var losing bool
	for _, b := range held {
		if b.ScopeType == auth.ScopeSystem && b.Effect == auth.EffectAllow {
			losing = true
			break
		}
	}
	if !losing {
		return nil
	}

	remaining, err := s.client.RoleBinding.Query().
		Where(
			entbinding.ScopeTypeEQ(string(auth.ScopeSystem)),
			entbinding.EffectEQ(string(auth.EffectAllow)),
			entbinding.Not(entbinding.HasTeamWith(entteam.IDEQ(teamID))),
		).
		Count(ctx)
	if err != nil {
		return fmt.Errorf("access: counting system-scope grants: %w", err)
	}
	if remaining == 0 {
		return ErrLastSystemBinding
	}
	return nil
}

// rollback undoes a transaction and reports whichever failure matters.
//
// The original error is the one worth returning: a rollback that also fails
// is a second symptom of the same outage, and replacing the cause with it
// would tell the caller the least useful of the two things that went wrong.
func rollback(tx *ent.Tx, cause error) error {
	if err := tx.Rollback(); err != nil {
		return fmt.Errorf("%w (rollback also failed: %v)", cause, err)
	}
	return cause
}

// CreateUser persists a new identity.
func (s *entStore) CreateUser(ctx context.Context, user User) (User, error) {
	email, err := normalizeEmail(user.Email)
	if err != nil {
		return User{}, err
	}

	created, err := s.client.User.Create().SetEmail(email).Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return User{}, fmt.Errorf("%w: %q", ErrExists, email)
		}
		return User{}, fmt.Errorf("access: creating user %q: %w", email, err)
	}
	return s.GetUser(ctx, created.ID)
}

// GetUser loads one identity with its team membership.
func (s *entStore) GetUser(ctx context.Context, id int) (User, error) {
	row, err := s.client.User.Query().
		Where(entuser.IDEQ(id)).
		WithTeams().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return User{}, fmt.Errorf("%w: user %d", ErrNotFound, id)
		}
		return User{}, fmt.Errorf("access: loading user %d: %w", id, err)
	}
	return hydrateUser(row), nil
}

// ListUsers returns a page of identities.
func (s *entStore) ListUsers(ctx context.Context, q Query) ([]User, error) {
	query := s.client.User.Query().
		WithTeams().
		Order(ent.Asc(entuser.FieldID)).
		Limit(boundLimit(q.Limit))
	if q.After > 0 {
		query = query.Where(entuser.IDGT(q.After))
	}
	if q.Search != "" {
		query = query.Where(entuser.EmailContainsFold(q.Search))
	}

	rows, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("access: listing users: %w", err)
	}
	out := make([]User, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrateUser(row))
	}
	return out, nil
}

// UpdateUser changes an identity's address.
func (s *entStore) UpdateUser(ctx context.Context, user User) error {
	if _, err := s.GetUser(ctx, user.ID); err != nil {
		return err
	}
	email, err := normalizeEmail(user.Email)
	if err != nil {
		return err
	}
	err = s.client.User.UpdateOneID(user.ID).SetEmail(email).Exec(ctx)
	return mapWriteError(err, "user", user.ID, email)
}

// DeleteUser removes an identity. Its team memberships go with it; the teams
// themselves and their grants do not, because a team is a statement about a
// function rather than about the people currently performing it.
//
// Two things that outlive the row are deleted with it, and both were latent
// before local passwords existed.
//
// The LOCAL CREDENTIAL goes automatically: internal/ent/schema/user.go
// declares the edge with an ON DELETE CASCADE, so the database removes it.
// A verifiable password for an identity that no longer exists is a
// credential nobody is accountable for.
//
// The SESSIONS have to be deleted explicitly, because a session row carries
// its subject as a plain string with no foreign key back to User: it is the
// record of what was proven rather than a reference to a live identity, and
// that is deliberate (see internal/ent/schema/session.go). The consequence
// is that nothing in the database links the two, so a deleted user's cookie
// keeps authenticating until its absolute deadline, up to eight hours. That
// was already wrong when a session could only be minted from a token, and
// it became sharper the moment an account could hold a password.
//
// Deleted BEFORE the user row, so a failure leaves the account present with
// its sessions gone rather than absent with its sessions live. The first is
// an inconsistency an operator can see and retry; the second is a deleted
// user who is still signed in.
func (s *entStore) DeleteUser(ctx context.Context, id int) error {
	user, err := s.GetUser(ctx, id)
	if err != nil {
		return err
	}

	if _, err := s.client.Session.Delete().
		Where(entsession.SubjectEQ(user.Email)).
		Exec(ctx); err != nil {
		return fmt.Errorf("access: failed to revoke sessions for user %d: %w", id, err)
	}

	err = s.client.User.DeleteOneID(id).Exec(ctx)
	return mapWriteError(err, "user", id, "")
}

// CreateBinding persists a grant, after refusing one that could never
// resolve to what its author meant.
func (s *entStore) CreateBinding(ctx context.Context, binding Binding) (Binding, error) {
	if err := validateBinding(binding); err != nil {
		return Binding{}, err
	}

	builder := s.client.RoleBinding.Create().
		SetRole(string(binding.Role)).
		SetScopeType(string(binding.ScopeType)).
		SetEffect(string(binding.Effect)).
		SetTeamID(binding.TeamID)
	// Left unset rather than written as zero for a system binding, so the
	// column is NULL and the resolver's scopeIDMatches pairs it with a nil
	// target rather than with an accidental zero.
	if binding.ScopeType != auth.ScopeSystem {
		builder = builder.SetScopeID(binding.ScopeID)
	}

	created, err := builder.Save(ctx)
	if err != nil {
		return Binding{}, fmt.Errorf("access: creating role binding: %w", err)
	}
	return s.GetBinding(ctx, created.ID)
}

// GetBinding loads one grant.
func (s *entStore) GetBinding(ctx context.Context, id int) (Binding, error) {
	row, err := s.client.RoleBinding.Query().
		Where(entbinding.IDEQ(id)).
		WithTeam().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Binding{}, fmt.Errorf("%w: role binding %d", ErrNotFound, id)
		}
		return Binding{}, fmt.Errorf("access: loading role binding %d: %w", id, err)
	}
	binding := hydrateBinding(row)
	// A slice of one, so the same resolution serves both read paths and
	// there is no second place deciding what a scope target is called.
	resolved := []Binding{binding}
	if err := s.resolveScopeNames(ctx, resolved); err != nil {
		return Binding{}, err
	}
	return resolved[0], nil
}

// ListBindings returns a page of grants.
func (s *entStore) ListBindings(ctx context.Context, q BindingQuery) ([]Binding, error) {
	query := s.client.RoleBinding.Query().
		WithTeam().
		Order(ent.Asc(entbinding.FieldID)).
		Limit(boundLimit(q.Limit))
	if len(q.TeamIDs) > 0 {
		query = query.Where(entbinding.HasTeamWith(entteam.IDIn(q.TeamIDs...)))
	}
	if len(q.ScopeTypes) > 0 {
		types := make([]string, 0, len(q.ScopeTypes))
		for _, t := range q.ScopeTypes {
			types = append(types, string(t))
		}
		query = query.Where(entbinding.ScopeTypeIn(types...))

		// Only with exactly one level named. With none, an id would match
		// across levels that share the integer; with several, the caller has
		// not said which level's record they mean, and guessing would answer
		// a question nobody asked.
		if q.ScopeID > 0 && len(q.ScopeTypes) == 1 {
			query = query.Where(entbinding.ScopeIDEQ(q.ScopeID))
		}
	}
	if q.After > 0 {
		query = query.Where(entbinding.IDGT(q.After))
	}

	rows, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("access: listing role bindings: %w", err)
	}
	out := make([]Binding, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrateBinding(row))
	}
	// One query per scope type present, never one per row.
	if err := s.resolveScopeNames(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateBinding changes a grant's role or effect.
//
// The scope is not updatable, and that is a deliberate narrowing rather than
// an omission. Re-pointing a binding at a different target is indistinguishable
// from revoking one grant and issuing another, and an audit trail that shows
// a grant quietly changing what it covers is one nobody can reconstruct. The
// UI offers delete and create instead, which leaves two legible events.
func (s *entStore) UpdateBinding(ctx context.Context, binding Binding) error {
	current, err := s.GetBinding(ctx, binding.ID)
	if err != nil {
		return err
	}

	updated := current
	updated.Role, updated.Effect = binding.Role, binding.Effect
	if err := validateBinding(updated); err != nil {
		return err
	}
	if err := s.guardLastSystemGrant(ctx, current, updated); err != nil {
		return err
	}

	err = s.client.RoleBinding.UpdateOneID(binding.ID).
		SetRole(string(updated.Role)).
		SetEffect(string(updated.Effect)).
		Exec(ctx)
	return mapWriteError(err, "role binding", binding.ID, "")
}

// DeleteBinding revokes a grant, refusing to remove the last system-scope
// Allow in the deployment.
func (s *entStore) DeleteBinding(ctx context.Context, id int) error {
	current, err := s.GetBinding(ctx, id)
	if err != nil {
		return err
	}
	if err := s.guardLastSystemGrant(ctx, current, Binding{}); err != nil {
		return err
	}

	err = s.client.RoleBinding.DeleteOneID(id).Exec(ctx)
	return mapWriteError(err, "role binding", id, "")
}

// guardLastSystemGrant refuses a write that would leave the deployment with
// no system-scope Allow at all.
//
// It covers both ways to reach that state, which is why it takes the value
// after the write rather than only the one before: deleting the last such
// binding, and editing it into a Deny or into a narrower role, produce the
// same outcome and only one of them looks like a deletion.
func (s *entStore) guardLastSystemGrant(ctx context.Context, before, after Binding) error {
	stillGrants := after.ScopeType == auth.ScopeSystem && after.Effect == auth.EffectAllow
	wasGranting := before.ScopeType == auth.ScopeSystem && before.Effect == auth.EffectAllow
	if !wasGranting || stillGrants {
		return nil
	}

	remaining, err := s.client.RoleBinding.Query().
		Where(
			entbinding.ScopeTypeEQ(string(auth.ScopeSystem)),
			entbinding.EffectEQ(string(auth.EffectAllow)),
			entbinding.IDNEQ(before.ID),
		).
		Count(ctx)
	if err != nil {
		return fmt.Errorf("access: counting system-scope grants: %w", err)
	}
	if remaining == 0 {
		return ErrLastSystemBinding
	}
	return nil
}

// ErrInUse is returned when a record cannot be removed because something
// still points at it.
//
// Distinct from ErrExists, which it used to be reported as. Both arrive as
// ent constraint errors, and collapsing them told an operator deleting a
// team that "a record with that name already exists", which is not merely
// unhelpful: it describes the opposite operation. A caller needs to know
// something still references this, not that something shares its name.
var ErrInUse = errors.New("access: record is still referenced")

// mapWriteError turns an ent write failure into this package's vocabulary.
//
// The two constraint kinds are separated by inspecting the driver's message,
// which is unlovely and is the only option ent offers: ConstraintError does
// not distinguish a uniqueness violation from a foreign-key one, and the two
// mean opposite things to a caller. Anything unrecognised falls through to a
// uniqueness reading, because that is what a constraint violation on a
// create almost always is here and it is the safer of the two to be wrong
// about: it reports a conflict rather than inventing a dependency.
func mapWriteError(err error, kind string, id int, name string) error {
	switch {
	case err == nil:
		return nil
	case ent.IsNotFound(err):
		return fmt.Errorf("%w: %s %d", ErrNotFound, kind, id)
	case ent.IsConstraintError(err) && isForeignKeyViolation(err):
		return fmt.Errorf("%w: %s %d", ErrInUse, kind, id)
	case ent.IsConstraintError(err):
		return fmt.Errorf("%w: %q", ErrExists, name)
	default:
		return fmt.Errorf("access: writing %s %d: %w", kind, id, err)
	}
}

// foreignKeyMarkers are what the two supported drivers say when a row is
// still referenced. SQLite and PostgreSQL word it differently, so both are
// listed rather than one being assumed.
var foreignKeyMarkers = []string{"FOREIGN KEY", "foreign key", "violates foreign key constraint"}

// isForeignKeyViolation reports whether a constraint error is about a
// reference rather than about uniqueness.
func isForeignKeyViolation(err error) bool {
	text := err.Error()
	for _, marker := range foreignKeyMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func hydrateOrganization(row *ent.Organization) Organization {
	return Organization{
		ID:             row.ID,
		Name:           row.Name,
		Description:    row.Description,
		Classification: Classification(row.Classification),
		ChangeWindow:   row.ChangeWindow,
		Frozen:         row.Frozen,
		FreezeReason:   row.FreezeReason,
		CostCentre:     row.CostCentre,
		TicketKey:      row.TicketKey,
		CMDBID:         row.CmdbID,
		Attested:       Attestation{By: row.AttestedBy, At: row.AttestedAt},
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
}

func hydrateTeam(row *ent.Team) Team {
	team := Team{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		Attested:    Attestation{By: row.AttestedBy, At: row.AttestedAt},
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
	if row.Edges.Organization != nil {
		team.OrganizationID = row.Edges.Organization.ID
		team.OrganizationName = row.Edges.Organization.Name
	}
	for _, u := range row.Edges.Users {
		team.UserIDs = append(team.UserIDs, u.ID)
	}
	return team
}

func hydrateUser(row *ent.User) User {
	user := User{
		ID:        row.ID,
		Email:     row.Email,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	for _, t := range row.Edges.Teams {
		user.TeamIDs = append(user.TeamIDs, t.ID)
		user.TeamNames = append(user.TeamNames, t.Name)
	}
	return user
}

func hydrateBinding(row *ent.RoleBinding) Binding {
	binding := Binding{
		ID:        row.ID,
		Role:      auth.Role(row.Role),
		ScopeType: auth.ScopeType(row.ScopeType),
		Effect:    auth.Effect(row.Effect),
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	if row.ScopeID != nil {
		binding.ScopeID = *row.ScopeID
	}
	if row.Edges.Team != nil {
		binding.TeamID = row.Edges.Team.ID
		binding.TeamName = row.Edges.Team.Name
	}
	return binding
}
