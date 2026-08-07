package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth/authtest"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// This file is Phase 13's Release Gate: "A read-only API user receives a
// device JSON object, but the _links array actively omits the delete URL."
//
// Every component below is the real one, because the gate's own recorded
// history is that it passed vacuously against a mock. The router is
// api.NewRouter, the authentication is api.AuthMiddleware over a real
// jwtEvaluator, the tokens are real HS256 tokens minted by
// internal/auth/authtest, the authorization is a real auth.AdmissionChain,
// the generator is auth.NewAdmissionHATEOASGenerator over that same chain,
// and the repository is a real entRepository over SQLite. There is no
// stub in any load-bearing position, per RULE 0.
//
// The gate is asserted in both directions. "Omits the delete URL" is only
// meaningful alongside a caller who does see it and a self link the
// read-only caller does see, or the assertion is satisfied by a generator
// that omits everything, which is exactly how it passed before.

const gateDeviceName = "edge-01"

// countingRecorder counts admission decisions so a test can prove how many
// were recorded, which is the observable difference between the enforcing
// path and the advertising one.
type countingRecorder struct {
	decisions int
}

func (c *countingRecorder) Record(_ context.Context, _ auth.Decision) { c.decisions++ }

// gateFixture is everything one Release Gate scenario needs.
type gateFixture struct {
	router   http.Handler
	issuer   *authtest.Issuer
	recorder *countingRecorder
	repo     inventory.Repository
}

// gateFixtureSeq makes each fixture's in-memory database name unique.
//
// "cache=shared" means two connections opened with the same name share one
// database, so deriving the name from t.Name() alone would make a second
// fixture inside one test reuse the first's data and fail seeding on the
// device's unique name. Tests below deliberately build more than one
// fixture per test, to keep a mutating probe from changing what a later
// probe observes, so the name has to be unique per call rather than per
// test.
var gateFixtureSeq atomic.Int64

// newGateFixture builds the real request pipeline end to end and seeds one
// real device.
func newGateFixture(t *testing.T) *gateFixture {
	t.Helper()

	dsn := fmt.Sprintf("file:gate-%d?mode=memory&cache=shared&_fk=1", gateFixtureSeq.Add(1))
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })

	client.Device.Create().
		SetName(gateDeviceName).
		SetType("linux_server").
		SetProperties(map[string]interface{}{"host": "10.0.0.5"}).
		SetState("active").
		SaveX(context.Background())

	repo := inventory.NewEntRepository(client, inventory.NewItemFactory())
	issuer := authtest.New(t, "gate-issuer", "gate-audience")

	// One chain, two consumers: exactly what cmd/controller wires. The
	// enforcing path records; the advertising path does not.
	chain := auth.AdmissionChain{auth.NewTokenScopeRule(issuer.Evaluator())}
	recorder := &countingRecorder{}
	admission := auth.Admission{Chain: chain, Recorder: recorder}

	generator, err := auth.NewAdmissionHATEOASGenerator(chain)
	if err != nil {
		t.Fatalf("building generator: %v", err)
	}

	devices := api.NewDeviceHandler(repo, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      api.AuthMiddleware(issuer.Evaluator()),
		Admission: admission,
		HATEOAS:   generator,
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/inventory/devices/{name}", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: devices.Get},
			{Method: http.MethodDelete, Pattern: "/inventory/devices/{name}", Scope: auth.ScopeInventoryWrite, Rel: auth.RelDelete, Handler: devices.Delete},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	return &gateFixture{router: router, issuer: issuer, recorder: recorder, repo: repo}
}

// do issues one authenticated request, or an anonymous one when id is nil.
func (f *gateFixture) do(t *testing.T, method, target string, id *auth.Identity) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if id != nil {
		req.Header.Set("Authorization", f.issuer.BearerToken(t, id))
	}
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

// deviceBody is the wire shape a device response carries, decoded exactly
// as a real client would decode it rather than through the server's own
// unexported type.
type deviceBody struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	Links *[]struct {
		Rel    string `json:"rel"`
		Href   string `json:"href"`
		Method string `json:"method"`
	} `json:"_links"`
}

// rels returns the relation names present in a decoded response body.
func (b deviceBody) rels() []string {
	if b.Links == nil {
		return nil
	}
	out := make([]string, 0, len(*b.Links))
	for _, l := range *b.Links {
		out = append(out, l.Rel)
	}
	return out
}

