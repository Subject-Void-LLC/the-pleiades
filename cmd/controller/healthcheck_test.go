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
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// newReadinessRouter builds the real Front Controller with one readiness
// check whose outcome the caller chooses.
//
// No Routes are registered, so no Admission and no HATEOAS generator are
// required: this server exists to serve the operational endpoints, which
// api.NewRouter mounts outside the versioned API subtree.
// AllowUnauthenticated is set because Auth is nil here, which matches
// production for /readyz specifically: the probe is deliberately
// unauthenticated so an orchestrator with no credentials can run it.
func newReadinessRouter(t *testing.T, probe func(context.Context) error) http.Handler {
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
	return router
}

// newReadinessServer serves that router over plain HTTP and returns the
// host:port it listens on, in the same shape LISTEN_ADDR carries.
//
// It also puts this process into the one configuration where probing a
// plain-HTTP listener is a legitimate thing to do, because the probe reads
// the same resolver the server does and refuses to guess a scheme. Set here
// rather than in each caller so no test can accidentally assert the
// healthcheck's behavior from a configuration the controller would have
// refused to start in.
func newReadinessServer(t *testing.T, probe func(context.Context) error) string {
	t.Helper()
	t.Setenv("PLEIADES_TLS_TERMINATED_UPSTREAM", "1")

	srv := httptest.NewServer(newReadinessRouter(t, probe))
	t.Cleanup(srv.Close)

	// The listener's own address, not srv.URL: LISTEN_ADDR is a bind
	// address with no scheme, and readyzURL is the code under test for
	// turning one into a URL.
	return srv.Listener.Addr().String()
}

// newTLSReadinessServer serves the same router over real TLS, presenting the
// same generated certificate a development stack and the end-to-end harness
// run on, and configures this process the way a container that terminates
// TLS itself is configured.
//
// A hand-rolled listener rather than httptest.NewTLSServer, for the reason
// that matters to what is under test: httptest mints its own certificate,
// and the probe's whole job is to verify the listener against the file
// TLS_CERT_FILE names. Against httptest's certificate the probe would
// correctly fail, and the test would prove nothing about the real path.
func newTLSReadinessServer(t *testing.T, probe func(context.Context) error) string {
	t.Helper()

	cert := testsupport.NewServingCertFor(t, t.TempDir())
	t.Setenv("TLS_CERT_FILE", cert.CertFile)
	t.Setenv("TLS_KEY_FILE", cert.KeyFile)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on loopback: %v", err)
	}
	srv := &http.Server{
		Handler:           newReadinessRouter(t, probe),
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}
	go func() {
		// ErrServerClosed is the normal end of this goroutine.
		_ = srv.ServeTLS(listener, cert.CertFile, cert.KeyFile)
	}()
	t.Cleanup(func() { _ = srv.Close() })

	return listener.Addr().String()
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

// TestRunHealthcheck_ProbesOverTLS is the case docker-compose.yml now runs
// in, and the one a plain-HTTP probe would fail with a handshake error while
// the controller served perfectly.
//
// It also proves the trust decision, not just the scheme: the probe verifies
// the listener against TLS_CERT_FILE, so this exits 0 only because the
// server really presented that certificate and proved it holds the key.
func TestRunHealthcheck_ProbesOverTLS(t *testing.T) {
	t.Setenv("LISTEN_ADDR", newTLSReadinessServer(t, func(context.Context) error { return nil }))

	if code := runHealthcheck([]string{healthcheckCommand}); code != 0 {
		t.Errorf("runHealthcheck against a ready HTTPS controller = %d, want 0", code)
	}
}

// TestRunHealthcheck_ReportsUnhealthyOverTLSToo proves the dependency check
// still decides the answer once TLS is in the way, rather than the handshake
// succeeding and the body going unread.
func TestRunHealthcheck_ReportsUnhealthyOverTLSToo(t *testing.T) {
	t.Setenv("LISTEN_ADDR", newTLSReadinessServer(t, func(context.Context) error {
		return errors.New("state store query failed")
	}))

	if code := runHealthcheck([]string{healthcheckCommand}); code != 1 {
		t.Errorf("runHealthcheck against an HTTPS controller reporting 503 = %d, want 1", code)
	}
}

