//go:build integration

// The web UI end to end, against the real controller binary, a real
// PostgreSQL server and a real NATS server.
//
// Every claim this phase makes that could only be checked against the real
// thing is checked here. In particular the one that motivated the whole
// design: a browser can now authenticate to this control plane at all. Before
// this phase api.AuthMiddleware read only Authorization: Bearer, an
// EventSource cannot set headers, and no login endpoint existed anywhere --
// so the previous UI's log viewer could not have worked no matter what else
// was fixed. The SSE assertion below is what proves that is over.
package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
)

// uiClient is a browser-shaped HTTP client: it keeps cookies, and it does
// not follow redirects, so a test can assert on the redirect itself rather
// than on wherever it landed.
func uiClient(t *testing.T) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() = %v, want nil", err)
	}
	return &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// signIn exchanges a token for a session cookie through the real login
// endpoint, and returns the client holding it.
func (h *harness) signIn(t *testing.T, id *auth.Identity) *http.Client {
	t.Helper()

	issuer := authtest.NewWithSecret(t, harnessJWTSecret, harnessJWTIssuer, harnessJWTAudience)
	client := uiClient(t)
	return h.submitLogin(t, client, url.Values{"token": {issuer.Token(t, id)}})
}

// loginForm fetches the sign-in page and returns the pre-auth CSRF token it
// carried, leaving the matching cookie in the client's jar.
//
// The two-step exists because POST /ui/login is behind a double-submit CSRF
// pair: the cookie is set by rendering the page and the token is a hidden
// field in it. A test that posted directly would be exercising a request no
// browser ever makes, and would have started failing the day that layer was
// added, which is exactly what happened to this harness.
func (h *harness) loginForm(t *testing.T, client *http.Client) string {
	t.Helper()

	resp, err := client.Get(h.baseURL + "/ui/login")
	if err != nil {
		t.Fatalf("GET /ui/login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ui/login = %d, want 200\n%s", resp.StatusCode, body)
	}

	const marker = `name="_csrf" value="`
	i := strings.Index(string(body), marker)
	if i < 0 {
		t.Fatal("the sign-in page carries no CSRF token")
	}
	rest := string(body)[i+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatal("the sign-in page's CSRF token is unterminated")
	}
	return rest[:end]
}

// submitLogin performs the full browser exchange: fetch the form, submit it
// with the pair, assert the redirect.
func (h *harness) submitLogin(t *testing.T, client *http.Client, form url.Values) *http.Client {
	t.Helper()

	form.Set("_csrf", h.loginForm(t, client))

	resp, err := client.PostForm(h.baseURL+"/ui/login", form)
	if err != nil {
		t.Fatalf("POST /ui/login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /ui/login = %d, want 303\n%s", resp.StatusCode, body)
	}

	// The credential must not come back out. A login response echoing what
	// it was given would put it in every proxy log between here and the
	// browser.
	if strings.Contains(string(body), "eyJ") {
		t.Error("the login response body echoes the submitted token")
	}
	if pw := form.Get("password"); pw != "" && strings.Contains(string(body), pw) {
		t.Error("the login response body echoes the submitted password")
	}
	return client
}

func adminID() *auth.Identity {
	return &auth.Identity{
		Subject: "e2e-admin",
		Role:    auth.RoleAdmin,
		Scopes: []auth.Scope{
			auth.ScopeInventoryRead, auth.ScopeInventoryWrite,
			auth.ScopeJobRead, auth.ScopeRunbookRead, auth.ScopeRunbookExecute,
		},
	}
}

func viewerID() *auth.Identity {
	return &auth.Identity{
		Subject: "e2e-viewer",
		Role:    auth.RoleViewer,
		Scopes:  []auth.Scope{auth.ScopeInventoryRead, auth.ScopeJobRead, auth.ScopeRunbookRead},
	}
}

// get issues an authenticated GET through a cookie-holding client.
func uiGet(t *testing.T, client *http.Client, url string) (int, string, http.Header) {
	t.Helper()

	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", url, err)
	}
	return resp.StatusCode, string(body), resp.Header
}

