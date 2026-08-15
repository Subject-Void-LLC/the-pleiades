// Tests for the healthcheck subcommand.
//
// These run against the REAL router, not a hand-written stub returning a
// hand-written body, per RULE 0. The claim worth proving is not that an
// HTTP client works; it is that this binary, run as `controller
// healthcheck` inside a container, agrees with what internal/api's own
// /readyz handler says about the same dependencies. A stub server would
// prove only that the test author and the test agree, and would keep
// passing on the day readyzHandler changed the status word or the code it
// answers with, which is exactly when a container healthcheck must not
// silently start reporting the opposite of the truth.
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
)

// newReadinessServer starts the real Front Controller with one readiness
// check whose outcome the caller chooses, and returns the host:port it
// listens on in the same shape LISTEN_ADDR carries.
//
// No Routes are registered, so no Admission and no HATEOAS generator are
// required: this server exists to serve the operational endpoints, which
// api.NewRouter mounts outside the versioned API subtree.
// AllowUnauthenticated is set because Auth is nil here, which matches
// production for /readyz specifically: the probe is deliberately
// unauthenticated so an orchestrator with no credentials can run it.
func newReadinessServer(t *testing.T, probe func(context.Context) error) string {
	t.Helper()

	router, err := api.NewRouter(api.RouterConfig{
		// Discarded rather than left to slog.Default(), so a readiness
		// check that is meant to fail does not print a scary warning in the
		// middle of a passing test run.
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Readiness: []api.ReadinessCheck{
			{Name: "database", Probe: probe},
		},
		AllowUnauthenticated: true,
	})
	if err != nil {
		t.Fatalf("api.NewRouter: %v", err)
	}

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	// The listener's own address, not srv.URL: LISTEN_ADDR is a bind
	// address with no scheme, and readyzURL is the code under test for
	// turning one into a URL.
	return srv.Listener.Addr().String()
}

// TestRunHealthcheck_ExitsZeroWhenReady is the case a container healthcheck
// spends almost all of its life in.
func TestRunHealthcheck_ExitsZeroWhenReady(t *testing.T) {
	t.Setenv("LISTEN_ADDR", newReadinessServer(t, func(context.Context) error { return nil }))

	if code := runHealthcheck([]string{healthcheckCommand}); code != 0 {
		t.Errorf("runHealthcheck against a ready controller = %d, want 0", code)
	}
}

// TestRunHealthcheck_ExitsNonZeroWhenNotReady is the case the whole
// subcommand exists for: the process is alive, so the container is running
// and nothing else would notice, but a dependency it cannot serve without
// is down.
func TestRunHealthcheck_ExitsNonZeroWhenNotReady(t *testing.T) {
	t.Setenv("LISTEN_ADDR", newReadinessServer(t, func(context.Context) error {
		return errors.New("state store query failed")
	}))

	if code := runHealthcheck([]string{healthcheckCommand}); code != 1 {
		t.Errorf("runHealthcheck against a controller reporting 503 = %d, want 1", code)
	}
}

// TestRunHealthcheck_ExitsNonZeroWhenNothingIsListening covers the startup
// window and the crashed-listener case. Docker runs this probe from the
// moment the container starts, which can be before the server has bound.
func TestRunHealthcheck_ExitsNonZeroWhenNothingIsListening(t *testing.T) {
	// A port that is genuinely free, obtained by binding one and releasing
	// it, rather than a number guessed to be unused. Guessing is how this
	// test would pass for the wrong reason on a machine where something
	// happens to answer.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a free port: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("releasing the reserved port: %v", err)
	}
	t.Setenv("LISTEN_ADDR", addr)

	if code := runHealthcheck([]string{healthcheckCommand, "-timeout", "2s"}); code != 1 {
		t.Errorf("runHealthcheck against a closed port = %d, want 1", code)
	}
}

// TestRunHealthcheck_RejectsAnUnusableListenAddr proves the caller-error
// exit code is distinct from the unhealthy one. Docker treats both as
// unhealthy; the operator reading the log needs to know which happened.
func TestRunHealthcheck_RejectsAnUnusableListenAddr(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "not-a-host-port")

	if code := runHealthcheck([]string{healthcheckCommand}); code != 2 {
		t.Errorf("runHealthcheck with a malformed LISTEN_ADDR = %d, want 2", code)
	}
}