// newSelfProvisionedReadinessServer serves the same router over the
// certificate the controller writes for ITSELF when an operator configured
// none, from a scratch directory, and configures this process exactly the
// way that container is configured.
//
// This is the case docker-compose.yml now runs in, so it is the case the
// probe has to get right. Everything here goes through the shipped code
// path: resolveTLS decides, prepareServingCertificate provisions, and the
// listener serves the pair that call returned, in memory, exactly the way
// main() does. Handing the server a certificate made some other way, or
// re-reading the files by path, would prove the probe works against a
// listener nobody runs.
//
// It returns the resolved settings as well as the address, because the
// rotation test needs to reach the same directory the server was started
// from.
func newSelfProvisionedReadinessServer(t *testing.T, probe func(context.Context) error) (string, tlsSettings) {
	t.Helper()

	clearTLSEnvironment(t)
	t.Setenv("PLEIADES_TLS_AUTOCERT_DIR", filepath.Join(t.TempDir(), "tls"))

	cfg, err := resolveTLS()
	if err != nil {
		t.Fatalf("resolveTLS(): %v", err)
	}
	if cfg.Mode != tlsModeSelfProvisioned {
		t.Fatalf("resolveTLS() mode = %v with nothing configured, want tlsModeSelfProvisioned", cfg.Mode)
	}
	pair, err := prepareServingCertificate(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("prepareServingCertificate: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on loopback: %v", err)
	}
	srv := &http.Server{
		Handler:           newReadinessRouter(t, probe),
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{*pair},
		},
	}
	go func() {
		// ErrServerClosed is the normal end of this goroutine. The two empty
		// paths are what main() passes: the certificate is already in the
		// config above.
		_ = srv.ServeTLS(listener, "", "")
	}()
	t.Cleanup(func() { _ = srv.Close() })

	return listener.Addr().String(), cfg
}

// TestRunHealthcheck_ProbesASelfProvisionedListener is the compose stack's
// case end to end: nothing configured, the controller wrote its own
// certificate, and the probe has to speak https to it and verify it against
// that exact file.
//
// It would fail if the probe defaulted to plain HTTP for an unconfigured
// container, which is the regression that would mark every self-provisioned
// controller permanently unhealthy while it served traffic perfectly.
func TestRunHealthcheck_ProbesASelfProvisionedListener(t *testing.T) {
	addr, _ := newSelfProvisionedReadinessServer(t, func(context.Context) error { return nil })
	t.Setenv("LISTEN_ADDR", addr)

	if code := runHealthcheck([]string{healthcheckCommand}); code != 0 {
		t.Errorf("runHealthcheck against a ready self-provisioned controller = %d, want 0", code)
	}
}

// TestRunHealthcheck_ReportsUnhealthyOnASelfProvisionedListenerToo proves
// the dependency check still decides the answer, rather than the handshake
// succeeding and the body going unread.
func TestRunHealthcheck_ReportsUnhealthyOnASelfProvisionedListenerToo(t *testing.T) {
	addr, _ := newSelfProvisionedReadinessServer(t, func(context.Context) error {
		return errors.New("state store query failed")
	})
	t.Setenv("LISTEN_ADDR", addr)

	if code := runHealthcheck([]string{healthcheckCommand}); code != 1 {
		t.Errorf("runHealthcheck against a self-provisioned controller reporting 503 = %d, want 1", code)
	}
}