// hrefFor returns the href a given relation points at.
func (b deviceBody) hrefFor(rel string) (string, string, bool) {
	if b.Links == nil {
		return "", "", false
	}
	for _, l := range *b.Links {
		if l.Rel == rel {
			return l.Href, l.Method, true
		}
	}
	return "", "", false
}

func decodeDevice(t *testing.T, rr *httptest.ResponseRecorder) deviceBody {
	t.Helper()
	var body deviceBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding device body %q: %v", rr.Body.String(), err)
	}
	return body
}

var (
	readOnlyIdentity  = &auth.Identity{Subject: "viewer@example.com", Role: auth.RoleViewer, Scopes: []auth.Scope{auth.ScopeInventoryRead}}
	readWriteIdentity = &auth.Identity{Subject: "operator@example.com", Role: auth.RoleOperator, Scopes: []auth.Scope{auth.ScopeInventoryRead, auth.ScopeInventoryWrite}}
	adminIdentity     = &auth.Identity{Subject: "root@example.com", Role: auth.RoleAdmin}
	unrelatedID       = &auth.Identity{Subject: "runner@example.com", Role: auth.RoleOperator, Scopes: []auth.Scope{auth.ScopeRunbookExecute}}
)

// TestHATEOAS_ReleaseGate is the gate itself.
func TestHATEOAS_ReleaseGate(t *testing.T) {
	f := newGateFixture(t)
	target := api.APIVersionPrefix + "/inventory/devices/" + gateDeviceName

	// The gate's own sentence: a read-only user receives a device JSON
	// object, and the _links array omits the delete URL.
	rr := f.do(t, http.MethodGet, target, readOnlyIdentity)
	if rr.Code != http.StatusOK {
		t.Fatalf("read-only GET returned %d, want 200: body %s", rr.Code, rr.Body.String())
	}
	body := decodeDevice(t, rr)

	if body.Name != gateDeviceName {
		t.Errorf("device name is %q, want %q: the caller did not receive a device object", body.Name, gateDeviceName)
	}
	if _, _, ok := body.hrefFor("delete"); ok {
		t.Errorf("read-only caller was offered a delete link: %v", body.rels())
	}

	// The half that stops the assertion above passing vacuously. Before
	// this phase the array omitted the delete URL because it omitted every
	// URL, which the roadmap itself recorded as the reason the gate was
	// unchecked.
	if body.Links == nil {
		t.Fatal("_links is absent entirely, so 'omits the delete URL' asserts nothing")
	}
	if _, _, ok := body.hrefFor("self"); !ok {
		t.Errorf("read-only caller received no self link, so the omission of delete is not meaningful: %v", body.rels())
	}

	// The omission has to be the truth about enforcement, not a hint.
	if got := f.do(t, http.MethodDelete, target, readOnlyIdentity); got.Code != http.StatusForbidden {
		t.Errorf("read-only DELETE returned %d, want 403: the missing link must reflect a real refusal", got.Code)
	}
}

func TestHATEOAS_WriteScopedCallerIsOfferedDeleteAndCanFollowIt(t *testing.T) {
	f := newGateFixture(t)
	target := api.APIVersionPrefix + "/inventory/devices/" + gateDeviceName

	body := decodeDevice(t, f.do(t, http.MethodGet, target, readWriteIdentity))
	href, method, ok := body.hrefFor("delete")
	if !ok {
		t.Fatalf("write-scoped caller was offered no delete link: %v", body.rels())
	}
	if method != http.MethodDelete {
		t.Errorf("delete link declares method %q, want %q", method, http.MethodDelete)
	}

	// Following the advertised link verbatim is what makes it an
	// affordance rather than a decoration. A link nothing can act on is
	// the "port with no callers" the Adversarial gate rejects.
	if got := f.do(t, method, href, readWriteIdentity); got.Code != http.StatusNoContent {
		t.Fatalf("following the advertised delete link returned %d, want 204: body %s", got.Code, got.Body.String())
	}

	// Retirement, not removal: the resource still answers, and now says
	// so. This is the state half of "hypermedia as the engine of
	// application state": the same caller, the same scopes, a different
	// resource state, and the affordance is gone because the action no
	// longer applies.
	after := decodeDevice(t, f.do(t, http.MethodGet, target, readWriteIdentity))
	if after.State != "archived" {
		t.Errorf("device state after delete is %q, want %q", after.State, "archived")
	}
	if _, _, ok := after.hrefFor("delete"); ok {
		t.Errorf("an already-archived device still offers a delete link: %v", after.rels())
	}
	if _, _, ok := after.hrefFor("self"); !ok {
		t.Errorf("an archived device offers no self link: %v", after.rels())
	}
}