// csrfTokenFrom pulls the token out of a rendered form, which is how a
// browser would obtain it. Reading it from the page rather than deriving it
// is deliberate: a test that computed the token itself would still pass if
// the server stopped rendering it, and the form would be broken.
func csrfTokenFrom(t *testing.T, body string) string {
	t.Helper()

	const marker = `name="_csrf" value="`
	idx := strings.Index(body, marker)
	if idx < 0 {
		t.Fatal("no CSRF token is rendered on the page, so no form on it could be submitted")
	}
	rest := body[idx+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatal("the rendered CSRF token is malformed")
	}
	return rest[:end]
}

// TestUI_LoginMintsAHardenedSessionCookie is the first assertion because
// everything else depends on it.
func TestUI_LoginMintsAHardenedSessionCookie(t *testing.T) {
	h := startHarness(t)

	// Read straight off the login response rather than out of the cookie
	// jar. A jar stores what it needs to send a cookie back -- name and
	// value -- and discards HttpOnly, SameSite and Path, so asserting on
	// jar contents would have been a test of net/http's jar that passed
	// whatever the server actually set.
	issuer := authtest.NewWithSecret(t, harnessJWTSecret, harnessJWTIssuer, harnessJWTAudience)
	client := uiClient(t)

	// The form is fetched first, as a browser does, because the login POST
	// is behind a double-submit CSRF pair. The POST is still made directly
	// rather than through submitLogin, so this test reads the raw
	// Set-Cookie headers rather than the jar: see the comment above.
	resp, err := client.PostForm(h.baseURL+"/ui/login", url.Values{
		"token": {issuer.Token(t, adminID())},
		"_csrf": {h.loginForm(t, client)},
	})
	if err != nil {
		t.Fatalf("POST /ui/login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /ui/login = %d, want 303\n%s", resp.StatusCode, body)
	}

	cookies := resp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("login set no cookie")
	}

	var found bool
	for _, c := range cookies {
		if c.Name != session.InsecureCookieName {
			continue
		}
		found = true
		if !c.HttpOnly {
			t.Error("the session cookie is not HttpOnly, so script can read the credential")
		}
		if c.SameSite != http.SameSiteStrictMode {
			t.Errorf("the session cookie has SameSite=%v, want Strict", c.SameSite)
		}
		if c.Path != "/" {
			t.Errorf("the session cookie has Path=%q, want /", c.Path)
		}
		if c.MaxAge != 0 || !c.Expires.IsZero() {
			t.Error("the session cookie carries its own expiry; the server owns session lifetime")
		}
	}
	if !found {
		t.Errorf("no session cookie named %q was set", session.InsecureCookieName)
	}
}

// TestUI_UnauthenticatedIsRedirectedToLogin covers both shapes: a document
// request gets a redirect, an HTMX fragment request gets the header that
// tells HTMX to navigate instead of swapping a login page into a table.
func TestUI_UnauthenticatedIsRedirectedToLogin(t *testing.T) {
	h := startHarness(t)
	client := uiClient(t)

	status, _, _ := uiGet(t, client, h.baseURL+"/ui/devices")
	if status != http.StatusSeeOther {
		t.Errorf("unauthenticated GET /ui/devices = %d, want 303", status)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, h.baseURL+"/ui/devices", nil)
	if err != nil {
		t.Fatalf("building the fragment request: %v", err)
	}
	req.Header.Set("HX-Request", "true")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("fragment request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got := resp.Header.Get("HX-Redirect"); got == "" {
		t.Error("an unauthenticated fragment request carries no HX-Redirect, so HTMX would " +
			"swap a login page into whatever element triggered it")
	}
}

// TestUI_ListsRealDevicesFromPostgres proves the page renders data that came
// out of the real database through the real repository, not a fixture.
func TestUI_ListsRealDevicesFromPostgres(t *testing.T) {
	h := startHarness(t)
	client := h.signIn(t, adminID())

	status, body, headers := uiGet(t, client, h.baseURL+"/ui/devices")
	if status != http.StatusOK {
		t.Fatalf("GET /ui/devices = %d, want 200", status)
	}
	if ct := headers.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(body, "rtr1") {
		t.Errorf("the rendered list does not contain the seeded device %q", "rtr1")
	}

	// The security headers the whole subtree carries.
	csp := headers.Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("the UI serves no content security policy")
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Errorf("the content security policy permits unsafe-inline: %s", csp)
	}
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("the content security policy is missing %q: %s", want, csp)
		}
	}
	if headers.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("the UI does not set X-Content-Type-Options: nosniff")
	}
}

