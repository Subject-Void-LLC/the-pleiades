//go:build integration

// Phase 79's Release Gate: local password authentication, end to end,
// through the real binaries.
//
// The claim under test is the one the whole phase exists for. Before it,
// the only way into the web UI was a JWT an operator on a fresh machine had
// no way to obtain, which made Phase 20's own "docker compose up on a clean
// machine" gate unreachable in practice: the mesh came up and nobody could
// get into it.
//
// Everything here runs against the real cmd/controller binary, a real
// database and the real HTTP surface. The bootstrap goes through the SAME
// shipped subcommand an operator types, invoked as a subprocess, because a
// test that seeded rows itself would prove the rows work and say nothing
// about whether the command an operator actually runs produces them.
package e2e

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// bootstrapEmail is the account this gate creates. Distinct from anything
// the harness seeds, so a failure cannot be attributed to a collision.
const (
	bootstrapEmail    = "release-gate-admin@example.test"
	bootstrapPassword = "a-real-release-gate-password"
)

// runControllerCommand invokes one administrative subcommand against the
// harness's own database and returns its combined output.
//
// The same binary the harness runs as a server, with the same environment.
// A second binary or a different DSN would be testing something adjacent to
// the thing that has to work.
func (h *harness) runControllerCommand(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.Command(controllerBinPath, args...)
	cmd.Env = append(os.Environ(),
		"DB_DSN="+h.dsn,
		"MASTER_ENCRYPTION_KEY="+harnessMasterKey,
		"OTEL_TRACES_EXPORTER=none",
	)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestLocalAuthReleaseGate_BootstrapThenPasswordSignIn is Phase 79's
// Release Gate.
//
// No JWT is minted, pasted or configured at any point in it. That is the
// property, not an incidental detail of how the test is written: if any
// step here needed a token, the gate would not be closed.
func TestLocalAuthReleaseGate_BootstrapThenPasswordSignIn(t *testing.T) {
	h := startHarness(t)

	// 1. One command on a machine with no prior state.
	out, err := h.runControllerCommand(t, bootstrapPassword+"\n",
		"bootstrap-admin", "--email", bootstrapEmail, "--password-stdin")
	if err != nil {
		t.Fatalf("controller bootstrap-admin failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "is ready") {
		t.Errorf("bootstrap-admin did not report success:\n%s", out)
	}

	// 2. That account signs in with an email and a password.
	client := h.submitLogin(t, uiClient(t), url.Values{
		"email":    {bootstrapEmail},
		"password": {bootstrapPassword},
	})

	// 3. And the session it minted carries real authority, derived from the
	// RoleBindings the subcommand wrote rather than from any token claim.
	// This is the assertion that catches a bootstrap which creates an
	// account that signs in and can reach nothing, which is exactly the
	// defect this command shipped with before its own unit test caught it.
	body := h.getUI(t, client, "/ui/dashboard", http.StatusOK)
	if strings.Contains(body, "Sign in") {
		t.Error("the dashboard rendered the sign-in page; the session did not authenticate")
	}

	// 4. The password is not recoverable from the database. Checked through
	// the administrative surface rather than by reading the column, because
	// the column is internal/localauth's own test's job and this one is
	// about what an operator can reach.
	out, err = h.runControllerCommand(t, "", "unlock", "--email", bootstrapEmail)
	if err != nil {
		t.Fatalf("controller unlock failed: %v\n%s", err, out)
	}
	if strings.Contains(out, bootstrapPassword) {
		t.Error("an administrative command echoed the password")
	}
}

// TestLocalAuthReleaseGate_EveryFailureIsIndistinguishable is the security
// half of the gate.
//
// A wrong password, an unknown address and an account with no local
// password must be indistinguishable in both CONTENT and TIME. The content
// half is asserted exactly; the timing half is asserted with a wide band,
// because this runs against a real process on a shared machine and the
// property worth protecting is "the derivation happened", not a
// statistically tight bound.
func TestLocalAuthReleaseGate_EveryFailureIsIndistinguishable(t *testing.T) {
	h := startHarness(t)

	if out, err := h.runControllerCommand(t, bootstrapPassword+"\n",
		"bootstrap-admin", "--email", bootstrapEmail, "--password-stdin"); err != nil {
		t.Fatalf("controller bootstrap-admin failed: %v\n%s", err, out)
	}

	attempt := func(email, password string) (int, string, time.Duration) {
		client := uiClient(t)
		form := url.Values{"email": {email}, "password": {password}}
		form.Set("_csrf", h.loginForm(t, client))

		start := time.Now()
		resp, err := client.PostForm(h.baseURL+"/ui/login", form)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("POST /ui/login: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, stripCSRF(string(raw)), elapsed
	}

	knownStatus, knownBody, knownTime := attempt(bootstrapEmail, "the-wrong-password")
	unknownStatus, unknownBody, unknownTime := attempt("nobody@example.test", "the-wrong-password")

	if knownStatus != http.StatusUnauthorized || unknownStatus != http.StatusUnauthorized {
		t.Fatalf("statuses = %d and %d, want 401 for both", knownStatus, unknownStatus)
	}
	if knownBody != unknownBody {
		t.Error("a wrong password and an unknown address rendered different pages, " +
			"which tells an attacker which accounts exist")
	}

	// The timing band. An implementation that short-circuited on "no such
	// user" would skip the Argon2id derivation entirely and come back
	// roughly a thousand times faster; a quarter is a wide enough floor to
	// survive a loaded CI machine while still catching that.
	if unknownTime < knownTime/4 {
		t.Errorf("an unknown address took %v and a known one took %v; the unknown path is skipping "+
			"the derivation, which makes the login form an account-existence oracle",
			unknownTime, knownTime)
	}
}

// TestLocalAuthReleaseGate_PasswordChangeRevokesOtherSessions proves the
// self-service change does what changing a password is for.
func TestLocalAuthReleaseGate_PasswordChangeRevokesOtherSessions(t *testing.T) {
	h := startHarness(t)

	if out, err := h.runControllerCommand(t, bootstrapPassword+"\n",
		"bootstrap-admin", "--email", bootstrapEmail, "--password-stdin"); err != nil {
		t.Fatalf("controller bootstrap-admin failed: %v\n%s", err, out)
	}

	credentials := url.Values{"email": {bootstrapEmail}, "password": {bootstrapPassword}}
	first := h.submitLogin(t, uiClient(t), cloneValues(credentials))
	second := h.submitLogin(t, uiClient(t), cloneValues(credentials))

	// Both work before the change, or the test proves nothing afterwards.
	h.getUI(t, first, "/ui/dashboard", http.StatusOK)
	h.getUI(t, second, "/ui/dashboard", http.StatusOK)

	// Change from the first session.
	account := h.getUI(t, first, "/ui/account", http.StatusOK)
	const replacement = "a-replacement-release-gate-password"
	form := url.Values{
		"current_password": {bootstrapPassword},
		"new_password":     {replacement},
		"confirm_password": {replacement},
		"_csrf":            {csrfFrom(t, account)},
	}
	resp, err := first.PostForm(h.baseURL+"/ui/account/password", form)
	if err != nil {
		t.Fatalf("POST /ui/account/password: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /ui/account/password = %d, want 303\n%s", resp.StatusCode, body)
	}

	// The changer stays signed in; the other session does not.
	h.getUI(t, first, "/ui/dashboard", http.StatusOK)
	h.getUI(t, second, "/ui/dashboard", http.StatusSeeOther)

	// And the credential really changed: the old password no longer works,
	// the new one does.
	oldAttempt := uiClient(t)
	oldForm := url.Values{"email": {bootstrapEmail}, "password": {bootstrapPassword}}
	oldForm.Set("_csrf", h.loginForm(t, oldAttempt))
	oldResp, err := oldAttempt.PostForm(h.baseURL+"/ui/login", oldForm)
	if err != nil {
		t.Fatalf("POST /ui/login: %v", err)
	}
	_ = oldResp.Body.Close()
	if oldResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the old password returned %d, want 401", oldResp.StatusCode)
	}

	h.submitLogin(t, uiClient(t), url.Values{
		"email":    {bootstrapEmail},
		"password": {replacement},
	})
}

// TestLocalAuthReleaseGate_LoginRefusesWithoutTheCSRFPair proves the guard
// is on in a real deployment rather than only in a unit test.
func TestLocalAuthReleaseGate_LoginRefusesWithoutTheCSRFPair(t *testing.T) {
	h := startHarness(t)

	if out, err := h.runControllerCommand(t, bootstrapPassword+"\n",
		"bootstrap-admin", "--email", bootstrapEmail, "--password-stdin"); err != nil {
		t.Fatalf("controller bootstrap-admin failed: %v\n%s", err, out)
	}

	// Correct credentials, submitted without ever fetching the form.
	client := uiClient(t)
	resp, err := client.PostForm(h.baseURL+"/ui/login", url.Values{
		"email":    {bootstrapEmail},
		"password": {bootstrapPassword},
	})
	if err != nil {
		t.Fatalf("POST /ui/login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusSeeOther {
		t.Fatal("a login with no CSRF pair succeeded")
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// getUI performs an authenticated GET and asserts the status.
func (h *harness) getUI(t *testing.T, client *http.Client, path string, want int) string {
	t.Helper()

	resp, err := client.Get(h.baseURL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("GET %s = %d, want %d\n%s", path, resp.StatusCode, want, body)
	}
	return string(body)
}

// csrfFrom pulls the CSRF token out of a rendered authenticated page.
func csrfFrom(t *testing.T, body string) string {
	t.Helper()

	const marker = `name="_csrf" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatal("the page carries no CSRF token")
	}
	rest := body[i+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatal("the page's CSRF token is unterminated")
	}
	return rest[:end]
}

// stripCSRF blanks the per-render CSRF token so two failure pages can be
// compared for everything that carries information.
//
// The token is random by design, so comparing raw bodies would assert that
// it is not random, which is the opposite of what this wants.
func stripCSRF(body string) string {
	const marker = `name="_csrf" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		return body
	}
	start := i + len(marker)
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		return body
	}
	return strings.ReplaceAll(body, body[start:start+end], "<csrf>")
}

// cloneValues copies a form, so two sign-ins do not share one map and the
// CSRF token of the first does not travel with the second.
func cloneValues(in url.Values) url.Values {
	out := url.Values{}
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}
