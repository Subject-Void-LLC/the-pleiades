package resources_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/web"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/go-chi/chi/v5"
)

// The conformance harness boots the real UI handler over fake ports.
//
// Real handler, real router, real templates, real session middleware, real
// CSRF, real admission chain and real HATEOAS generator. Only the storage
// ports are fakes, and only because a conformance suite that needed
// PostgreSQL and NATS to assert that a table has a header row would be a
// suite nobody runs. Everything a view actually depends on for its
// behaviour is the shipped code path, which is what RULE 0 asks for: the
// mocked layer here is never the layer under test.

// registerOnce guards the process-wide view registry. Registration panics
// on a duplicate name by design, so the suite registers exactly once and
// every test reads the same table the binary would.
var registerOnce sync.Once

func registerViews(t *testing.T) {
	t.Helper()
	registerOnce.Do(func() {
		repo := newFakeRepository()
		if err := resources.RegisterAll(resources.Deps{
			Inventory: repo,
			Factory:   inventory.NewItemFactory(),
			Jobs:      newFakeJobStore(),
			Runbooks:  fakeRunbookSource{},
			// A nil dispatcher is enough for every assertion here: the
			// Jobs view's create path is exercised for validation and
			// refusal, never for a successful launch, which is the
			// e2e suite's job against a real broker.
			Dispatcher: nil,
		}); err != nil {
			t.Fatalf("RegisterAll() = %v, want nil", err)
		}
	})
}

// identities the suite drives every view with. The pair is the point: an
// assertion that admin sees a delete button proves nothing unless the same
// assertion shows viewer does not.
var (
	adminIdentity = &auth.Identity{
		Subject: "conformance-admin",
		Role:    auth.RoleAdmin,
		Scopes:  []auth.Scope{auth.ScopeInventoryWrite, auth.ScopeInventoryRead, auth.ScopeJobRead, auth.ScopeRunbookRead, auth.ScopeRunbookExecute},
	}
	viewerIdentity = &auth.Identity{
		Subject: "conformance-viewer",
		Role:    auth.RoleViewer,
		Scopes:  []auth.Scope{auth.ScopeInventoryRead, auth.ScopeJobRead, auth.ScopeRunbookRead},
	}
)

// harness is a booted UI plus the cookie that authenticates against it.
type harness struct {
	handler http.Handler
	store   *fakeSessionStore
	cookie  session.CookieCodec
	token   string

	// generator and identity are kept so a test can ask the authorization
	// question directly and compare the answer against what the page
	// rendered. That comparison is only meaningful because it is the same
	// generator instance the handler used.
	generator auth.HATEOASGenerator
	identity  *auth.Identity
}

func newHarness(t *testing.T, id *auth.Identity) *harness {
	t.Helper()
	registerViews(t)

	// The real chain, with the real rule the controller mounts, over the
	// shipped test issuer's real evaluator. Faking the admission decision
	// would make every affordance assertion below a test of the fake.
	issuer := authtest.New(t, "pleiades-conformance", "pleiades")
	chain := auth.AdmissionChain{auth.NewTokenScopeRule(issuer.Evaluator())}
	generator, err := auth.NewAdmissionHATEOASGenerator(chain)
	if err != nil {
		t.Fatalf("NewAdmissionHATEOASGenerator() = %v, want nil", err)
	}

	store := newFakeSessionStore()
	token, err := store.Create(context.Background(), id, time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("session Create() = %v, want nil", err)
	}

	cookie := session.CookieCodec{Insecure: true}
	h := web.New(web.Config{
		Prefix:    "/ui",
		Version:   "conformance",
		Sessions:  store,
		Cookie:    cookie,
		HATEOAS:   generator,
		Admission: auth.Admission{Chain: chain},
	})

	// Mounted at the prefix rather than served at the root, because that
	// is what the composition root does: api.NewRouter mounts the UI
	// handler with chi.Mount, which strips the prefix before the sub-router
	// sees a path. A harness that served the sub-router directly would be
	// testing routes that do not exist at the URLs users visit.
	root := chi.NewRouter()
	root.Mount("/ui", h.Routes())

	return &harness{
		handler:   root,
		store:     store,
		cookie:    cookie,
		token:     token,
		generator: generator,
		identity:  id,
	}
}

