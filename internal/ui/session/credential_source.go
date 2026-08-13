package session

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// CookieSource resolves the session cookie into an identity, satisfying
// api.CredentialSource.
//
// Mounting it on the versioned API subtree alongside the Bearer source is
// the entire fix for the SSE defect the previous UI could not work around:
// an EventSource cannot set an Authorization header, but it does send
// cookies, so the job-log stream becomes reachable from a browser without
// weakening how any other client authenticates.
type CookieSource struct {
	Store  Store
	Cookie CookieCodec

	// IdleTimeout is how far a successful resolution slides the session's
	// idle deadline forward. Zero uses DefaultIdleTimeout.
	IdleTimeout time.Duration

	// Now is injectable for tests. Nil uses time.Now.
	Now func() time.Time
}

// Name implements api.CredentialSource.
func (CookieSource) Name() string { return "cookie" }

// Resolve implements api.CredentialSource.
//
// A request with no session cookie returns (nil, nil): it carries no
// credential of this kind, and another source may still authenticate it.
// A cookie naming a session that does not exist, or has expired, is an
// error rather than a fall-through -- a browser presenting a stale cookie
// should be told to log in again, not silently treated as anonymous and
// then refused for a reason that does not name the real problem.
func (s CookieSource) Resolve(r *http.Request) (*auth.Identity, error) {
	token := s.Cookie.Read(r)
	if token == "" {
		return nil, nil
	}

	sess, err := s.Store.Resolve(r.Context(), token)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	// Slide the idle deadline forward. The failure is deliberately
	// ignored: the caller has already proven a live session, and refusing
	// the request because a bookkeeping write failed would turn a
	// transient database hiccup into a logout.
	idle := s.IdleTimeout
	if idle == 0 {
		idle = DefaultIdleTimeout
	}
	_ = s.Store.Touch(r.Context(), token, idle)

	return sess.Identity(), nil
}

// TokenFromRequest returns the raw session token a request carries. The UI
// needs it to derive this request's CSRF token, which is an HMAC over the
// token itself; nothing else should reach for it.
func (s CookieSource) TokenFromRequest(r *http.Request) string {
	return s.Cookie.Read(r)
}

// SweepExpired deletes expired sessions until ctx is cancelled, waking on
// every interval tick.
//
// isLeader gates the work so exactly one replica sweeps, using the same
// election every other cluster-singleton in this platform runs behind. A
// sweep on every replica would be correct but wasteful, and would have
// fifty processes issuing the same DELETE against one table on a timer.
func SweepExpired(ctx context.Context, store Store, interval time.Duration, isLeader func() bool, onSwept func(int)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if isLeader != nil && !isLeader() {
				continue
			}
			n, err := store.DeleteExpired(ctx, time.Now())
			if err != nil {
				// A failed sweep is not fatal: expiry is enforced on read
				// regardless, so an unswept row is unusable, merely
				// untidy. The next tick tries again.
				continue
			}
			if n > 0 && onSwept != nil {
				onSwept(n)
			}
		}
	}
}
