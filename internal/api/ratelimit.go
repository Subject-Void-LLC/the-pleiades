package api

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiterConfig describes one ingress token bucket per caller.
//
// PATTERNS.md's Rate Limiter entry names the threat this defends against:
// "a misconfigured CI pipeline retrying a failed playbooks.dispatch call in
// a tight loop is a more realistic threat than a malicious actor." It is a
// per-identity bucket, so one runaway client cannot spend everyone else's
// budget, and it protects the Controller's own ingress, which is a
// different job from the Circuit Breaker that protects downstream devices.
type RateLimiterConfig struct {
	// RequestsPerSecond is the sustained refill rate of each caller's
	// bucket.
	RequestsPerSecond float64

	// Burst is how many requests a caller may make back to back before the
	// sustained rate applies. Zero means one.
	Burst int

	// MaxCallers caps how many distinct callers are tracked at once. The
	// bucket map is keyed by caller, and a caller key is attacker
	// influenced (an unauthenticated request keys on its source address),
	// so an uncapped map is a memory exhaustion vector: the limiter meant
	// to protect the process would be the thing that kills it. Zero means
	// defaultMaxCallers.
	MaxCallers int

	// IdleTimeout is how long a caller's bucket is kept after its last
	// request. Zero means defaultIdleTimeout.
	IdleTimeout time.Duration
}

const (
	defaultMaxCallers  = 8192
	defaultIdleTimeout = 10 * time.Minute
)

// bucket is one caller's token bucket plus the last time it was used, so
// idle entries can be swept.
type bucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter is a per-caller token bucket with a bounded, self-evicting
// caller table. The zero value is not usable; construct it with
// NewRateLimiter.
type RateLimiter struct {
	limit       rate.Limit
	burst       int
	maxCallers  int
	idleTimeout time.Duration

	// now is time.Now in production and a fake in tests. Sweeping is
	// driven by request arrivals rather than a background ticker, so the
	// limiter owns no goroutine and needs no Close.
	now func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

// NewRateLimiter builds a limiter from cfg, filling in defaults for the
// zero values cfg leaves unset.
func NewRateLimiter(cfg RateLimiterConfig) *RateLimiter {
	burst := cfg.Burst
	if burst < 1 {
		burst = 1
	}
	maxCallers := cfg.MaxCallers
	if maxCallers < 1 {
		maxCallers = defaultMaxCallers
	}
	idleTimeout := cfg.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleTimeout
	}
	return &RateLimiter{
		limit:       rate.Limit(cfg.RequestsPerSecond),
		burst:       burst,
		maxCallers:  maxCallers,
		idleTimeout: idleTimeout,
		now:         time.Now,
		buckets:     make(map[string]*bucket),
	}
}

// Allow reports whether the caller identified by key may make a request
// now, consuming a token if so.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	b, ok := rl.buckets[key]
	if !ok {
		// Evict before inserting, never after, so the table can never
		// exceed its cap even for an instant.
		rl.evictLocked(now)
		b = &bucket{limiter: rate.NewLimiter(rl.limit, rl.burst)}
		rl.buckets[key] = b
	}
	b.lastSeen = now
	return b.limiter.AllowN(now, 1)
}

// evictLocked drops idle callers once the table is at its cap, and then,
// if that was not enough, drops the least recently seen caller until there
// is room for one more. The caller must hold rl.mu.
//
// Sweeping only at the cap, rather than on every request, keeps the common
// path a single map lookup: the O(n) scan happens only when the table is
// actually full.
func (rl *RateLimiter) evictLocked(now time.Time) {
	if len(rl.buckets) < rl.maxCallers {
		return
	}
	for key, b := range rl.buckets {
		if now.Sub(b.lastSeen) >= rl.idleTimeout {
			delete(rl.buckets, key)
		}
	}
	for len(rl.buckets) >= rl.maxCallers {
		oldestKey := ""
		var oldestSeen time.Time
		for key, b := range rl.buckets {
			if oldestKey == "" || b.lastSeen.Before(oldestSeen) {
				oldestKey, oldestSeen = key, b.lastSeen
			}
		}
		delete(rl.buckets, oldestKey)
	}
}

// RateLimitMiddleware rejects a request with 429 when its caller has spent
// its budget.
//
// The caller key is the authenticated identity's subject when
// AuthMiddleware has already run, and the request's source address
// otherwise. The source address comes from RemoteAddr and never from
// X-Forwarded-For or X-Real-IP: those are caller-supplied strings, so
// keying on them would let one client mint a fresh, full bucket for every
// request simply by varying a header, which is worse than having no
// limiter at all because it looks like protection.
func RateLimitMiddleware(rl *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rl.Allow(callerKey(r)) {
				next.ServeHTTP(w, r)
				return
			}
			// Retry-After is advertised in seconds, rounded up from the
			// bucket's own refill interval, so a well-behaved client backs
			// off by roughly the right amount instead of guessing.
			w.Header().Set("Retry-After", strconv.Itoa(rl.retryAfterSeconds()))
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		})
	}
}

// retryAfterSeconds is how long a rejected caller should wait for its
// bucket to refill by one token, rounded up to at least one second.
func (rl *RateLimiter) retryAfterSeconds() int {
	if rl.limit <= 0 {
		return 1
	}
	seconds := int(1/float64(rl.limit) + 0.999)
	if seconds < 1 {
		return 1
	}
	return seconds
}

// callerKey identifies who a request should be billed to.
func callerKey(r *http.Request) string {
	if id, ok := IdentityFromContext(r.Context()); ok && id.Subject != "" {
		return "sub:" + id.Subject
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr without a port (a Unix socket, or a synthesized
		// request in a test) is used whole rather than discarded, so the
		// limiter never silently collapses every such caller onto one
		// shared bucket.
		return "addr:" + r.RemoteAddr
	}
	return "addr:" + host
}
