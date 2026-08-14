package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/routing"
	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// The bind-time half of the injector refusal.
//
// The run-time half lives in internal/adapters/native and fires on any
// payload that reaches it, whatever route it took. What this one buys is
// timing: the operator learns while they are still looking at the binding
// form and can fix it in a second, rather than on the first job they launch
// afterwards.

// bindingStore is a Store double that records what was bound, so a test can
// prove a refusal happened BEFORE the write rather than after it.
//
// The distinction matters: a check that refuses after writing has already
// created the binding it says it refused, and the next launch honours it.
type bindingStore struct {
	credstore.Store

	credentials map[int]credstore.Credential
	types       map[int]credstore.CredentialType
	bound       []int
	wrote       bool
}

func (s *bindingStore) GetCredential(_ context.Context, id int) (credstore.Credential, error) {
	c, ok := s.credentials[id]
	if !ok {
		return credstore.Credential{}, credstore.ErrNotFound
	}
	return c, nil
}

func (s *bindingStore) GetType(_ context.Context, id int) (credstore.CredentialType, error) {
	ct, ok := s.types[id]
	if !ok {
		return credstore.CredentialType{}, credstore.ErrNotFound
	}
	return ct, nil
}

func (s *bindingStore) SetTemplateCredentials(_ context.Context, _ int, ids []int) error {
	s.wrote = true
	s.bound = ids
	return nil
}

func (s *bindingStore) TemplateCredentials(context.Context, int) ([]credstore.Credential, error) {
	out := make([]credstore.Credential, 0, len(s.bound))
	for _, id := range s.bound {
		out = append(out, s.credentials[id])
	}
	return out, nil
}

// envInjectingType is a credential type the native path cannot honour: an
// aws credential injecting the two variables amazon.aws's own modules read
// and nothing else looks at.
func envInjectingType() credstore.CredentialType {
	return credstore.CredentialType{
		ID: 4,
		CredentialType: credtype.CredentialType{
			Name: "Amazon Web Services", Kind: credtype.KindCloud, Namespace: "aws",
			Inputs: credtype.InputSchema{Fields: []credtype.InputField{
				{ID: "access_key", Label: "Access Key"},
				{ID: "secret_key", Label: "Secret Key", Secret: true},
			}},
			Injectors: credtype.Injectors{Env: map[string]string{
				"AWS_ACCESS_KEY_ID":     "{{ access_key }}",
				"AWS_SECRET_ACCESS_KEY": "{{ secret_key }}",
			}},
		},
	}
}

// varInjectingType is a credential type both paths can honour.
func varInjectingType() credstore.CredentialType {
	return credstore.CredentialType{
		ID: 5,
		CredentialType: credtype.CredentialType{
			Name: "Custom REST API Token", Kind: credtype.KindCloud, Namespace: "custom_api_token",
			Inputs: credtype.InputSchema{Fields: []credtype.InputField{
				{ID: "api_token", Label: "Token", Secret: true},
			}},
			Injectors: credtype.Injectors{ExtraVars: map[string]any{"ansible_api_token": "{{ api_token }}"}},
		},
	}
}

// newBindingFixture builds a handler over a template of the given kind.
func newBindingFixture(t *testing.T, kindName string) (*api.CredentialHandler, *bindingStore) {
	t.Helper()

	store := &bindingStore{
		credentials: map[int]credstore.Credential{
			18: {ID: 18, Name: "prod aws", TypeID: 4},
			19: {ID: 19, Name: "prod api", TypeID: 5},
		},
		types: map[int]credstore.CredentialType{4: envInjectingType(), 5: varInjectingType()},
	}

	tmpl := launch.Template{ID: 12, Name: "patch the edge routers", KindName: kindName, Definition: "pb-1", InventoryID: 7}
	handler := api.NewCredentialHandler(store, render.New(), api.WithBindingTemplates(stubTemplates{tmpl: tmpl}))
	return handler, store
}

