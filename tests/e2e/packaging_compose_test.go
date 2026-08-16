//go:build integration

// Phase 20's Release Gate, Docker Compose half.
//
// The claim: somebody who has just cloned this repository, and who runs no
// preparatory command of any kind, types one line and gets a working
// control plane they can sign into over TLS.
//
// Every word of that is asserted here against the real docker-compose.yml,
// the real images built from the real Dockerfiles, and the real
// bootstrap-admin subcommand. Nothing is stubbed, and no step is allowed
// to be "arranged" by the test: the certificate is the one the controller
// provisioned for itself, the administrator is created by the documented
// command, and the sign-in is the same two-request browser exchange
// internal/ui/web serves.
//
// WHAT THIS TEST DOES TO THE MACHINE, stated plainly because it is
// destructive and a reader deserves to know before running it. It removes
// the compose stack and its named volumes (`docker compose down -v`), and
// it deletes the two locally built images so the cold measurement is a
// real build rather than a cache hit. Both are restored by the test
// itself: the images are rebuilt on the way through, and the stack is
// brought down at the end. Any data in the local development stack is
// gone, which is what `down -v` means everywhere else in this repository
// too.
package e2e

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The account this half creates, distinct from anything the rest of the
// suite seeds so a failure cannot be blamed on a collision.
const (
	composeGateEmail    = "compose-gate-admin@example.test"
	composeGatePassword = "a-real-compose-gate-password"
)

// composeWarmTarget is the budget for a warm `docker compose up -d
// --wait`, meaning one where the images already exist.
//
// Ten seconds is the phase's own target and it is not arbitrary: this
// number is what the start_period and start_interval keys on the postgres,
// nats and controller healthchecks were added to buy. Without them the
// same stack took about 14 seconds, because a Docker healthcheck does not
// probe when a container starts, it probes one full interval later, three
// times over in a chain of three dependent services.
const composeWarmTarget = 10 * time.Second

// composeWarmCeiling is the point at which a slow warm start stops being
// contention and starts being a regression.
//
// The gate measures the warm start more than once and judges the FASTEST
// run against composeWarmTarget, because this runs on a shared machine
// that may be building images for another test at the same moment, and one
// slow sample is contention rather than evidence. The ceiling applies to
// every sample, so a stack that is uniformly slow still fails even if the
// target is met once.
const composeWarmCeiling = 40 * time.Second

// composeWarmSamples is how many warm starts are measured. Two is enough
// to tell one contended sample from a uniformly slow stack, and each one
// costs a full down/up cycle.
const composeWarmSamples = 2

// TestPackagingReleaseGate_ComposeStack is the Docker Compose half of
// Phase 20's Release Gate.
func TestPackagingReleaseGate_ComposeStack(t *testing.T) {
	requireDockerDaemon(t)
	root := ensurePleiadesImages(t)

	// The machine is left as this test found it conceptually (no stack, no
	// volumes), whatever happens in between.
	t.Cleanup(func() { composeDown(t, root) })

	// A clean state, with no preparatory command, is the premise of the
	// claim. `down -v` destroys the named volumes, so the controller has no
	// certificate, the database has no schema and the broker has no
	// streams: everything the stack needs, it has to create.
	composeDown(t, root)

	coldElapsed := measureComposeCold(t, root)
	t.Logf("COLD `docker compose up -d --wait` (no image, no container, no volume; "+
		"BuildKit layer cache as found): %s", coldElapsed.Round(time.Millisecond))

	warmElapsed := measureComposeWarm(t, root)

	// Judged on the fastest sample, for the reason composeWarmCeiling
	// records. The slowest is checked separately below.
	fastest := warmElapsed[0]
	for _, sample := range warmElapsed {
		if sample < fastest {
			fastest = sample
		}
		if sample > composeWarmCeiling {
			t.Errorf("a warm `docker compose up -d --wait` took %s, over the %s ceiling; "+
				"that is no longer contention", sample.Round(time.Millisecond), composeWarmCeiling)
		}
	}
	t.Logf("WARM `docker compose up -d --wait`: %v (fastest %s, target %s)",
		roundAll(warmElapsed), fastest.Round(time.Millisecond), composeWarmTarget)
	if fastest > composeWarmTarget {
		t.Errorf("the fastest warm start took %s, over the %s target",
			fastest.Round(time.Millisecond), composeWarmTarget)
	}

	// One administrator, created by the documented non-interactive form of
	// the documented command. Nothing seeds a row directly, because a
	// seeded row proves the schema works and says nothing about whether the
	// command an operator types produces one.
	out := mustRunPackagingTool(t, root, nil, composeGatePassword+"\n",
		"docker", "compose", "run", "--rm", "-T", "controller",
		"bootstrap-admin", "--email", composeGateEmail, "--password-stdin")
	if !strings.Contains(out, "is ready") {
		t.Fatalf("bootstrap-admin did not report success:\n%s", out)
	}

	assertComposeRefusesPlainHTTP(t)

	// The certificate is fetched the way docker-compose.yml's own header
	// tells an operator to fetch it, out of band through the Docker socket
	// rather than off the TLS connection being tested. That is what makes
	// the sign-in below a verified TLS session rather than a trusted-on-
	// first-use one: the trust anchor did not come from the server.
	client := composeTLSClient(t, root)
	signInOverTLS(t, client)
}