// recorderFor serves one already-built request, for callers that need to
// construct the request themselves (the routing fuzzer builds paths net/http
// would reject through httptest.NewRequest, which panics rather than
// returning an error).
func recorderFor(h *harness, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, r)
	return w
}

// get issues an authenticated GET and returns the recorded response.
func (h *harness) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	h.authenticate(r)
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, r)
	return w
}

// post issues an authenticated POST carrying a valid CSRF token, so a test
// asserting on a handler's behaviour is not silently asserting on the CSRF
// middleware instead.
func (h *harness) post(t *testing.T, path string, form map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	values := make([]string, 0, len(form)+1)
	for k, v := range form {
		values = append(values, k+"="+v)
	}
	sess, err := h.store.Resolve(context.Background(), h.token)
	if err != nil {
		t.Fatalf("resolving the harness session: %v", err)
	}
	values = append(values, "_csrf="+session.CSRFToken(sess.CSRFKey, h.token))

	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(strings.Join(values, "&")))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.authenticate(r)

	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, r)
	return w
}

// postWithoutCSRF is the negative half of the pair above.
func (h *harness) postWithoutCSRF(t *testing.T, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.authenticate(r)

	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, r)
	return w
}

func (h *harness) authenticate(r *http.Request) {
	w := httptest.NewRecorder()
	h.cookie.Write(w, h.token)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}
}

// ---- fake ports ----

// fakeSessionStore is an in-memory session.Store. It is a fake rather than
// the real ent-backed store because what it holds is not what any of these
// assertions are about, and every one of them needs a live session first.
type fakeSessionStore struct {
	mu       sync.Mutex
	sessions map[string]session.Session
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{sessions: map[string]session.Session{}}
}

func (s *fakeSessionStore) Create(_ context.Context, id *auth.Identity, _, absolute time.Duration) (string, error) {
	token, err := session.NewToken()
	if err != nil {
		return "", err
	}
	key, err := session.NewCSRFKey()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	scopes := make([]string, 0, len(id.Scopes))
	for _, sc := range id.Scopes {
		scopes = append(scopes, string(sc))
	}
	s.sessions[token] = session.Session{
		Subject:           id.Subject,
		Role:              id.Role,
		Scopes:            scopes,
		CSRFKey:           key,
		IdleExpiresAt:     time.Now().Add(absolute),
		AbsoluteExpiresAt: time.Now().Add(absolute),
	}
	return token, nil
}

func (s *fakeSessionStore) Resolve(_ context.Context, token string) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return session.Session{}, session.ErrNotFound
	}
	return sess, nil
}

func (s *fakeSessionStore) Touch(context.Context, string, time.Duration) error { return nil }

func (s *fakeSessionStore) Delete(_ context.Context, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
	return nil
}

func (s *fakeSessionStore) DeleteExpired(context.Context, time.Time) (int, error) { return 0, nil }

// fakeJobStore serves a fixed set of jobs covering every state the
// dashboard buckets, so the chart's figures are checkable rather than
// merely present.
type fakeJobStore struct {
	dispatch.JobStore
	jobs []*dispatch.Job
}

func newFakeJobStore() *fakeJobStore {
	return &fakeJobStore{jobs: []*dispatch.Job{
		{JobID: "job-0001", RunbookID: "patch-tuesday", GroupName: "edge", State: "completed", DispatchedCount: 12, Actor: "someone", CreatedAt: time.Unix(1_700_000_000, 0)},
		{JobID: "job-0002", RunbookID: "patch-tuesday", GroupName: "core", State: "failed", FailedCount: 3, Actor: "someone", CreatedAt: time.Unix(1_700_000_100, 0)},
		{JobID: "job-0003", RunbookID: "audit", GroupName: "edge", State: "pending", Actor: "someone", CreatedAt: time.Unix(1_700_000_200, 0)},
	}}
}

