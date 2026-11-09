// Package api_test: the device handlers act on the name a request carries,
// through the real request pipeline: api.NewRouter, api.AuthMiddleware over a
// real JWT evaluator, a real admission chain and HATEOAS generator, and a real
// ent repository over SQLite. FAILURE_PATTERNS 1013: chi records a value
// still escaped whenever the request has a RawPath, so a handler that read
// chi.URLParam acted on the literal "sw%2F1" for a request naming "sw/1".
package api_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// addressedNames are the names each device test addresses. Each special
// character appears twice: alone, and beside a "/".
//
// The pairs are there because the two halves fail different broken
// handlers. A name holding a "/" (or a ",", which url.PathEscape escapes
// and Go's default path encoding does not) gives the request a RawPath, so
// chi records every value in it still escaped, and a handler that read
// chi.URLParam got the escaped text: those rows fail the old code. A name
// alone gives no RawPath, and chi's value is already decoded, so a handler
// that decoded every value regardless would read "50%" as a broken escape
// and "a%2Fb" as "a/b": those rows fail that fix.
var addressedNames = []string{
	"sw/1",
	"50%", "sw/50%",
	"a%2Fb", "sw/a%2Fb",
	"a b", "sw/a b",
	"café", "sw/café",
	"a,b",
}

// pathFixtureSeq makes each fixture's in-memory database its own.
var pathFixtureSeq atomic.Int64

// pathFixture is the real pipeline over one seeded database.
type pathFixture struct {
	router http.Handler
	issuer *authtest.Issuer
	repo   inventory.Repository
}

