// The sustained login-flood stress test.
//
// It runs against the real handler, the real chi router, the real
// api.RateLimiter and the real pre-auth CSRF layer. The only double is the
// credential verifier, and that is deliberate rather than a shortcut: the
// property under test is what the SERVER does under a flood, and paying
// thirty milliseconds of real Argon2id per request would mean this test
// measured the KDF's speed instead. internal/localauth's own benchmark
// already measures that, and the derivation gate that bounds it is tested
// in its own package.
//
// What this asserts, per the phase's own Fuzz/Stress item: the limiter
// SHEDS rather than the process growing without bound, and no goroutine
// leaks per rejected request.
package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
	"github.com/go-chi/chi/v5"
)

// countingPasswords records how many verifications actually ran, so the
// test can prove the limiter shed work rather than merely answering 429
// after doing it.
type countingPasswords struct{ calls atomic.Int64 }

func (c *countingPasswords) Authenticate(_ context.Context, _, _ string) (string, error) {
	c.calls.Add(1)
	return "", errRefused
}

// TestLoginFlood_ShedsRatherThanGrowing is the stress property.
//
// A login endpoint that answered every request would spend a real Argon2id
// derivation on each one, which at roughly 20 MB apiece is the memory
// exhaustion the whole three-mechanism design exists to prevent. The
// limiter is the first of those three and the only one that can refuse
// before any work happens.
func TestLoginFlood_ShedsRatherThanGrowing(t *testing.T) {
	defer goleak.VerifyNone(t,
		// chi and net/http/httptest leave nothing running, but the test
		// binary itself carries the testing package's own goroutines.
		goleak.IgnoreTopFunction("testing.tRunner"),
	)

	registerTestView()
	passwords := &countingPasswords{}
	store := newMemStore()
	cookie := session.CookieCodec{}

	const (
		burst    = 5
		requests = 400
		workers  = 16
	)

	h := New(Config{
		Prefix:     "/ui",
		Sessions:   store,
		Cookie:     cookie,
		Passwords:  passwords,
		Identities: fakeIdentities{identity: &auth.Identity{Role: auth.RoleOperator}},
		// A real limiter, deliberately tight, so the flood is shed rather
		// than merely slowed.
		LoginLimiter: api.NewRateLimiter(api.RateLimiterConfig{RequestsPerSecond: 1, Burst: burst}),
		HATEOAS:      permitEverything{},
		Admission:    allowAll{},
	})
	root := chi.NewRouter()
	root.Mount("/ui", h.Routes())

	var (
		shed     atomic.Int64
		answered atomic.Int64
		wg       sync.WaitGroup
	)

	before := runtime.NumGoroutine()

	work := make(chan int, requests)
	for i := range requests {
		work <- i
	}
	close(work)

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range work {
				r := httptest.NewRequest(http.MethodPost, "/ui/login",
					strings.NewReader("email=operator@example.test&password=guess"))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				// No CSRF pair: the flood is what an attacker sends, and
				// an attacker does not fetch the form first. This also
				// proves the limiter runs BEFORE the CSRF check.
				w := httptest.NewRecorder()
				root.ServeHTTP(w, r)

				if w.Code == http.StatusTooManyRequests {
					shed.Add(1)
				} else {
					answered.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	// 1. The limiter shed the overwhelming majority. Burst plus a small
	// allowance for refill during the run, not an exact number: the bucket
	// refills on a clock and pinning the count would make this flaky for a
	// reason that has nothing to do with the property.
	if shed.Load() < requests-burst-10 {
		t.Errorf("shed %d of %d requests; the limiter is not shedding a flood",
			shed.Load(), requests)
	}

	// 2. No verification ran at all.
	//
	// Stated precisely, because the obvious reading overclaims: this does
	// NOT prove the limiter ran before the store. Negative-controlled by
	// widening the limiter until it shed nothing, this assertion still
	// passed, because the pre-auth CSRF layer refuses an unpaired request
	// before the handler is reached either way. What it proves is that a
	// flood of the shape an attacker actually sends reaches no derivation
	// through EITHER guard. The ordering claim is
	// TestLoginRateLimit_RunsBeforeTheCSRFCheck's job, which distinguishes
	// them by status code.
	if passwords.calls.Load() != 0 {
		t.Errorf("%d verifications ran during a flood; work reached the credential store",
			passwords.calls.Load())
	}

	// 3. No unbounded goroutine growth. goleak above catches leaks that
	// survive the test; this catches a handler that spawns one per request
	// and lets them pile up during it.
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+workers+10 {
		t.Errorf("goroutines grew from %d to %d across %d requests", before, after, requests)
	}

	// 4. Nothing was authenticated. A flood that minted even one session
	// would mean the refusals were cosmetic.
	if store.count() != 0 {
		t.Errorf("%d sessions were minted during a flood of wrong passwords", store.count())
	}
}
