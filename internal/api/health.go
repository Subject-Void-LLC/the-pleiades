package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// readinessProbeTimeout bounds how long the whole /readyz handler waits on
// its dependency checks. A readiness probe that hangs is worse than one
// that fails: the orchestrator's own probe timeout fires instead, which
// looks identical to the process being wedged.
const readinessProbeTimeout = 3 * time.Second

// ReadinessCheck is one named dependency /readyz reports on. Probe returns
// nil when the dependency is usable and an error describing why not
// otherwise.
//
// It is a struct of a name and a function rather than an interface because
// every real check is a two-line closure over a connection the composition
// root already holds (a NATS handle, an ent client), and an interface
// would force each of those into its own named type for no gain.
type ReadinessCheck struct {
	// Name identifies the dependency in the response body and in logs,
	// for example "nats" or "database".
	Name string

	// Probe reports whether the dependency is currently usable. It must
	// respect ctx: it is called with a deadline.
	Probe func(ctx context.Context) error
}

// healthResponse is the body both probes return. Checks is omitted for
// liveness, which has no dependencies to report on.
type healthResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// healthzHandler is the liveness probe. It answers "this process is
// running and its request pipeline works" and deliberately checks no
// dependency at all: a liveness probe that fails when NATS is down tells
// the orchestrator to restart a process that a restart cannot fix, turning
// one broken dependency into a crash loop across every replica. That is
// what /readyz is for.
func healthzHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

// readyzHandler is the readiness probe. It runs every check concurrently
// under one deadline and returns 503 if any of them fails, which is what
// PATTERNS.md's Liveness & Readiness Probes entry requires: a replica that
// has lost NATS must stop receiving traffic without being restarted.
//
// The response body names each check and reports only "ok" or "failed",
// never the underlying error text. An unauthenticated endpoint that echoes
// a driver error leaks database file paths, hostnames, and library
// versions to anyone who can reach the port; the real error goes to the
// log line instead, where it is just as useful to an operator and not
// readable by a stranger.
func readyzHandler(logger *slog.Logger, checks []ReadinessCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessProbeTimeout)
		defer cancel()

		results := make([]error, len(checks))
		var wg sync.WaitGroup
		for i, check := range checks {
			wg.Add(1)
			go func(i int, check ReadinessCheck) {
				defer wg.Done()
				results[i] = check.Probe(ctx)
			}(i, check)
		}
		wg.Wait()

		body := healthResponse{Status: "ready", Checks: make(map[string]string, len(checks))}
		status := http.StatusOK
		for i, check := range checks {
			if results[i] != nil {
				body.Checks[check.Name] = "failed"
				body.Status = "not ready"
				status = http.StatusServiceUnavailable
				logger.LogAttrs(ctx, slog.LevelWarn, "readiness check failed",
					slog.String("check", check.Name),
					slog.String("error", results[i].Error()),
				)
				continue
			}
			body.Checks[check.Name] = "ok"
		}

		writeJSON(w, status, body)
	}
}