// TestReadyzURL covers the address shapes LISTEN_ADDR actually arrives in,
// including the default this image runs with.
func TestReadyzURL(t *testing.T) {
	tests := []struct {
		name       string
		listenAddr string
		want       string
		wantErr    bool
	}{
		// The default, and the compose stack's shape: a wildcard bind that
		// no client can dial as written.
		{"the default wildcard bind", defaultListenAddr, "http://127.0.0.1:8080/readyz", false},
		{"an explicit IPv4 wildcard", "0.0.0.0:9090", "http://127.0.0.1:9090/readyz", false},
		{"an IPv6 wildcard", "[::]:9090", "http://127.0.0.1:9090/readyz", false},
		{"an explicit loopback bind", "127.0.0.1:8081", "http://127.0.0.1:8081/readyz", false},
		// An IPv6 literal has to go back inside brackets or the URL is not
		// parseable at all.
		{"an IPv6 literal bind", "[::1]:8080", "http://[::1]:8080/readyz", false},
		{"no port at all", "8080", "", true},
		{"empty", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readyzURL(tt.listenAddr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("readyzURL(%q) = %q, want an error", tt.listenAddr, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("readyzURL(%q): %v", tt.listenAddr, err)
			}
			if got != tt.want {
				t.Errorf("readyzURL(%q) = %q, want %q", tt.listenAddr, got, tt.want)
			}
		})
	}
}

// TestRouteFor pins the guard ORDER, which is the thing that can actually
// regress.
//
// An earlier version of this test asserted isHealthcheckCommand and
// isAdminCommand separately and claimed to pin the ordering. It did not.
// Reversing the two guards left this whole package green (ok, 4.2s) while
// the shipped binary answered `controller healthcheck` with exit 2,
// "unknown command". docker-compose.yml's healthcheck runs exactly that
// argument vector, so the regression would have marked every container
// permanently unhealthy with no test anywhere going red. The routing
// decision was extracted into routeFor specifically so this test can assert
// on it, and reversing the two checks inside routeFor now fails the first
// case below.
//
// The subtlety worth keeping: isAdminCommand answers true for "healthcheck"
// too, because it treats any non-flag first argument as a subcommand so an
// unknown one gets a usage message rather than silently starting a server.
// Both guards match, so only their order decides, which is exactly why
// asserting each one alone proved nothing.
func TestRouteFor(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want commandRoute
	}{
		{
			// The case the old test could not catch. Both guards match
			// these args, so this assertion is entirely about order.
			name: "healthcheck wins over the admin guard that also matches it",
			args: []string{healthcheckCommand},
			want: routeHealthcheck,
		},
		{
			name: "no arguments runs the server",
			args: nil,
			want: routeServer,
		},
		{
			name: "an empty slice runs the server",
			args: []string{},
			want: routeServer,
		},
		{
			name: "an admin subcommand reaches the admin path",
			args: []string{"bootstrap-admin", "--email", "a@example.com"},
			want: routeAdmin,
		},
		{
			name: "an unknown subcommand reaches the admin path for its usage message",
			args: []string{"not-a-command"},
			want: routeAdmin,
		},
		{
			// A leading flag is not a subcommand, so it belongs to the
			// server. This is what stops a future server flag from being
			// mistaken for an admin command.
			name: "a leading flag runs the server",
			args: []string{"-someflag"},
			want: routeServer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := routeFor(tt.args); got != tt.want {
				t.Errorf("routeFor(%q) = %v, want %v.\n"+
					"If this is the healthcheck case, the two guards in routeFor have been swapped: isAdminCommand matches \"healthcheck\" as well, so the more specific guard must run first or every container healthcheck exits 2 with \"unknown command\".",
					tt.args, got, tt.want)
			}
		})
	}

	// Guard the premise the ordering rests on. If isAdminCommand ever stops
	// matching "healthcheck", the order stops mattering and routeFor's
	// comment becomes misleading rather than wrong, which is harder to spot.
	if !isAdminCommand([]string{healthcheckCommand}) {
		t.Error("isAdminCommand no longer matches \"healthcheck\", so routeFor's guard-order reasoning is stale; re-read it before changing the order")
	}
}