// bindCredentials issues a real PUT /templates/{id}/credentials through a
// real chi router, so the path parameter is parsed by the same mux
// production uses rather than injected into a request context by hand.
func bindCredentials(t *testing.T, handler *api.CredentialHandler, templateID int, ids []int) (int, []byte) {
	t.Helper()

	body, err := json.Marshal(map[string]any{"credentials": ids})
	if err != nil {
		t.Fatalf("failed to encode the binding request: %v", err)
	}

	router := chi.NewRouter()
	router.Put("/templates/{id}/credentials", handler.SetTemplateCredentials)

	req := httptest.NewRequest(http.MethodPut, "/templates/"+strconv.Itoa(templateID)+"/credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	return rec.Code, rec.Body.Bytes()
}

// TestBindingRefusesAnInjectorTheTemplatesPathCannotHonour is the headline
// case, and the second assertion is what makes it meaningful: nothing was
// written.
func TestBindingRefusesAnInjectorTheTemplatesPathCannotHonour(t *testing.T) {
	handler, store := newBindingFixture(t, "runbook")

	status, body := bindCredentials(t, handler, 12, []int{18})
	if status != 409 {
		t.Fatalf("status = %d, want 409: the request is well formed and both records exist; the PAIR is what conflicts", status)
	}
	if store.wrote {
		t.Error("the binding was written before being refused, so the next launch would honour it")
	}
	for _, want := range []string{"Amazon Web Services", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the refusal does not name %q: %s", want, body)
		}
	}
}

// TestBindingAcceptsWhatEachPathCanHonour covers the three legal
// combinations, because a check that refused everything would pass the test
// above.
func TestBindingAcceptsWhatEachPathCanHonour(t *testing.T) {
	tests := []struct {
		name         string
		kind         string
		credentialID int
	}{
		{name: "the legacy path accepts an env-injecting type", kind: "playbook", credentialID: 18},
		{name: "the native path accepts an extra-vars-only type", kind: "runbook", credentialID: 19},
		{name: "the legacy path accepts an extra-vars-only type too", kind: "playbook", credentialID: 19},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, store := newBindingFixture(t, tt.kind)

			status, body := bindCredentials(t, handler, 12, []int{tt.credentialID})
			if status != 200 {
				t.Fatalf("status = %d, want 200: %s", status, body)
			}
			if !store.wrote {
				t.Error("the binding was accepted but never written")
			}
		})
	}
}

// TestBindingWithNoTemplateReaderStillWrites covers the degradation, which
// has to be safe rather than merely permitted.
//
// A handler with no template reader skips the check entirely and the
// run-time backstop in internal/adapters/native is what refuses. That is
// what makes the option optional: its absence changes when the refusal
// arrives, never whether it does.
func TestBindingWithNoTemplateReaderStillWrites(t *testing.T) {
	store := &bindingStore{
		credentials: map[int]credstore.Credential{18: {ID: 18, Name: "prod aws", TypeID: 4}},
		types:       map[int]credstore.CredentialType{4: envInjectingType()},
	}
	handler := api.NewCredentialHandler(store, render.New())

	status, body := bindCredentials(t, handler, 12, []int{18})
	if status != 200 {
		t.Fatalf("status = %d, want 200 with no template reader wired: %s", status, body)
	}
	if !store.wrote {
		t.Error("the binding was not written")
	}
}

// TestUnbindingIsNeverRefused covers the empty-list case, which is how an
// operator gets OUT of a bad binding. Refusing it would make a template
// bound to an unsupported credential unfixable through the API.
func TestUnbindingIsNeverRefused(t *testing.T) {
	handler, store := newBindingFixture(t, "runbook")

	status, body := bindCredentials(t, handler, 12, []int{})
	if status != 200 {
		t.Fatalf("status = %d, want 200: unbinding everything must always be possible: %s", status, body)
	}
	if !store.wrote {
		t.Error("the unbinding was not written")
	}
}

