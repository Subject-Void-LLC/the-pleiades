package session

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entsession "github.com/Subject-Void-LLC/the-pleiades/internal/ent/session"
)

// entStore is the Store adapter over the shared database every controller
// replica already holds a client for.
type entStore struct {
	client *ent.Client
	now    func() time.Time
}

// NewEntStore builds a Store over client.
//
// It takes the same *ent.Client every other repository in a controller
// process shares rather than opening its own: a second pool against the
// same database would double the connection count for no isolation
// benefit, and sessions have no reason to survive a transaction the rest
// of the process rolled back.
func NewEntStore(client *ent.Client) Store {
	return &entStore{client: client, now: time.Now}
}

func (s *entStore) Create(ctx context.Context, id *auth.Identity, idle, absolute time.Duration) (string, error) {
	if id == nil {
		return "", fmt.Errorf("refusing to create a session with no identity")
	}

	token, err := NewToken()
	if err != nil {
		return "", err
	}
	csrfKey, err := NewCSRFKey()
	if err != nil {
		return "", err
	}

	scopes := make([]string, 0, len(id.Scopes))
	for _, sc := range id.Scopes {
		scopes = append(scopes, string(sc))
	}

	now := s.now()
	_, err = s.client.Session.Create().
		SetTokenHash(HashToken(token)).
		SetSubject(id.Subject).
		SetRole(string(id.Role)).
		SetScopes(scopes).
		SetCsrfKey(csrfKey).
		SetIdleExpiresAt(now.Add(idle)).
		SetAbsoluteExpiresAt(now.Add(absolute)).
		SetLastSeenAt(now).
		Save(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to persist session: %w", err)
	}

	// The token is returned exactly once, here. Nothing else in this
	// process can reproduce it: only its hash was stored.
	return token, nil
}

func (s *entStore) Resolve(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrNotFound
	}

	row, err := s.client.Session.Query().
		Where(entsession.TokenHashEQ(HashToken(token))).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("failed to resolve session: %w", err)
	}

	// Expiry is enforced on read rather than trusted to the sweeper. The
	// sweeper is a periodic tidy-up that one replica runs; correctness
	// cannot depend on how recently it last ran, or a session would stay
	// usable for however long the sweep interval happens to be.
	now := s.now()
	if !now.Before(row.AbsoluteExpiresAt) || !now.Before(row.IdleExpiresAt) {
		return Session{}, ErrNotFound
	}

	return Session{
		Subject:           row.Subject,
		Role:              auth.Role(row.Role),
		Scopes:            row.Scopes,
		CSRFKey:           row.CsrfKey,
		IdleExpiresAt:     row.IdleExpiresAt,
		AbsoluteExpiresAt: row.AbsoluteExpiresAt,
		LastSeenAt:        row.LastSeenAt,
	}, nil
}

func (s *entStore) Touch(ctx context.Context, token string, idle time.Duration) error {
	now := s.now()

	// The absolute deadline is deliberately absent from this update. A
	// Touch that extended it would turn the absolute bound into a second
	// idle timeout, and a stolen cookie exercised regularly would never
	// expire at all -- which is the exact failure two deadlines exist to
	// prevent.
	n, err := s.client.Session.Update().
		Where(entsession.TokenHashEQ(HashToken(token))).
		SetIdleExpiresAt(now.Add(idle)).
		SetLastSeenAt(now).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to touch session: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *entStore) Delete(ctx context.Context, token string) error {
	if _, err := s.client.Session.Delete().
		Where(entsession.TokenHashEQ(HashToken(token))).
		Exec(ctx); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	// A logout for a session that no longer exists is a success, not an
	// error: the caller asked for it to be gone and it is gone.
	return nil
}

// DeleteForSubject revokes every session for one subject, optionally
// sparing one token.
//
// The spare is expressed as a NOT on the token hash rather than by reading
// the rows and deleting all but one: it is a single statement, so a session
// created concurrently by the same subject is either included or not by the
// database's own ordering rather than by a window between a read and a
// write. That matters here more than it usually would, because the caller
// is often reacting to a suspected compromise, and a race that leaves one
// session alive is the one outcome the operation exists to prevent.
func (s *entStore) DeleteForSubject(ctx context.Context, subject, keepToken string) (int, error) {
	if subject == "" {
		// Refused rather than treated as "match every row with an empty
		// subject". The schema requires subject to be non-empty, so an
		// empty argument is always a caller bug, and the failure mode of
		// guessing is deleting nothing while reporting success.
		return 0, fmt.Errorf("refusing to delete sessions for an empty subject")
	}

	query := s.client.Session.Delete().Where(entsession.SubjectEQ(subject))
	if keepToken != "" {
		query = query.Where(entsession.TokenHashNEQ(HashToken(keepToken)))
	}

	n, err := query.Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to delete sessions for subject: %w", err)
	}
	return n, nil
}

func (s *entStore) DeleteExpired(ctx context.Context, now time.Time) (int, error) {
	// Only the absolute deadline is swept. An idle-expired session is
	// already unusable (Resolve refuses it) and may still be within its
	// absolute window, so deleting it here would discard a row an
	// operator investigating recent activity might still want to see.
	n, err := s.client.Session.Delete().
		Where(entsession.AbsoluteExpiresAtLT(now)).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to delete expired sessions: %w", err)
	}
	return n, nil
}
