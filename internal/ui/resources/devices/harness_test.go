// Package devices_test boots the real web UI with the devices view over a
// real device repository, for the tests that drive its edit form.
package devices_test

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/devices"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/web"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"
)

// The harness boots the real UI handler over a real device repository.
//
// Real router, templates, session store (ent, the one the Controller uses),
// CSRF, admission chain and HATEOAS generator, over the ent repository the
// Controller composes or the Crawl tier's file repository. Nothing on the
// write path is a fake: the package-wide conformance suite's fake repository
// used to answer Save with nil and keep nothing, which is how an edit that
// wrote nothing passed every test there (FAILURE_PATTERNS 1044).

// registerOnce guards the process-wide view registry, which refuses a second
// registration of one name. This test binary registers the devices view
// exactly once, over currentRepo.
var registerOnce sync.Once

// currentRepo is the repository the registered view writes through. The
// registry is process-wide, so each test points it at its own store rather
// than registering again. It only forwards: every call reaches the real
// repository unchanged.
var currentRepo = &swappableRepo{}

// swappableRepo forwards every inventory.Repository call to the repository a
// test installed.
type swappableRepo struct {
	mu    sync.Mutex
	inner inventory.Repository
}

// repo returns the installed repository.
func (s *swappableRepo) repo() inventory.Repository {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner
}

// set installs repo for the rest of the calling test.
func (s *swappableRepo) set(repo inventory.Repository) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inner = repo
}

func (s *swappableRepo) GetGroup(ctx context.Context, sel pkginventory.Selector) (inventory.Iterator, error) {
	return s.repo().GetGroup(ctx, sel)
}

func (s *swappableRepo) GetByName(ctx context.Context, name string) (pkginventory.InventoryItem, error) {
	return s.repo().GetByName(ctx, name)
}

func (s *swappableRepo) Create(ctx context.Context, item pkginventory.InventoryItem) error {
	return s.repo().Create(ctx, item)
}

func (s *swappableRepo) Save(ctx context.Context, item pkginventory.InventoryItem) error {
	return s.repo().Save(ctx, item)
}

func (s *swappableRepo) Retire(ctx context.Context, name string) error {
	return s.repo().Retire(ctx, name)
}

func (s *swappableRepo) GroupAncestry(ctx context.Context, name string) ([]inventory.HierarchyLayer, error) {
	return s.repo().GroupAncestry(ctx, name)
}

// adminIdentity holds the two scopes the devices view's routes need.
var adminIdentity = &auth.Identity{
	Subject: "devices-admin",
	Role:    auth.RoleAdmin,
	Scopes:  []auth.Scope{auth.ScopeInventoryRead, auth.ScopeInventoryWrite},
}

// harness is a booted UI plus the session that authenticates against it.
type harness struct {
	handler  http.Handler
	sessions session.Store
	cookie   session.CookieCodec
	token    string
}

// newHarness boots the UI with the devices view over repo.
func newHarness(t *testing.T, repo inventory.Repository) *harness {
	t.Helper()
	registerOnce.Do(func() {
		if err := devices.Register(currentRepo, inventory.NewItemFactory()); err != nil {
			t.Fatalf("devices.Register() = %v, want nil", err)
		}
	})
	currentRepo.set(repo)
	t.Cleanup(func() { currentRepo.set(nil) })

	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:sessions-%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })
	sessions := session.NewEntStore(client)
	token, err := sessions.Create(context.Background(), adminIdentity, time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("session Create() = %v, want nil", err)
	}

	issuer := authtest.New(t, "pleiades-devices", "pleiades")
	chain := auth.AdmissionChain{auth.NewTokenScopeRule(issuer.Evaluator())}
	generator, err := auth.NewAdmissionHATEOASGenerator(chain)
	if err != nil {
		t.Fatalf("NewAdmissionHATEOASGenerator() = %v, want nil", err)
	}
	cookie := session.CookieCodec{}
	h := web.New(web.Config{
		Prefix:    "/ui",
		Version:   "devices-test",
		Sessions:  sessions,
		Cookie:    cookie,
		HATEOAS:   generator,
		Admission: auth.Admission{Chain: chain},
	})
	// Mounted at the prefix, as the composition root mounts it.
	root := chi.NewRouter()
	root.Mount("/ui", h.Routes())
	return &harness{handler: root, sessions: sessions, cookie: cookie, token: token}
}

// newEntRepo opens a fresh in-memory database and the ent repository the
// Controller composes over it.
func newEntRepo(t *testing.T) inventory.Repository {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:devices-%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })
	return inventory.NewEntRepository(client, inventory.NewItemFactory())
}

// newFileRepo writes an empty static inventory and returns the Crawl tier's
// file repository over it, with the inventory file's path.
func newFileRepo(t *testing.T) (inventory.Repository, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), inventory.DefaultInventoryFilename)
	if err := os.WriteFile(path, []byte("hosts: []\n"), 0o600); err != nil {
		t.Fatalf("writing the inventory: %v", err)
	}
	return inventory.NewFileRepository(path, inventory.NewItemFactory()), path
}

// authenticate attaches the harness session's cookie to r.
func (h *harness) authenticate(r *http.Request) {
	w := httptest.NewRecorder()
	h.cookie.Write(w, h.token)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}
}

// get issues an authenticated GET.
func (h *harness) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	h.authenticate(r)
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, r)
	return w
}

// post issues an authenticated POST carrying a valid CSRF token.
func (h *harness) post(t *testing.T, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	sess, err := h.sessions.Resolve(context.Background(), h.token)
	if err != nil {
		t.Fatalf("resolving the harness session: %v", err)
	}
	form.Set("_csrf", session.CSRFToken(sess.CSRFKey, h.token))
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.authenticate(r)
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, r)
	return w
}

// inputPattern matches one rendered text input, whichever order its name and
// value attributes come in.
var inputPattern = regexp.MustCompile(`<input\b[^>]*>`)

// attrPattern reads one attribute off a matched tag.
var attrPattern = regexp.MustCompile(`\b(name|value|type)="([^"]*)"`)

// formFields returns what a browser would submit from a rendered form's text
// inputs: every named input with its prefilled value, the CSRF token left
// out because post sets its own.
//
// A test that wrote the submission by hand would test the submission it
// imagined. This one submits what the edit form actually offers, so a form
// that renders a control the write then ignores is caught by the same test.
func formFields(t *testing.T, body string) url.Values {
	t.Helper()
	out := url.Values{}
	for _, tag := range inputPattern.FindAllString(body, -1) {
		attrs := map[string]string{}
		for _, m := range attrPattern.FindAllStringSubmatch(tag, -1) {
			attrs[m[1]] = html.UnescapeString(m[2])
		}
		name := attrs["name"]
		if name == "" || name == "_csrf" || name == "_method" {
			continue
		}
		// A browser submits a hidden input as it submits a text one; a
		// submit button and an unchecked checkbox carry nothing here.
		if kind := attrs["type"]; kind == "submit" || kind == "checkbox" {
			continue
		}
		out.Set(name, attrs["value"])
	}
	return out
}
