// The controller's healthcheck subcommand: this binary asking its own
// /readyz endpoint whether the process is ready to serve, and turning the
// answer into a process exit code.
//
// # Why the binary has to probe itself
//
// A Docker HEALTHCHECK runs INSIDE the container it checks, so it can only
// execute a program that image already contains. The runtime image is
// gcr.io/distroless/base-debian12:nonroot, whose /bin, /sbin, /usr/bin and
// /usr/sbin are empty directories: no shell, no curl, no wget, no busybox.
// The only executable in the whole image is the controller binary itself.
// So either this binary can speak HTTP to itself or the container has no
// healthcheck at all, and a healthcheck that cannot run is the same thing
// as no healthcheck. docker-compose.yml's nats service records what that
// costs in practice: a wget probe on a scratch image failed forever with
// "executable file not found", the service was marked unhealthy for good,
// and every service gated on it deadlocked at first start.
//
// # Why /readyz and not /healthz
//
// /healthz answers "this process is running and its request pipeline
// works", which a container healthcheck already knows: the container is
// running or Docker would have reported it exited. /readyz runs the real
// dependency probes (a query against the state store, the live NATS
// connection) and returns 503 when one is down, which is the fact an
// operator cannot get for free from `docker compose ps`.
//
// # Why this is not one of the admin subcommands
//
// runAdmin (admin.go) opens the database before it dispatches, because all
// three administrative commands need it. This one must not. It runs every
// few seconds for the life of the container, so it has to stay fast and
// hold nothing; and opening a second connection to the database to ask a
// running process whether IT can reach the database would be measuring the
// wrong process.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// healthcheckCommand is the single argument that selects the self-probe.
const healthcheckCommand = "healthcheck"

// readyStatus is the value /readyz puts in its JSON body when every
// dependency probe passed (internal/api's readyzHandler).
//
// It is checked in addition to the 200 status code, not instead of it, so
// that a 200 from something else on that port (a proxy, a stray container
// bound to the same address) cannot be read as this controller reporting
// itself ready.
const readyStatus = "ready"

// defaultHealthcheckTimeout bounds the whole probe: name resolution,
// connect, request and response.
//
// It is deliberately longer than internal/api's own readinessProbeTimeout
// of 3 seconds. The handler is allowed to spend that long deciding, and a
// client that gave up first would turn an honest "not ready, the database
// check failed" answer into a timeout, which tells an operator nothing
// about which dependency is down.
const defaultHealthcheckTimeout = 5 * time.Second

// maxHealthcheckBody caps how much of the response is read. The readiness
// document is a few hundred bytes; the cap is what stops a wrong or
// hostile listener on that port from making this probe read forever.
const maxHealthcheckBody = 4096

// isHealthcheckCommand reports whether args select the self-probe.
//
// routeFor checks this BEFORE isAdminCommand, and the order is load bearing.
// isAdminCommand answers true for any first argument that is not a flag, so
// an admin dispatch placed first would swallow "healthcheck" and exit 2
// with "unknown command healthcheck".
func isHealthcheckCommand(args []string) bool {
	return len(args) > 0 && args[0] == healthcheckCommand
}

// commandRoute names which of the three things this binary is being asked
// to be, resolved from its arguments and nothing else.
type commandRoute int

const (
	// routeServer is the argument-free case: run the control plane. This is
	// the only route that returns to main() rather than exiting.
	routeServer commandRoute = iota

	// routeHealthcheck is the container probe (healthcheck.go).
	routeHealthcheck

	// routeAdmin is the operator subcommands (admin.go).
	routeAdmin

	// routeSetup is the setup command (setup.go). It is not an admin
	// command because every admin command opens the database under a key
	// the environment already holds, and setup is what makes that key.
	routeSetup

	// routeBackup is backup, restore and decommission (backup.go). None of
	// them opens the database under a key the environment holds: they read
	// the key from .env, and a restore onto a clean machine has none yet.
	routeBackup
)

// routeFor resolves an argument vector to exactly one route.
//
// This function exists so that the ORDER of the two guards is a value a
// test can assert on, rather than a property of statements inside main().
// That distinction is not academic: it was found by reversing the guards
// and watching the whole package stay green while the shipped binary
// answered `controller healthcheck` with exit 2, "unknown command". The
// compose healthcheck runs exactly that argument vector, so the regression
// would have left every container permanently unhealthy while every test
// passed.
//
// The order is load bearing for the reason isHealthcheckCommand's own
// comment gives: isAdminCommand treats any non-flag first argument as a
// subcommand, so it answers true for "healthcheck" too. The more specific
// guard has to run first. TestRouteFor pins that, and it fails if these two
// checks are swapped.
func routeFor(args []string) commandRoute {
	if isHealthcheckCommand(args) {
		return routeHealthcheck
	}
	// Before the admin guard for the same reason the healthcheck is:
	// isAdminCommand matches "setup" too, and the admin route would open
	// the database under MASTER_ENCRYPTION_KEY before setup ran, which on a
	// first install is a key that does not exist yet.
	if isSetupCommand(args) {
		return routeSetup
	}
	// Before the admin guard for the same reason setup is.
	if isBackupCommand(args) {
		return routeBackup
	}
	if isAdminCommand(args) {
		return routeAdmin
	}
	return routeServer
}