// TestRunHealthcheck_SurvivesACertificateRotationUnderTheRunningServer is
// the trust-model defect.
//
// A controller serves the pair it loaded at start-up for as long as it runs.
// The directory it loaded from can move on underneath it: a second
// controller sharing the volume renews, and cert.pem is then a certificate
// the first one is not presenting. A probe anchored on that file alone
// starts failing against a process that is serving perfectly, and the
// orchestrator kills a healthy controller for it. This test does exactly
// that rotation between starting the server and probing it.
//
// It is the whole reason internal/tlscert keeps the certificates it has
// replaced: the anchor has to be everything this deployment may legitimately
// be serving, not just the newest thing on disk.
func TestRunHealthcheck_SurvivesACertificateRotationUnderTheRunningServer(t *testing.T) {
	addr, cfg := newSelfProvisionedReadinessServer(t, func(context.Context) error { return nil })
	t.Setenv("LISTEN_ADDR", addr)

	// A real renewal, forced the way a real one happens: a required name the
	// stored certificate does not carry. The running server above keeps
	// serving the certificate it already loaded.
	renewed, err := tlscert.Ensure(cfg.AutocertDir, tlscert.Options{ExtraNames: []string{"controller.example.test"}})
	if err != nil {
		t.Fatalf("renewing the stored certificate: %v", err)
	}
	if !renewed.Generated {
		t.Fatal("the directory was not actually renewed, so this test proves nothing")
	}

	if code := runHealthcheck([]string{healthcheckCommand}); code != 0 {
		t.Errorf("runHealthcheck against a healthy controller whose certificate directory was rotated under it = %d, want 0", code)
	}
}

// TestRunHealthcheck_StillRejectsAStrangerOnThePort is the control for the
// test above.
//
// Widening the trust anchor from one file to a set is only acceptable if the
// set is still closed. A certificate this deployment never provisioned must
// be rejected, or the probe has quietly become InsecureSkipVerify with more
// steps.
func TestRunHealthcheck_StillRejectsAStrangerOnThePort(t *testing.T) {
	clearTLSEnvironment(t)
	t.Setenv("PLEIADES_TLS_AUTOCERT_DIR", filepath.Join(t.TempDir(), "tls"))

	cfg, err := resolveTLS()
	if err != nil {
		t.Fatalf("resolveTLS(): %v", err)
	}
	if _, err := prepareServingCertificate(cfg, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("prepareServingCertificate: %v", err)
	}

	// Something else entirely, holding the port this controller's probe will
	// dial and answering readiness perfectly.
	stranger := testsupport.NewServingCertFor(t, t.TempDir())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on loopback: %v", err)
	}
	srv := &http.Server{
		Handler:           newReadinessRouter(t, func(context.Context) error { return nil }),
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}
	go func() { _ = srv.ServeTLS(listener, stranger.CertFile, stranger.KeyFile) }()
	t.Cleanup(func() { _ = srv.Close() })

	t.Setenv("LISTEN_ADDR", listener.Addr().String())
	if code := runHealthcheck([]string{healthcheckCommand}); code == 0 {
		t.Error("the probe reported ready for a listener presenting a certificate this deployment never provisioned")
	}
}

// TestRunHealthcheck_TreatsAnUnwrittenCertificateAsNotReady covers the cold
// start, which is a window Docker really probes: it begins the moment the
// container starts, and the server writes this certificate before it opens
// its listener.
//
// Exit 1, not 2, and the difference is the whole test. Docker treats both as
// unhealthy, so this is for the human reading the logs: 2 says "your
// configuration is wrong" and would be printed on every single cold start of
// a correctly configured stack.
//
// This replaces a test that asserted 2 for a container with no TLS
// configuration at all. That expectation was correct while an unconfigured
// controller refused to start, and became wrong the moment one provisions
// its own certificate instead.
func TestRunHealthcheck_TreatsAnUnwrittenCertificateAsNotReady(t *testing.T) {
	clearTLSEnvironment(t)
	// An empty directory, so the certificate genuinely does not exist yet.
	t.Setenv("PLEIADES_TLS_AUTOCERT_DIR", filepath.Join(t.TempDir(), "tls"))
	t.Setenv("LISTEN_ADDR", "127.0.0.1:8080")

	if code := runHealthcheck([]string{healthcheckCommand}); code != 1 {
		t.Errorf("runHealthcheck before the self-provisioned certificate exists = %d, want 1 (not ready, not a caller error)", code)
	}
}