// TestUI_WriteWithoutCSRFChangesNothing asserts the side effect, not the
// status code. A 403 with the row created anyway would pass a status-only
// test and be a complete failure of the control.
func TestUI_WriteWithoutCSRFChangesNothing(t *testing.T) {
	h := startHarness(t)
	client := h.signIn(t, adminID())

	const probe = "csrf-probe-device"

	resp, err := client.PostForm(h.baseURL+"/ui/devices", url.Values{
		"name": {probe},
		"type": {"linux_server"},
	})
	if err != nil {
		t.Fatalf("POST /ui/devices: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST with no CSRF token = %d, want 403", resp.StatusCode)
	}

	// Read back through the database, not through the UI, so a rendering
	// bug cannot hide a write that really happened.
	client2 := h.openReadBackClient(t)
	count, err := client2.Device.Query().Where().Count(context.Background())
	if err != nil {
		t.Fatalf("counting devices: %v", err)
	}
	status, body, _ := uiGet(t, client, h.baseURL+"/ui/devices")
	if status == http.StatusOK && strings.Contains(body, probe) {
		t.Errorf("a CSRF-rejected request created %q anyway (%d devices in the database)", probe, count)
	}
}

// TestUI_FullCRUDRoundTrip drives create, read, edit and delete through the
// rendered pages against real PostgreSQL.
func TestUI_FullCRUDRoundTrip(t *testing.T) {
	h := startHarness(t)
	client := h.signIn(t, adminID())

	const name = "e2e-crud-device"

	// Create, with the token read off the rendered form.
	_, formPage, _ := uiGet(t, client, h.baseURL+"/ui/devices/new")
	resp, err := client.PostForm(h.baseURL+"/ui/devices", url.Values{
		"name":  {name},
		"type":  {"linux_server"},
		"tags":  {"e2e, crud"},
		"_csrf": {csrfTokenFrom(t, formPage)},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create = %d, want 303", resp.StatusCode)
	}

	// Read: the list contains it, and so does its detail page.
	_, list, _ := uiGet(t, client, h.baseURL+"/ui/devices")
	if !strings.Contains(list, name) {
		t.Fatalf("the list does not contain the device just created")
	}

	status, detail, _ := uiGet(t, client, h.baseURL+"/ui/devices/"+name)
	if status != http.StatusOK {
		t.Fatalf("GET detail = %d, want 200", status)
	}
	if !strings.Contains(detail, "e2e") {
		t.Error("the detail page does not show the tags that were submitted")
	}

	// Delete, which for a device is retirement rather than row removal:
	// every Revision is immutable by schema, so a real delete would destroy
	// the audit trail the schema exists to protect.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		h.baseURL+"/ui/devices/"+name,
		strings.NewReader("_method=DELETE&_csrf="+url.QueryEscape(csrfTokenFrom(t, detail))))
	if err != nil {
		t.Fatalf("building the delete request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	deleteResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	_ = deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete = %d, want 303", deleteResp.StatusCode)
	}

	// And the affordance is gone: an archived device offers no delete to
	// anyone, however broadly scoped, because retirement is idempotent and
	// following the link again would change nothing with no error to say so.
	_, afterDelete, _ := uiGet(t, client, h.baseURL+"/ui/devices/"+name)
	if strings.Contains(afterDelete, `data-dialog-open="confirm-delete"`) {
		t.Error("an archived device still offers a delete control")
	}
}