// runHealthcheck probes /readyz and returns a process exit code: 0 only
// when the controller reports itself ready, non-zero otherwise.
//
// args includes the subcommand name at index 0, matching runAdmin, so the
// two guards in main() can be read side by side.
//
// The exit codes are split the way the admin path splits them: 2 for a
// caller error (a flag this command does not have, an unusable LISTEN_ADDR)
// and 1 for a controller that is not ready. Docker treats every non-zero
// code as unhealthy, so the distinction is for the human reading the logs,
// not for the orchestrator.
func runHealthcheck(args []string) int {
	// Named flags rather than fs, which is what it used to be called: this
	// function now compares against fs.ErrNotExist, and a local named fs
	// shadows the io/fs package so quietly that the compiler's complaint
	// points at flag.FlagSet instead.
	flags := flag.NewFlagSet(healthcheckCommand, flag.ContinueOnError)
	timeout := flags.Duration("timeout", defaultHealthcheckTimeout,
		"how long to wait for the whole probe before reporting failure")
	if err := flags.Parse(args[1:]); err != nil {
		// flag has already written the reason and the usage to stderr.
		return 2
	}

	// The same resolver main() uses, not a second reading of the same
	// variables: the probe and the server cannot end up disagreeing about
	// whether this container speaks HTTP or HTTPS, because there is one
	// place that decides. It inherits the fail-closed behavior too, so a
	// container whose TLS configuration is half-written reports unhealthy
	// rather than probing the wrong scheme forever.
	//
	// It also inherits the self-provisioning case, and inherits it as a
	// DECISION only: resolveTLS computes where that certificate lives and
	// writes nothing. That split is what keeps this subcommand safe to run
	// every few seconds for the life of the container. A probe that
	// generated a certificate would race the server it is probing, and
	// would eventually hand the listener a key it is not serving.
	tlsCfg, err := resolveTLS()
	if err != nil {
		fmt.Fprintf(os.Stderr, "controller %s: %v\n", healthcheckCommand, err)
		return 2
	}

	// The same LISTEN_ADDR and the same default main() binds the server to,
	// read through the same helper, so the probe cannot end up asking a
	// different port than the one the server is on.
	target, err := readyzURL(getenv("LISTEN_ADDR", defaultListenAddr), tlsCfg.Mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "controller %s: %v\n", healthcheckCommand, err)
		return 2
	}

	transport, err := probeTransport(tlsCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "controller %s: %v\n", healthcheckCommand, err)
		// A self-provisioned certificate that is not there yet is the
		// startup window, not a caller error. Docker begins probing the
		// moment the container starts, and the server writes this
		// certificate before it opens its listener, so "no such file" means
		// "the server has not gotten that far", which is precisely what
		// exit 1 says. Reporting 2 here would tell the operator reading the
		// logs that their configuration is wrong during every cold start.
		//
		// The distinction only holds for the mode this process provisions
		// itself. A missing TLS_CERT_FILE really is a caller error: nothing
		// is going to create it.
		if tlsCfg.Mode == tlsModeSelfProvisioned && errors.Is(err, fs.ErrNotExist) {
			return 1
		}
		return 2
	}

	if err := probeReadiness(target, transport, *timeout); err != nil {
		fmt.Fprintf(os.Stderr, "controller %s: %v\n", healthcheckCommand, err)
		return 1
	}
	return 0
}

// readyzURL turns the address the server binds into the URL to ask.
//
// The scheme follows the mode rather than being fixed, because both are now
// real. When this process terminates TLS, its only listener speaks TLS and a
// plain-HTTP probe of it gets a handshake error rather than a readiness
// document. When an ingress in front terminates TLS instead, the local
// listener really is plain HTTP, and a probe inside the container must not
// try to speak TLS to it.
func readyzURL(listenAddr string, mode tlsMode) (string, error) {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", fmt.Errorf("LISTEN_ADDR %q is not a host:port address: %w", listenAddr, err)
	}

	// A wildcard bind names every interface, which is not an address a
	// client can dial. Loopback is the right target regardless of which
	// interfaces the server listens on: the process being probed is in this
	// container, and a probe that left the container would be reporting on
	// something else. SplitHostPort has already stripped the brackets from
	// an address written as "[::]:8080", so the IPv6 wildcard arrives here
	// as a bare "::".
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}

	// Plain HTTP is the exception rather than the rule now: two of the
	// three modes terminate TLS on this listener, and only the one that
	// declares an ingress in front does not.
	scheme := "https"
	if mode == tlsModeUpstream {
		scheme = "http"
	}

	// JoinHostPort, not string concatenation, because it puts an IPv6
	// literal back inside brackets. "::1:8080" is not a URL host.
	return scheme + "://" + net.JoinHostPort(host, port) + "/readyz", nil
}