// TestRunHealthcheck_RefusesAnUnreadableCertificate covers the other caller
// error the TLS path adds: a certificate path that names nothing.
func TestRunHealthcheck_RefusesAnUnreadableCertificate(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "127.0.0.1:8080")
	t.Setenv("TLS_CERT_FILE", t.TempDir()+"/absent.pem")
	t.Setenv("TLS_KEY_FILE", t.TempDir()+"/absent-key.pem")

	if code := runHealthcheck([]string{healthcheckCommand}); code != 2 {
		t.Errorf("runHealthcheck with an unreadable TLS_CERT_FILE = %d, want 2", code)
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
	// A valid transport decision, so this test fails for the reason it
	// names (nothing is listening) rather than for a missing one.
	t.Setenv("PLEIADES_TLS_TERMINATED_UPSTREAM", "1")

	if code := runHealthcheck([]string{healthcheckCommand, "-timeout", "2s"}); code != 1 {
		t.Errorf("runHealthcheck against a closed port = %d, want 1", code)
	}
}

// TestRunHealthcheck_RejectsAnUnusableListenAddr proves the caller-error
// exit code is distinct from the unhealthy one. Docker treats both as
// unhealthy; the operator reading the log needs to know which happened.
func TestRunHealthcheck_RejectsAnUnusableListenAddr(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "not-a-host-port")
	// As above: the address is the thing under test here, so the transport
	// decision has to be a valid one.
	t.Setenv("PLEIADES_TLS_TERMINATED_UPSTREAM", "1")

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
		mode       tlsMode
		want       string
		wantErr    bool
	}{
		// The default, and the compose stack's shape: a wildcard bind that
		// no client can dial as written.
		{"the default wildcard bind", defaultListenAddr, tlsModeUpstream, "http://127.0.0.1:8080/readyz", false},
		{"an explicit IPv4 wildcard", "0.0.0.0:9090", tlsModeUpstream, "http://127.0.0.1:9090/readyz", false},
		{"an IPv6 wildcard", "[::]:9090", tlsModeUpstream, "http://127.0.0.1:9090/readyz", false},
		{"an explicit loopback bind", "127.0.0.1:8081", tlsModeUpstream, "http://127.0.0.1:8081/readyz", false},
		// An IPv6 literal has to go back inside brackets or the URL is not
		// parseable at all.
		{"an IPv6 literal bind", "[::1]:8080", tlsModeUpstream, "http://[::1]:8080/readyz", false},
		// The scheme follows the mode, and nothing else about the address
		// handling changes with it.
		{"terminating TLS here asks over https", defaultListenAddr, tlsModeServe, "https://127.0.0.1:8080/readyz", false},
		{"an IPv6 literal bind over https", "[::1]:8443", tlsModeServe, "https://[::1]:8443/readyz", false},
		// The unconfigured container, which terminates TLS from a
		// certificate it wrote for itself. Getting this one wrong would
		// leave every compose stack permanently unhealthy while it served
		// traffic perfectly.
		{"a self-provisioned certificate asks over https too", defaultListenAddr, tlsModeSelfProvisioned, "https://127.0.0.1:8080/readyz", false},
		{"no port at all", "8080", tlsModeUpstream, "", true},
		{"empty", "", tlsModeUpstream, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readyzURL(tt.listenAddr, tt.mode)
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
			// Both setup and admin match this, so this is also about order:
			// the admin route opens the database under a key setup has not
			// written yet.
			name: "setup wins over the admin guard that also matches it",
			args: []string{setupCommand, "--target", "compose"},
			want: routeSetup,
		},
		{
			// The admin guard matches these too, and would open the
			// database under a key before a restore could supply one.
			name: "backup, restore and decommission win over the admin guard",
			args: []string{restoreCommand, "--file", "x.dump"},
			want: routeBackup,
		},
		{
			name: "backup routes to the backup command",
			args: []string{backupCommand},
			want: routeBackup,
		},
		{
			name: "decommission routes to the backup command",
			args: []string{decommissionCommand, "--destroy-deployment"},
			want: routeBackup,
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
	if !isAdminCommand([]string{setupCommand}) {
		t.Error("isAdminCommand no longer matches \"setup\", so routeFor's guard-order reasoning is stale; re-read it before changing the order")
	}
	if !isAdminCommand([]string{healthcheckCommand}) {
		t.Error("isAdminCommand no longer matches \"healthcheck\", so routeFor's guard-order reasoning is stale; re-read it before changing the order")
	}
}
