// This file is the controller's HTTP handler for the time between binding its
// listener and having a router to serve: the time it spends migrating the
// database, among other things.
//
// The listener used to be bound only after everything else was wired, so a
// migration ran with nothing answering on the port. An orchestrator's
// startup probe then had to be sized as a "migration budget", and a migration
// longer than that budget was killed half way, rolled back, and started again
// on the next restart, for ever (LESSONS_LEARNED.md #48 names the rule this
// broke: startup work runs alongside whatever makes the probes pass, never
// before it). Now the port answers from the start: the process is alive, it
// is not ready, and it says so, for as long as the migration takes.
package main

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

// startupHandler answers until serve hands it the real router, and delegates
// to that router from then on.
type startupHandler struct {
	router atomic.Pointer[http.Handler]
}

// serve switches every later request to the real router.
func (h *startupHandler) serve(router http.Handler) {
	h.router.Store(&router)
}

// ServeHTTP answers a request: through the router once there is one, and
// otherwise as a process that is alive and not yet ready.
func (h *startupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if next := h.router.Load(); next != nil {
		(*next).ServeHTTP(w, r)
		return
	}
	switch r.URL.Path {
	case "/healthz":
		// Alive: the process is running and its request pipeline works,
		// which is all liveness ever asks (internal/api's healthzHandler).
		writeStartupJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	case "/readyz":
		// The same body shape internal/api's /readyz uses, naming a check
		// and never a reason, since this endpoint is unauthenticated.
		writeStartupJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "not ready",
			"checks": map[string]string{"startup": "failed"},
		})
	default:
		w.Header().Set("Retry-After", "5")
		writeStartupJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "starting"})
	}
}

// writeStartupJSON writes a small JSON body. A write error has nobody left
// to tell; the probe that sent the request will simply ask again.
func writeStartupJSON(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