// composeDown removes the stack and its named volumes.
//
// Failures are logged rather than fatal. This runs in cleanup as well as
// at the start, and a cleanup that fails the test for a stack that was
// already gone reports a problem that does not exist.
func composeDown(t *testing.T, root string) {
	t.Helper()
	if out, err := runPackagingTool(t, root, nil, "",
		"docker", "compose", "down", "-v", "--remove-orphans"); err != nil {
		t.Logf("docker compose down -v: %v\n%s", err, out)
	}
}

// measureComposeCold deletes the two locally built images and times the
// `up -d --wait` that has to rebuild them.
//
// There is no threshold on the result and there should not be: the number
// is dominated by a Go compile whose speed says nothing about the
// packaging. It is measured and logged because "how long does the first
// one take" is the question every new operator asks, and an unmeasured
// answer in the documentation is a guess.
//
// What "cold" means here, stated exactly so nobody reads more into the
// number than it carries: no image, no container, no volume, and a warm
// BuildKit layer cache. A build with `--no-cache` would be a different and
// much larger number, and it would be measuring the Go toolchain and the
// Debian mirror rather than this repository.
func measureComposeCold(t *testing.T, root string) time.Duration {
	t.Helper()

	// Errors ignored on purpose: an image that is already absent is the
	// state this wants, so "no such image" is success spelled differently.
	if out, err := runPackagingTool(t, root, nil, "",
		"docker", "image", "rm", packagingControllerImage, packagingRunnerImage); err != nil {
		t.Logf("removing the built images before the cold measurement (already absent is fine): %v\n%s", err, out)
	}

	started := time.Now()
	mustRunPackagingTool(t, root, nil, "", "docker", "compose", "up", "-d", "--wait")
	elapsed := time.Since(started)

	assertFourHealthyServices(t, root)
	return elapsed
}

// measureComposeWarm times repeated warm starts, each from destroyed
// volumes so that every sample measures the same work.
//
// Destroying the volumes between samples matters. With them kept, postgres
// skips initdb and the samples measure a different, faster stack than the
// one a first-time operator starts, which would make the gate report a
// number nobody ever experiences.
func measureComposeWarm(t *testing.T, root string) []time.Duration {
	t.Helper()

	samples := make([]time.Duration, 0, composeWarmSamples)
	for i := 0; i < composeWarmSamples; i++ {
		composeDown(t, root)

		started := time.Now()
		mustRunPackagingTool(t, root, nil, "", "docker", "compose", "up", "-d", "--wait")
		samples = append(samples, time.Since(started))

		assertFourHealthyServices(t, root)
	}
	return samples
}