func (s *fakeJobStore) List(_ context.Context, after string, limit int) ([]*dispatch.Job, error) {
	start := 0
	if after != "" {
		for i, j := range s.jobs {
			if j.JobID == after {
				start = i + 1
				break
			}
		}
	}
	end := min(start+limit, len(s.jobs))
	if start > len(s.jobs) {
		start = len(s.jobs)
	}
	return s.jobs[start:end], nil
}

func (s *fakeJobStore) Get(_ context.Context, jobID string) (*dispatch.Job, []dispatch.JobTask, error) {
	for _, j := range s.jobs {
		if j.JobID == jobID {
			return j, nil, nil
		}
	}
	return nil, nil, dispatch.ErrJobNotFound
}

// fakeRunbookSource serves two runbooks, one interruptible and one not, so
// both badge branches render somewhere in the suite.
type fakeRunbookSource struct{ runbook.Source }

func (fakeRunbookSource) List(context.Context) ([]string, error) {
	return []string{"audit", "patch-tuesday"}, nil
}

func (fakeRunbookSource) Get(_ context.Context, id string) (*runbook.Runbook, error) {
	switch id {
	case "audit":
		return &runbook.Runbook{ID: "audit", Interruptible: true}, nil
	case "patch-tuesday":
		return &runbook.Runbook{ID: "patch-tuesday", Interruptible: false}, nil
	default:
		return nil, runbook.ErrNotFound
	}
}

// fakeRepository is an in-memory inventory.Repository holding one device,
// which is enough for a list with a row, a detail page, and an edit form.
type fakeRepository struct {
	inventory.Repository
	mu    sync.Mutex
	items []pkginventory.InventoryItem
}

func newFakeRepository() *fakeRepository {
	factory := inventory.NewItemFactory()
	item, err := factory.Build(record.Record{
		ID:     "01890000-0000-7000-8000-000000000001",
		Name:   "core-router-01",
		Type:   "linux_server",
		Tags:   []pkginventory.Tag{"edge", "critical"},
		State:  pkginventory.StateActive,
		Source: pkginventory.SourceAuthority{Plugin: "conformance"},
	})
	if err != nil {
		panic("conformance fixture device did not build: " + err.Error())
	}
	return &fakeRepository{items: []pkginventory.InventoryItem{item}}
}

func (r *fakeRepository) GetGroup(_ context.Context, _ pkginventory.Selector) (inventory.Iterator, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return &sliceIterator{items: append([]pkginventory.InventoryItem(nil), r.items...)}, nil
}

func (r *fakeRepository) GetByName(_ context.Context, name string) (pkginventory.InventoryItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range r.items {
		if item.Name() == name {
			return item, nil
		}
	}
	return nil, inventory.ErrItemNotFound
}

func (r *fakeRepository) Create(_ context.Context, item pkginventory.InventoryItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, item)
	return nil
}

func (r *fakeRepository) Save(context.Context, pkginventory.InventoryItem) error { return nil }
func (r *fakeRepository) Retire(context.Context, string) error                   { return nil }

// sliceIterator adapts a slice to the repository's streaming iterator, so
// the view's own paging code runs unchanged against it.
type sliceIterator struct {
	items []pkginventory.InventoryItem
	idx   int
}

func (it *sliceIterator) Next(context.Context) bool {
	if it.idx >= len(it.items) {
		return false
	}
	it.idx++
	return true
}

func (it *sliceIterator) Item() pkginventory.InventoryItem { return it.items[it.idx-1] }
func (it *sliceIterator) Error() error                     { return nil }
func (it *sliceIterator) Close() error                     { return nil }