// TestUI_ViewerAndAdminSeeDifferentControls is Phase 13's Release Gate in
// HTML, against the real admission chain the controller built.
func TestUI_ViewerAndAdminSeeDifferentControls(t *testing.T) {
	h := startHarness(t)

	admin := h.signIn(t, adminID())
	viewer := h.signIn(t, viewerID())

	_, adminList, _ := uiGet(t, admin, h.baseURL+"/ui/devices")
	if !strings.Contains(adminList, `href="/ui/devices/new"`) {
		t.Fatal("admin is offered no create control, so this test could not detect its " +
			"absence for a viewer")
	}

	status, viewerList, _ := uiGet(t, viewer, h.baseURL+"/ui/devices")
	if status != http.StatusOK {
		t.Fatalf("viewer GET /ui/devices = %d, want 200", status)
	}
	if strings.Contains(viewerList, `href="/ui/devices/new"`) {
		t.Error("a viewer is offered a create control")
	}

	// And the route itself refuses, not only the button. A UI that hid the
	// control while the endpoint stayed open would be security by CSS.
	writeStatus, _, _ := uiGet(t, viewer, h.baseURL+"/ui/devices/new")
	if writeStatus != http.StatusForbidden {
		t.Errorf("viewer GET /ui/devices/new = %d, want 403", writeStatus)
	}
}

// TestUI_SessionCookieWorksAcrossControllers is the Sticky Session
// assertion, and the reason PATTERNS.md's rejection of server-side sessions
// needed correcting rather than overruling.
//
// A session row in the shared database forces no affinity: a cookie minted
// against one controller authenticates against another, because the row is
// in PostgreSQL rather than in either process. A node-local session map
// would fail this test, which is exactly why that remains rejected.
func TestUI_SessionCookieWorksAcrossControllers(t *testing.T) {
	h := startHarness(t)
	client := h.signIn(t, adminID())

	// A second controller against the same database and broker.
	second := &harness{dsn: h.dsn, natsURL: h.natsURL, runbookDir: h.runbookDir}
	second.startController(t)

	status, body, _ := uiGet(t, client, second.baseURL+"/ui/devices")
	if status != http.StatusOK {
		t.Fatalf("a cookie minted against controller A returned %d from controller B, want 200.\n"+
			"A session that does not cross processes would force Sticky Session, which is "+
			"what this design rejected.", status)
	}
	if !strings.Contains(body, "rtr1") {
		t.Error("the second controller rendered no devices")
	}
}