// assertFourHealthyServices proves `up -d --wait` returning zero meant
// what it appears to mean.
//
// The exit code alone has been wrong here before: with a bad DB_DSN,
// `up -d --wait` printed "controller-1 Healthy" and exited 0 while the
// controller was dead, because the service carried no healthcheck and
// compose treats an unprobed service as satisfied. So this asserts the
// per-service health compose reports, not the exit code.
func assertFourHealthyServices(t *testing.T, root string) {
	t.Helper()
	services := composeServices(t, root)

	// All FOUR services carry a healthcheck now and all four must report
	// healthy. The runner joined them in Phase 20: this used to assert
	// only that it was RUNNING, because the image held nothing able to
	// answer "am I still consuming", and running is exactly the claim
	// FAILURE_PATTERNS.md #119 showed to be worthless. Its probe reads a
	// heartbeat the agent writes only after a real round trip to the
	// durable consumer it pulls from, so healthy here means it genuinely
	// reached the broker rather than merely started.
	for _, name := range []string{"postgres", "nats", "controller", "runner"} {
		service, ok := services[name]
		if !ok {
			t.Fatalf("service %q is not running after `up -d --wait`; compose reported %v", name, services)
		}
		if service.Health != "healthy" {
			t.Errorf("service %q reports health %q, want healthy (state %q)", name, service.Health, service.State)
		}
	}

	if len(services) != 4 {
		t.Errorf("compose reports %d running services, want exactly 4: %v", len(services), services)
	}
}

