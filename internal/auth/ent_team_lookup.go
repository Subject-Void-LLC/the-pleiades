package auth

import (
	"context"
	"fmt"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/user"
)

// entTeamLookup adapts TeamLookup to internal/ent. subject is matched
// against User.email: Identity.Subject is the JWT "sub" claim, and this
// codebase has no other stable, already-established identifier to join a
// token's subject against a User row by.
type entTeamLookup struct {
	client *ent.Client
}

// NewEntTeamLookup builds a TeamLookup backed by client.
func NewEntTeamLookup(client *ent.Client) TeamLookup {
	return &entTeamLookup{client: client}
}

// TeamIDsForSubject implements TeamLookup. A subject matching no User (or
// an empty subject) returns an empty, non-error result: an identity with
// no known User has no Teams, not an error condition on its own.
func (l *entTeamLookup) TeamIDsForSubject(ctx context.Context, subject string) ([]int, error) {
	if subject == "" {
		return nil, nil
	}
	ids, err := l.client.User.Query().
		Where(user.EmailEQ(subject)).
		QueryTeams().
		IDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: looking up teams for subject %q: %w", subject, err)
	}
	return ids, nil
}