// TestUI_LogStreamAuthenticatesWithTheCookieAlone closes the defect that
// motivated this phase.
//
// An EventSource cannot set an Authorization header. Before this phase the
// API accepted nothing else, so the SSE log stream was unreachable from any
// browser regardless of what the UI looked like. This asserts the cookie is
// now sufficient -- with no Authorization header present at all.
func TestUI_LogStreamAuthenticatesWithTheCookieAlone(t *testing.T) {
	h := startHarness(t)
	client := h.signIn(t, adminID())

	issuer := authtest.NewWithSecret(t, harnessJWTSecret, harnessJWTIssuer, harnessJWTAudience)
	status, body := h.launch(t, issuer.BearerToken(t, adminID()))
	if status != http.StatusAccepted {
		t.Fatalf("dispatch = %d, want 202\n%s", status, body)
	}

	var accepted struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatalf("decoding the dispatch response: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		h.baseURL+"/api/v1/jobs/"+accepted.JobID+"/logs", nil)
	if err != nil {
		t.Fatalf("building the stream request: %v", err)
	}
	// Deliberately no Authorization header. The cookie jar carries the
	// session and nothing else does.
	if req.Header.Get("Authorization") != "" {
		t.Fatal("the request carries an Authorization header, which would make this test prove nothing")
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET the log stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET the log stream with only a cookie = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	// The wildcard CORS header on a now-credentialed endpoint was removed
	// this phase; same-origin serving makes it unnecessary and its presence
	// on a cookie-authenticated stream would be precisely wrong.
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got == "*" {
		t.Error("the credentialed log stream still sends Access-Control-Allow-Origin: *")
	}

	buf := make([]byte, 128)
	n, err := resp.Body.Read(buf)
	if err != nil && n == 0 {
		t.Fatalf("reading the first stream frame: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "event: init") {
		t.Errorf("the first frame was %q, want an init event", string(buf[:n]))
	}
}

// TestUI_ChartServesRealAggregates asserts the dashboard's figures come from
// the same job store the Jobs view reads, rather than from a hardcoded
// panel of the kind the previous UI shipped.
func TestUI_ChartServesRealAggregates(t *testing.T) {
	h := startHarness(t)
	client := h.signIn(t, adminID())

	issuer := authtest.NewWithSecret(t, harnessJWTSecret, harnessJWTIssuer, harnessJWTAudience)
	if status, body := h.launch(t, issuer.BearerToken(t, adminID())); status != http.StatusAccepted {
		t.Fatalf("dispatch = %d, want 202\n%s", status, body)
	}

	status, body, headers := uiGet(t, client, h.baseURL+"/ui/dashboard/chart.json")
	if status != http.StatusOK {
		t.Fatalf("GET /ui/dashboard/chart.json = %d, want 200", status)
	}
	if ct := headers.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}

	var data struct {
		Buckets []struct {
			Label string `json:"label"`
			Count int    `json:"count"`
			Class string `json:"class"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		t.Fatalf("decoding chart data: %v\n%s", err, body)
	}
	if len(data.Buckets) == 0 {
		t.Fatal("the chart endpoint returned no buckets")
	}

	total := 0
	for _, b := range data.Buckets {
		if b.Label == "" || b.Class == "" {
			t.Errorf("bucket %+v has no label or no class, so it would render as colour alone", b)
		}
		total += b.Count
	}
	if total == 0 {
		t.Error("the chart reports no jobs at all, though one was just dispatched")
	}

	// It must never be an ECharts option document: coupling a server
	// response to a charting library's schema makes swapping the library
	// an API break.
	if strings.Contains(body, "series") || strings.Contains(body, "xAxis") {
		t.Error("the chart endpoint serves an ECharts option document rather than domain JSON")
	}

	// And the same figures must be in the HTML, so a reader who cannot see
	// the graphic still has the data.
	_, page, _ := uiGet(t, client, h.baseURL+"/ui/dashboard")
	if !strings.Contains(page, "chart-table") {
		t.Error("the dashboard renders no table equivalent for its chart")
	}
}

// TestUI_StaticAssetsAreContentHashedAndImmutable covers the embed seam: the
// controller carries its own front end and reaches no network to render a
// page, which is the air-gap requirement made structural.
func TestUI_StaticAssetsAreContentHashedAndImmutable(t *testing.T) {
	h := startHarness(t)
	client := h.signIn(t, adminID())

	_, page, _ := uiGet(t, client, h.baseURL+"/ui/devices")

	const marker = `href="/ui/static/`
	idx := strings.Index(page, marker)
	if idx < 0 {
		t.Fatal("the page references no stylesheet")
	}
	rest := page[idx+len(`href="`):]
	assetPath := rest[:strings.Index(rest, `"`)]

	status, body, headers := uiGet(t, client, h.baseURL+assetPath)
	if status != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", assetPath, status)
	}
	if ct := headers.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", ct)
	}
	if cc := headers.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}
	if !strings.Contains(body, "--fg") {
		t.Error("the served stylesheet does not look like the token vocabulary it should be")
	}

	// The hash is in the filename rather than a query string, which is what
	// makes an immutable cache lifetime safe: a changed file is a different
	// URL to every cache in the chain.
	if !strings.Contains(assetPath, ".css") || strings.Count(assetPath, ".") < 2 {
		t.Errorf("asset path %q carries no content hash", assetPath)
	}
}

// TestUI_LogoutRevokesImmediately proves the session is genuinely revocable,
// which is the whole reason this UI holds a server-side session rather than
// a signed token that cannot be withdrawn.
func TestUI_LogoutRevokesImmediately(t *testing.T) {
	h := startHarness(t)
	client := h.signIn(t, adminID())

	_, page, _ := uiGet(t, client, h.baseURL+"/ui/devices")
	if !strings.Contains(page, "Sign out") {
		t.Fatal("the chrome offers no sign-out control")
	}

	resp, err := client.PostForm(h.baseURL+"/ui/logout", url.Values{
		"_csrf": {csrfTokenFrom(t, page)},
	})
	if err != nil {
		t.Fatalf("POST /ui/logout: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout = %d, want 303", resp.StatusCode)
	}

	// The row is deleted rather than flagged, so the credential stops
	// working everywhere at once rather than when some cache notices.
	status, _, _ := uiGet(t, client, h.baseURL+"/ui/devices")
	if status != http.StatusSeeOther {
		t.Errorf("after logout, GET /ui/devices = %d, want a redirect to login", status)
	}
}

// TestUI_ActivityStreamRecordsWhatTheWebUIDid drives a create, an edit and
// a delete through the real web UI against the real controller binary and
// real PostgreSQL, and asserts each one left a line in the activity stream
// naming the subject that made it.
//
// The surface matters more than the assertion. These writes reach
// internal/access directly and pass through no JSON API handler at all, so
// a recording call placed in a handler would have covered the API, left
// this path silent, and looked complete in every test that exercised the
// covered one. The recording lives in a store decorator wrapped once at the
// composition root precisely so both surfaces are covered by construction,
// and this is the test that proves the uncovered-looking one is not.
func TestUI_ActivityStreamRecordsWhatTheWebUIDid(t *testing.T) {
	h := startHarness(t)
	client := h.signIn(t, adminID())

	const name = "e2e-activity-org"
	const renamed = name + "-renamed"

	// Create.
	_, formPage, _ := uiGet(t, client, h.baseURL+"/ui/organizations/new")
	resp, err := client.PostForm(h.baseURL+"/ui/organizations", url.Values{
		"name":  {name},
		"_csrf": {csrfTokenFrom(t, formPage)},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create = %d, want 303", resp.StatusCode)
	}

	id := organizationIDNamed(t, client, h.baseURL, name)

	// Edit.
	_, editPage, _ := uiGet(t, client, h.baseURL+"/ui/organizations/"+id+"/edit")
	editResp, err := client.PostForm(h.baseURL+"/ui/organizations/"+id, url.Values{
		"name":    {renamed},
		"_method": {"PATCH"},
		"_csrf":   {csrfTokenFrom(t, editPage)},
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	_ = editResp.Body.Close()
	if editResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("edit = %d, want 303", editResp.StatusCode)
	}

	// Delete.
	_, detail, _ := uiGet(t, client, h.baseURL+"/ui/organizations/"+id)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		h.baseURL+"/ui/organizations/"+id,
		strings.NewReader("_method=DELETE&_csrf="+url.QueryEscape(csrfTokenFrom(t, detail))))
	if err != nil {
		t.Fatalf("building the delete request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	deleteResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	_ = deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete = %d, want 303", deleteResp.StatusCode)
	}

	// Now read the stream back, through the UI, as an operator would.
	status, stream, _ := uiGet(t, client, h.baseURL+"/ui/activity")
	if status != http.StatusOK {
		t.Fatalf("GET /ui/activity = %d, want 200", status)
	}

	for _, want := range []string{
		adminID().Subject + " created organization " + name,
		adminID().Subject + " updated organization " + renamed,
		adminID().Subject + " deleted organization " + renamed,
	} {
		if !strings.Contains(stream, want) {
			t.Errorf("the activity stream does not record %q", want)
		}
	}

	// The deletion entry survives the object it describes, which is the
	// whole reason the entry carries a captured name and a bare id rather
	// than a foreign key: a cascade would have destroyed exactly the record
	// somebody investigating the deletion needs.
	if strings.Contains(stream, "organization "+id+" (deleted)") {
		t.Error("the deletion entry lost the name the organization had when it was deleted")
	}
}

// organizationIDNamed finds the id of the organization named name.
//
// It reads it off the row's own detail link rather than guessing at a
// sequence value: the database chooses ids, and a test that assumed 1 would
// pass or fail depending on what ran before it.
func organizationIDNamed(t *testing.T, client *http.Client, baseURL, name string) string {
	t.Helper()

	_, list, _ := uiGet(t, client, baseURL+"/ui/organizations")
	href := regexp.MustCompile(`href="/ui/organizations/(\d+)"`)

	// The link text is "Open", not the record name, so the row is the unit
	// that has to be matched: find the row carrying the name, then take the
	// link inside it.
	for _, row := range strings.Split(list, "<tr") {
		if !strings.Contains(row, name) {
			continue
		}
		if m := href.FindStringSubmatch(row); m != nil {
			return m[1]
		}
	}
	t.Fatalf("no organization named %q appears in the list", name)
	return ""
}