// assertComposeRefusesPlainHTTP proves the TLS in "sign in over TLS" is
// not optional.
//
// This is the assertion that would have caught the defect the whole TLS
// change exists to fix. The session cookie is Secure and __Host- prefixed
// unconditionally, so a browser silently drops it on a plain-HTTP origin
// and reports a correct password as bad credentials. The controller
// therefore refuses to speak HTTP at all unless an operator states that
// something in front of it terminates TLS, and this compose file
// deliberately does not state that.
//
// WHAT COUNTS AS A REFUSAL, since "not a 200" is not the same claim and
// this check used to make the weaker one. A controller that really served
// plain HTTP would answer a redirect to the sign-in page (302), or a 404,
// or a 401, and every one of those passed a check that only failed on an
// exact 200 while the origin was plain HTTP the whole time. The property
// under test is that NO HTTP application response is served on that port,
// so the only two acceptable outcomes are:
//
//   - a transport error, meaning nothing answered the plain-HTTP request
//     at all; or
//   - Go's own TLS-listener reply, which is a 400 saying the client sent
//     an HTTP request to an HTTPS server. That response comes from
//     net/http before any handler runs, so it is evidence the listener is
//     speaking TLS rather than evidence about a route.
//
// Anything else, at any status code, is the controller answering over
// plain HTTP.
func assertComposeRefusesPlainHTTP(t *testing.T) {
	t.Helper()

	client := &http.Client{
		Timeout: 15 * time.Second,
		// Redirects are NOT followed. A 302 to an https:// location would
		// otherwise be silently upgraded by the client and reported as the
		// success of the request that came after it, hiding the fact that
		// something answered the plain-HTTP one.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	resp, err := client.Get("http://localhost:8080/ui/login")
	if err != nil {
		// A transport error is also a refusal, and a legitimate one. Say
		// what happened and stop, rather than asserting a status code that
		// no response carries.
		t.Logf("plain HTTP was refused at the transport level: %v", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	text := strings.TrimSpace(string(body))

	// The exact sentence net/http writes when a plain-HTTP request reaches
	// a TLS listener. Matched on the distinctive half of it so a Go release
	// rewording the surrounding text does not turn this into a false
	// failure, while any OTHER 400 (one a handler produced) still fails.
	const tlsMismatch = "HTTP request to an HTTPS server"
	if resp.StatusCode == http.StatusBadRequest && strings.Contains(text, tlsMismatch) {
		t.Logf("plain HTTP was refused by the TLS listener: %d %s", resp.StatusCode, text)
		return
	}

	t.Fatalf("the controller answered a plain-HTTP request with %d, so it is serving HTTP on that "+
		"origin. Any application response here is the defect, not just a 200: the session cookie is "+
		"Secure and __Host- prefixed, so a browser drops it on this origin and reports a correct "+
		"password as bad credentials. Expected either no answer at all or net/http's %q reply.\n%s",
		resp.StatusCode, tlsMismatch, text)
}

// composeTLSClient builds an HTTP client that trusts exactly one
// certificate: the one the controller provisioned into its own data volume
// and nothing else.
//
// An InsecureSkipVerify client would have made this test pass against
// anything at all answering on port 8080, which is the property a
// self-signed certificate is most often accused of having. It does not
// have it here: the pool below holds one certificate, fetched through the
// Docker socket rather than off the connection, so a mismatched name, an
// expired certificate or a different server all fail the handshake.
func composeTLSClient(t *testing.T, root string) *http.Client {
	t.Helper()

	certPath := filepath.Join(t.TempDir(), "controller-cert.pem")
	mustRunPackagingTool(t, root, nil, "",
		"docker", "compose", "cp", "controller:/data/tls/cert.pem", certPath)

	pem, err := os.ReadFile(certPath) // #nosec G304 -- a path this test just created under t.TempDir()
	if err != nil {
		t.Fatalf("reading the certificate copied out of the controller: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatalf("the file at %s is not a PEM certificate:\n%s", certPath, pem)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Jar:     jar,
		// Redirects are stopped so the 303 the sign-in returns can be
		// asserted directly. A client that followed it would report the
		// destination's status and hide whether the login itself succeeded.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: pool,
				// Go's default is TLS 1.2, and this states the floor rather
				// than inheriting it, because the gate is about what the
				// deployment serves.
				MinVersion: tls.VersionTLS12,
			},
		},
	}
}

// signInOverTLS performs the whole browser exchange against the compose
// stack and proves the session it mints reaches an authenticated page.
func signInOverTLS(t *testing.T, client *http.Client) {
	t.Helper()
	const base = "https://localhost:8080"

	// The sign-in page, which both sets the pre-auth CSRF cookie and
	// carries its matching token. POSTing without this step exercises a
	// request no browser ever makes.
	resp, err := client.Get(base + "/ui/login")
	if err != nil {
		t.Fatalf("GET /ui/login over TLS: %v", err)
	}
	loginPage, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ui/login = %d, want 200\n%s", resp.StatusCode, loginPage)
	}
	if resp.TLS == nil {
		t.Fatal("the sign-in page was served without TLS")
	}
	t.Logf("sign-in page served over %s with certificate CN %q",
		tls.VersionName(resp.TLS.Version), resp.TLS.PeerCertificates[0].Subject.CommonName)

	form := url.Values{
		"email":    {composeGateEmail},
		"password": {composeGatePassword},
		"_csrf":    {csrfFrom(t, string(loginPage))},
	}
	posted, err := client.PostForm(base+"/ui/login", form)
	if err != nil {
		t.Fatalf("POST /ui/login over TLS: %v", err)
	}
	postBody, _ := io.ReadAll(posted.Body)
	_ = posted.Body.Close()
	if posted.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /ui/login = %d, want 303\n%s", posted.StatusCode, postBody)
	}
	if strings.Contains(string(postBody), composeGatePassword) {
		t.Error("the login response echoes the submitted password")
	}

	// The session cookie the browser will actually keep. __Host- prefixed
	// and Secure, which is exactly why this whole exchange has to happen
	// over TLS.
	var session *http.Cookie
	for _, cookie := range posted.Cookies() {
		if strings.HasPrefix(cookie.Name, "__Host-") && cookie.Value != "" {
			session = cookie
		}
	}
	if session == nil {
		t.Fatal("the sign-in set no __Host- prefixed session cookie")
	}
	if !session.Secure || !session.HttpOnly {
		t.Errorf("session cookie %q has Secure=%v HttpOnly=%v, want both true",
			session.Name, session.Secure, session.HttpOnly)
	}

	// And the session reaches an authenticated page. A redirect back to the
	// sign-in form, or the form rendered inline, both mean the cookie was
	// minted and then not accepted, which is the failure this gate is
	// ultimately about.
	dashboard, err := client.Get(base + "/ui/dashboard")
	if err != nil {
		t.Fatalf("GET /ui/dashboard over TLS: %v", err)
	}
	dashBody, _ := io.ReadAll(dashboard.Body)
	_ = dashboard.Body.Close()
	if dashboard.StatusCode != http.StatusOK {
		t.Fatalf("GET /ui/dashboard = %d, want 200\n%s", dashboard.StatusCode, dashBody)
	}
	if strings.Contains(string(dashBody), "Sign in") {
		t.Error("the dashboard rendered the sign-in page; the session did not authenticate")
	}
}

// roundAll rounds a set of durations for a log line, so the message reads
// as milliseconds rather than nanoseconds.
func roundAll(samples []time.Duration) []time.Duration {
	rounded := make([]time.Duration, len(samples))
	for i, sample := range samples {
		rounded[i] = sample.Round(time.Millisecond)
	}
	return rounded
}