func TestHATEOAS_AdminWithNoExplicitScopesSeesEveryLink(t *testing.T) {
	// This is the vacuous-pass killer named in Phase 13's Adversarial
	// gate. auth.Identity.HasScope grants RoleAdmin everything regardless
	// of the scopes claim, so a generator that read Identity.Scopes
	// directly would return an empty set for the single most privileged
	// caller and the Release Gate would still pass.
	f := newGateFixture(t)
	target := api.APIVersionPrefix + "/inventory/devices/" + gateDeviceName

	body := decodeDevice(t, f.do(t, http.MethodGet, target, adminIdentity))
	for _, want := range []string{"self", "delete"} {
		if _, _, ok := body.hrefFor(want); !ok {
			t.Errorf("admin holding no explicit scopes was not offered the %q link: %v", want, body.rels())
		}
	}
}

func TestHATEOAS_LinksAgreeWithEnforcement(t *testing.T) {
	// The invariant one shared AdmissionChain exists to guarantee: for
	// every identity and every advertised affordance, the link is present
	// if and only if a real request to that method and href is not
	// refused. Anything else means the API is telling clients one thing
	// and doing another.
	for _, id := range []*auth.Identity{readOnlyIdentity, readWriteIdentity, adminIdentity, unrelatedID} {
		t.Run(id.Subject, func(t *testing.T) {
			f := newGateFixture(t)
			target := api.APIVersionPrefix + "/inventory/devices/" + gateDeviceName

			rr := f.do(t, http.MethodGet, target, id)
			if rr.Code == http.StatusForbidden {
				// A caller who cannot read the resource has no body to
				// carry links, which is consistent by construction.
				return
			}
			body := decodeDevice(t, rr)

			for _, probe := range []struct {
				rel    string
				method string
			}{
				{"self", http.MethodGet},
				{"delete", http.MethodDelete},
			} {
				_, _, advertised := body.hrefFor(probe.rel)

				// Use a fresh fixture per probe so a successful DELETE in
				// one probe cannot change what a later probe observes.
				probeFixture := newGateFixture(t)
				got := probeFixture.do(t, probe.method, target, id)
				permitted := got.Code != http.StatusForbidden

				if advertised != permitted {
					t.Errorf("relation %q: advertised=%v but a real %s returned %d (permitted=%v)",
						probe.rel, advertised, probe.method, got.Code, permitted)
				}
			}
		})
	}
}

func TestHATEOAS_AffordanceProbingIsNotAudited(t *testing.T) {
	// Computing which links to advertise probes every affordance a
	// resource declares. Routing that through the recorded Admission
	// would emit one audit line per candidate per request and log every
	// affordance a caller merely lacks as a denial at Warn, burying the
	// real denials under speculative ones nobody attempted.
	//
	// A read-only GET declares two affordances, so a naive implementation
	// would record three decisions: one enforcement plus two probes.
	f := newGateFixture(t)
	target := api.APIVersionPrefix + "/inventory/devices/" + gateDeviceName

	f.recorder.decisions = 0
	if rr := f.do(t, http.MethodGet, target, readOnlyIdentity); rr.Code != http.StatusOK {
		t.Fatalf("GET returned %d, want 200", rr.Code)
	}

	if f.recorder.decisions != 1 {
		t.Errorf("one request recorded %d admission decisions, want exactly 1 (the enforcement); affordance probing must not reach the audit trail", f.recorder.decisions)
	}
}

func TestHATEOAS_UnauthenticatedCallerGetsNoLinksAndNoBody(t *testing.T) {
	f := newGateFixture(t)
	target := api.APIVersionPrefix + "/inventory/devices/" + gateDeviceName

	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodOptions} {
		rr := f.do(t, method, target, nil)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s returned %d, want 401", method, rr.Code)
		}
		if strings.Contains(rr.Body.String(), "_links") {
			t.Errorf("anonymous %s response carries a _links array: %s", method, rr.Body.String())
		}
	}
}