// TestTheBindTimeAndRunTimeRefusalsAreOneRule holds the two halves against
// each other.
//
// They share an error and a message by construction (both read
// internal/adapters/routing), and this asserts that rather than leaving it
// to a reader comparing two files.
func TestTheBindTimeAndRunTimeRefusalsAreOneRule(t *testing.T) {
	err := routing.CheckInjectable(routing.AdapterNative, "Amazon Web Services",
		[]string{"AWS_ACCESS_KEY_ID"}, nil)
	if err == nil {
		t.Fatal("CheckInjectable accepted an env injector on the native path")
	}
	if !errors.Is(err, routing.ErrUnsupportedInjection) {
		t.Errorf("error = %v, want one matching ErrUnsupportedInjection", err)
	}

	handler, _ := newBindingFixture(t, "runbook")
	_, body := bindCredentials(t, handler, 12, []int{18})
	if !strings.Contains(string(body), routing.UnsupportedInjectionMessage) {
		t.Errorf("the bind-time refusal does not carry the shared explanation: %s", body)
	}
}

// TestBindingSkipsTheCheckWhenItCannotApply covers the three ways the
// bind-time check declines to have an opinion, each of which has to be
// permissive rather than refusing.
//
// A check that refused when it could not evaluate would break bindings for
// reasons unrelated to what the operator did, and the run-time backstop in
// internal/adapters/native guarantees the rule regardless.
func TestBindingSkipsTheCheckWhenItCannotApply(t *testing.T) {
	tests := []struct {
		name      string
		templates api.TemplateReader
	}{
		{
			name: "the template no longer exists",
			// The store's own write below produces the right error for
			// that, and reporting it here too would give one condition two
			// different responses depending on whether the option was wired.
			templates: stubTemplates{err: launch.ErrNotFound},
		},
		{
			name: "the template's kind is no longer registered",
			// The launch path already refuses such a template with a
			// message about the kind, which is the useful error.
			templates: stubTemplates{tmpl: launch.Template{ID: 12, Name: "orphan", KindName: "a-kind-this-build-forgot"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &bindingStore{
				credentials: map[int]credstore.Credential{18: {ID: 18, Name: "prod aws", TypeID: 4}},
				types:       map[int]credstore.CredentialType{4: envInjectingType()},
			}
			handler := api.NewCredentialHandler(store, render.New(), api.WithBindingTemplates(tt.templates))

			status, body := bindCredentials(t, handler, 12, []int{18})
			if status != 200 {
				t.Fatalf("status = %d, want 200: an inapplicable check must not refuse. Body: %s", status, body)
			}
			if !store.wrote {
				t.Error("the binding was not written")
			}
		})
	}
}

// TestBindingReportsAStoreFailure covers the error paths through the check,
// which must not degrade into permitting the binding: a store that cannot
// answer is not the same fact as a credential the path can honour.
func TestBindingReportsAStoreFailure(t *testing.T) {
	tests := []struct {
		name  string
		store *bindingStore
	}{
		{
			name: "the credential is gone",
			store: &bindingStore{
				credentials: map[int]credstore.Credential{},
				types:       map[int]credstore.CredentialType{4: envInjectingType()},
			},
		},
		{
			name: "the credential's type is gone",
			store: &bindingStore{
				credentials: map[int]credstore.Credential{18: {ID: 18, Name: "prod aws", TypeID: 99}},
				types:       map[int]credstore.CredentialType{4: envInjectingType()},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl := launch.Template{ID: 12, Name: "native", KindName: "runbook", Definition: "pb-1", InventoryID: 7}
			handler := api.NewCredentialHandler(tt.store, render.New(),
				api.WithBindingTemplates(stubTemplates{tmpl: tmpl}))

			status, body := bindCredentials(t, handler, 12, []int{18})
			if status == 200 {
				t.Fatalf("the binding was accepted despite an unreadable record. Body: %s", body)
			}
			if tt.store.wrote {
				t.Error("the binding was written despite the check failing")
			}
		})
	}
}