// probeTransport builds the transport the probe dials with.
//
// Proxy is nil on purpose, rather than http.DefaultTransport's
// ProxyFromEnvironment. A self-probe must reach the process inside this
// container, and an HTTP_PROXY set for outbound traffic must never be able
// to answer on its behalf. DisableKeepAlives because this process makes
// exactly one request and then exits, so a pooled connection would only ever
// be left for the kernel to reap.
//
// On the TLS path the trust anchor is this deployment's OWN certificate
// material, never the system pool and never InsecureSkipVerify. That is a
// stronger check than either alternative, not a shortcut around one: the
// handshake only succeeds if the listener presents a certificate this
// container was configured to serve and proves it holds the matching key, so
// a different process that grabbed the port cannot answer for it. Turning
// verification off would have made this probe report "ready" for anything
// listening.
//
// What the anchor IS differs by mode, and the difference is a defect this
// probe used to have. A controller serves the pair it loaded at start-up,
// from memory, for as long as it runs. In the self-provisioning mode the
// directory can move on underneath it: a second controller sharing the
// volume renews, cert.pem becomes a certificate the first one is not
// serving, and a probe anchored on that file alone reports a perfectly
// healthy process unhealthy until an orchestrator kills it. So that mode
// anchors on the whole set of certificates this deployment provisioned
// (internal/tlscert's AnchorsForDir), which covers both processes and trusts
// nothing this deployment did not write. The operator-configured mode has no
// such set: TLS_CERT_FILE is whatever the operator put there, and a rotation
// they perform is picked up by the restart that also makes the server load
// it.
func probeTransport(cfg tlsSettings) (*http.Transport, error) {
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	if !cfg.ServesTLS() {
		return transport, nil
	}

	anchors, err := trustAnchorsFor(cfg)
	if err != nil {
		// Wrapped by internal/tlscert so runHealthcheck can still tell "not
		// written yet", which is a cold start, from "unreadable", which is a
		// real fault.
		return nil, fmt.Errorf("verifying the local listener: %w", err)
	}

	transport.TLSClientConfig = &tls.Config{
		RootCAs: anchors.Roots,
		// The same floor main() serves with.
		MinVersion: tls.VersionTLS12,
		// The name this probe asks to be shown, which is not the address it
		// dials. The probe always dials loopback, because the process it is
		// checking is in this container, while a real deployment's
		// certificate is issued for a public hostname and carries no
		// loopback SAN at all. Sending the certificate's own first DNS name
		// as SNI is what lets one probe work for both a hostname
		// certificate and a development one, without weakening the check:
		// the certificate still has to be a configured one, and its key
		// still has to be proven.
		ServerName: anchors.ServerName,
	}
	return transport, nil
}

// trustAnchorsFor picks which set of certificates this probe will accept.
func trustAnchorsFor(cfg tlsSettings) (tlscert.Anchors, error) {
	if cfg.Mode == tlsModeSelfProvisioned {
		return tlscert.AnchorsForDir(cfg.AutocertDir)
	}
	return tlscert.AnchorsForFile(cfg.CertFile)
}

// probeReadiness performs the request and decides whether the answer means
// ready.
//
// Standard library only, deliberately: this runs in an image that contains
// one executable, so every dependency it gained would have to be linked
// into the server binary that every container also runs.
//
// transport arrives as a parameter rather than being built here, because
// building it can fail (an unreadable or malformed certificate file) and
// that is a caller error worth a different exit code than "not ready".
func probeReadiness(target string, transport *http.Transport, timeout time.Duration) error {
	// One context bounds everything, including the dial, which a bare
	// Client.Timeout would also cover but which is stated here so the
	// deadline travels with the request rather than living on the client.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("building the request for %s: %w", target, err)
	}

	// See probeTransport for why this transport proxies nothing, pools
	// nothing, and (on the TLS path) trusts exactly one certificate.
	client := &http.Client{Transport: transport}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("requesting %s: %w", target, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHealthcheckBody))
	if err != nil {
		return fmt.Errorf("reading the response from %s: %w", target, err)
	}

	if resp.StatusCode != http.StatusOK {
		// The body is safe to print. readyzHandler reports each check as
		// only "ok" or "failed" and never echoes the underlying driver
		// error, precisely so an unauthenticated endpoint leaks no paths or
		// hostnames, so quoting it here names the failing dependency and
		// nothing more.
		return fmt.Errorf("%s answered %s: %s", target, resp.Status, strings.TrimSpace(string(body)))
	}

	var reported struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &reported); err != nil {
		return fmt.Errorf("%s answered 200 with a body that is not a readiness document: %w", target, err)
	}
	if reported.Status != readyStatus {
		return fmt.Errorf("%s answered 200 but reports status %q rather than %q", target, reported.Status, readyStatus)
	}
	return nil
}