// newPathFixture seeds a device for every name in names, and a decoy named
// as that name's escaped form: the record a handler reading the value
// still escaped would act on instead.
func newPathFixture(t *testing.T, names []string) *pathFixture {
	t.Helper()

	dsn := fmt.Sprintf("file:pathparam-%d?mode=memory&cache=shared&_fk=1", pathFixtureSeq.Add(1))
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })

	seeded := map[string]bool{}
	for _, name := range names {
		for _, n := range []string{name, url.PathEscape(name)} {
			if seeded[n] {
				continue
			}
			seeded[n] = true
			client.Device.Create().
				SetName(n).
				SetType("linux_server").
				SetProperties(map[string]interface{}{"host": "10.0.0.5"}).
				SetState("active").
				SaveX(context.Background())
		}
	}

	repo := inventory.NewEntRepository(client, inventory.NewItemFactory())
	issuer := authtest.New(t, "pathparam-issuer", "pathparam-audience")
	chain := auth.AdmissionChain{auth.NewTokenScopeRule(issuer.Evaluator())}
	generator, err := auth.NewAdmissionHATEOASGenerator(chain)
	if err != nil {
		t.Fatalf("building generator: %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	devices := api.NewDeviceHandler(repo, inventory.NewItemFactory(), logger)

	router, err := api.NewRouter(api.RouterConfig{
		Logger:    logger,
		Auth:      api.AuthMiddleware(issuer.Evaluator()),
		Admission: auth.Admission{Chain: chain},
		HATEOAS:   generator,
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/inventory/devices/{name}", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: devices.Get},
			{Method: http.MethodPatch, Pattern: "/inventory/devices/{name}", Scope: auth.ScopeInventoryWrite, Rel: auth.RelUpdate, Handler: devices.Update},
			{Method: http.MethodDelete, Pattern: "/inventory/devices/{name}", Scope: auth.ScopeInventoryWrite, Rel: auth.RelDelete, Handler: devices.Delete},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return &pathFixture{router: router, issuer: issuer, repo: repo}
}

// do sends one request as readWriteIdentity, with body as JSON when set.
func (f *pathFixture) do(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", f.issuer.BearerToken(t, readWriteIdentity))
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

// deviceTarget is the URL a client builds for name: one segment, escaped
// once, which is also the form the API's own links take.
func deviceTarget(name string) string {
	return api.APIVersionPrefix + "/inventory/devices/" + url.PathEscape(name)
}

// TestDeviceRoutes_FetchTheDeviceTheURLNames reads each name through its
// URL and through the self link the response carries, and both must answer
// with that device rather than its escaped-name decoy.
func TestDeviceRoutes_FetchTheDeviceTheURLNames(t *testing.T) {
	f := newPathFixture(t, addressedNames)
	for _, name := range addressedNames {
		t.Run(name, func(t *testing.T) {
			rr := f.do(t, http.MethodGet, deviceTarget(name), "")
			if rr.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200: body %s", deviceTarget(name), rr.Code, rr.Body.String())
			}
			body := decodeDevice(t, rr)
			if body.Name != name {
				t.Fatalf("GET %s served the device %q, want %q", deviceTarget(name), body.Name, name)
			}

			// The link the response offers names the same device the
			// handler served, which is what LESSONS 420 asks of an encode
			// and decode pair: the side that builds the address and the
			// side that resolves it read it the same way.
			self, _, ok := body.hrefFor(string(auth.RelSelf))
			if !ok {
				t.Fatalf("GET %s carried no self link: %s", deviceTarget(name), rr.Body.String())
			}
			again := f.do(t, http.MethodGet, self, "")
			if again.Code != http.StatusOK {
				t.Fatalf("following the self link %s = %d, want 200", self, again.Code)
			}
			if got := decodeDevice(t, again).Name; got != name {
				t.Fatalf("the self link %s served %q, want %q", self, got, name)
			}
		})
	}
}

// TestDeviceRoutes_UpdateTheDeviceTheURLNames patches each name's tags and
// reads both records back from the repository: the named device changed and
// its decoy did not.
func TestDeviceRoutes_UpdateTheDeviceTheURLNames(t *testing.T) {
	f := newPathFixture(t, addressedNames)
	for _, name := range addressedNames {
		t.Run(name, func(t *testing.T) {
			rr := f.do(t, http.MethodPatch, deviceTarget(name), `{"tags":["patched"]}`)
			if rr.Code != http.StatusOK {
				t.Fatalf("PATCH %s = %d, want 200: body %s", deviceTarget(name), rr.Code, rr.Body.String())
			}
			if got := decodeDevice(t, rr).Name; got != name {
				t.Fatalf("PATCH %s answered for %q, want %q", deviceTarget(name), got, name)
			}
			if !hasTag(t, f.repo, name, "patched") {
				t.Errorf("device %q does not carry the tag the PATCH sent", name)
			}
			if decoy := url.PathEscape(name); decoy != name && hasTag(t, f.repo, decoy, "patched") {
				t.Errorf("the PATCH for %q changed the device %q instead", name, decoy)
			}
		})
	}
}

// TestDeviceRoutes_DeleteTheDeviceTheURLNames retires each name and reads
// both records back: the named device is archived and its decoy is not.
func TestDeviceRoutes_DeleteTheDeviceTheURLNames(t *testing.T) {
	f := newPathFixture(t, addressedNames)
	for _, name := range addressedNames {
		t.Run(name, func(t *testing.T) {
			rr := f.do(t, http.MethodDelete, deviceTarget(name), "")
			if rr.Code != http.StatusNoContent {
				t.Fatalf("DELETE %s = %d, want 204: body %s", deviceTarget(name), rr.Code, rr.Body.String())
			}
			if got := stateOf(t, f.repo, name); got != pkginventory.StateArchived {
				t.Errorf("device %q is %s after its DELETE, want archived", name, got)
			}
			if decoy := url.PathEscape(name); decoy != name {
				if got := stateOf(t, f.repo, decoy); got != pkginventory.StateActive {
					t.Errorf("the DELETE for %q left the device %q %s, want it untouched", name, decoy, got)
				}
			}
		})
	}
}

// TestDeviceRoutes_AnEscapeThatDoesNotDecodeIs400 sends a RawPath holding a
// broken escape, which net/http itself never hands a handler, and expects a
// 400 that does not echo the value, never a 404 claiming no such device.
func TestDeviceRoutes_AnEscapeThatDoesNotDecodeIs400(t *testing.T) {
	f := newPathFixture(t, []string{"sw/1"})
	req := httptest.NewRequest(http.MethodGet, deviceTarget("sw/1"), nil)
	req.URL.RawPath = api.APIVersionPrefix + "/inventory/devices/sw%2F1%zz"
	req.Header.Set("Authorization", f.issuer.BearerToken(t, readWriteIdentity))
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("GET with an undecodable escape = %d, want 400: body %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "%zz") {
		t.Errorf("the 400 echoes the caller's value: %s", rr.Body.String())
	}
}

// TestRoutedHandlers_AnswerAnUndecodableEscapeWith400 holds every handler
// that reads a string parameter through pathParam to the same answer as the
// device routes: a RawPath whose escape does not decode is a 400, before the
// store is asked anything.
func TestRoutedHandlers_AnswerAnUndecodableEscapeWith400(t *testing.T) {
	schedules := scheduleRouter(t, newStubScheduleStore())
	runbooks := runbooksRouter(t, stubRunbookRepo{ids: []string{"alpha"}})
	for _, tc := range []struct {
		router  http.Handler
		method  string
		path    string
		rawPath string
	}{
		{schedules, http.MethodGet, "/schedules/a/b", "/schedules/a%2Fb%zz"},
		{schedules, http.MethodPatch, "/schedules/a/b", "/schedules/a%2Fb%zz"},
		{schedules, http.MethodDelete, "/schedules/a/b", "/schedules/a%2Fb%zz"},
		{schedules, http.MethodGet, "/schedules/a/b/occurrences", "/schedules/a%2Fb%zz/occurrences"},
		{runbooks, http.MethodGet, "/runbooks/a/b", "/runbooks/a%2Fb%zz"},
	} {
		t.Run(tc.method+" "+tc.rawPath, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, api.APIVersionPrefix+tc.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.URL.RawPath = api.APIVersionPrefix + tc.rawPath
			rr := httptest.NewRecorder()
			tc.router.ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("%s %s = %d, want 400: body %s", tc.method, tc.rawPath, rr.Code, rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), "%zz") {
				t.Errorf("the 400 echoes the caller's value: %s", rr.Body.String())
			}
		})
	}
}

// hasTag reports whether the stored device name carries tag.
func hasTag(t *testing.T, repo inventory.Repository, name, tag string) bool {
	t.Helper()
	item, err := repo.GetByName(context.Background(), name)
	if err != nil {
		t.Fatalf("reading device %q back: %v", name, err)
	}
	for _, have := range item.Tags() {
		if string(have) == tag {
			return true
		}
	}
	return false
}

// stateOf is the stored device name's lifecycle state.
func stateOf(t *testing.T, repo inventory.Repository, name string) pkginventory.LifecycleState {
	t.Helper()
	item, err := repo.GetByName(context.Background(), name)
	if err != nil {
		t.Fatalf("reading device %q back: %v", name, err)
	}
	return item.State()
}
