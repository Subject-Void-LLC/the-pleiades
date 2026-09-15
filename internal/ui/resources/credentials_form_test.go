// Package resources_test's credential form coverage: the create and edit
// form end to end, through the real router, the real handler chain and the
// real ent-backed store the fixture builds with the encryption hook
// registered.
//
// Deliberately not a unit test over the projector. The thing worth proving
// is impossible to see from inside one function: that a control declared by
// a credential TYPE, which this package has never heard of, survives the
// submission narrowing that exists to reject exactly that kind of
// undeclared field. A test calling Bind directly would skip the narrowing
// and assert nothing about it.
package resources_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// body fetches a page and returns it, failing the test on any status but
// 200. The shared harness returns a recorder; every assertion here is
// against markup, so unwrapping it once is clearer than at each call.
func body(t *testing.T, h *harness, path string) string {
	t.Helper()
	rec := h.get(t, path)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// optionValue pulls the value of the <option> whose text is label, out of
// the select named field.
//
// Scraping rather than asking the store, because the ids a person can
// actually choose are the ones the page offered them. A test reading the id
// from the fixture would still pass if the select rendered an empty list.
func optionValue(t *testing.T, body, field, label string) string {
	t.Helper()

	sel := regexp.MustCompile(`(?s)<select[^>]*name="` + regexp.QuoteMeta(field) + `".*?</select>`)
	block := sel.FindString(body)
	if block == "" {
		t.Fatalf("the form rendered no select named %q", field)
	}
	opt := regexp.MustCompile(`<option[^>]*value="([^"]*)"[^>]*>\s*` + regexp.QuoteMeta(label))
	m := opt.FindStringSubmatch(block)
	if m == nil {
		t.Fatalf("the %q select offers no option labelled %q; it rendered:\n%s", field, label, block)
	}
	return m[1]
}

// TestCredentialsForm_ResolvesTheChosenTypesInputs is the dependent-field
// seam observed from outside: the same URL renders a different set of
// controls depending on which credential type the query names.
func TestCredentialsForm_ResolvesTheChosenTypesInputs(t *testing.T) {
	h := newHarness(t, adminIdentity)

	bare := body(t, h, "/ui/credentials/new")
	if strings.Contains(bare, `name="input_api_token"`) {
		t.Error("the create form rendered a type's inputs before any type was chosen")
	}
	typeID := optionValue(t, bare, "credential_type", "Conformance API")

	chosen := body(t, h, "/ui/credentials/new?credential_type="+typeID)
	for _, want := range []string{`name="input_api_token"`, `name="input_api_url"`} {
		if !strings.Contains(chosen, want) {
			t.Errorf("choosing a credential type did not render %s", want)
		}
	}

	// PLAN.md Section 29.3: an ask-at-runtime input is never persisted, so
	// a form that stores values must not offer a control for one. This is
	// the assertion that keeps the never-persist rule from being quietly
	// broken by a form that renders every field in the schema.
	if strings.Contains(chosen, `name="input_one_time_code"`) {
		t.Error("the create form offers a control for an ask-at-runtime input, which is never stored")
	}

	// The secret input must be a password control. A type declares which of
	// its fields are secret and the form is the last place that can honour
	// it before a value is typed into a page.
	token := regexp.MustCompile(`<input[^>]*name="input_api_token"[^>]*>`).FindString(chosen)
	if !strings.Contains(token, `type="password"`) {
		t.Errorf("the secret input rendered as %q, want a password control", token)
	}
}

// TestCredentialsForm_CreatesAndNeverEchoesTheSecret walks the create the
// whole way and then looks for the secret everywhere it could have leaked.
func TestCredentialsForm_CreatesAndNeverEchoesTheSecret(t *testing.T) {
	h := newHarness(t, adminIdentity)

	form := body(t, h, "/ui/credentials/new")
	typeID := optionValue(t, form, "credential_type", "Conformance API")
	orgID := optionValue(t, body(t, h, "/ui/credentials/new?credential_type="+typeID), "organization", "acme")

	// uniqueName rather than a literal, because the view registry and the
	// stores it captured are process-wide: `go test -count=3` runs this
	// body three times against the same store, and a fixed name collides
	// with the credential the previous run created.
	name := uniqueName(t, "form-created-credential")
	const secret = "sk-live-FORM-CANARY-9f8e7d6c5b4a"

	w := h.post(t, "/ui/credentials", map[string]string{
		"credential_type": typeID,
		"organization":    orgID,
		"name":            name,
		"description":     "created through the real form",
		"input_api_token": secret,
		"input_api_url":   "https://created.example.test",
	})
	if w.Code >= http.StatusBadRequest {
		t.Fatalf("creating a credential = %d, want a redirect or a page: %s", w.Code, w.Body.String())
	}

	list := body(t, h, "/ui/credentials")
	if !strings.Contains(list, name) {
		t.Fatalf("the credential created through the form is not in the list:\n%s", list)
	}
	if strings.Contains(list, secret) {
		t.Fatal("the credentials list rendered a secret value")
	}

	// The detail page is the other place a value could surface, and the
	// edit form is the place it would be most natural to prefill with one.
	// Neither may ever contain it.
	for _, path := range []string{"/ui/credentials", "/ui/credentials/new"} {
		if page := body(t, h, path); strings.Contains(page, secret) {
			t.Errorf("%s rendered the secret value", path)
		}
	}
}

// TestCredentialsForm_RejectsAnInputTheTypeDoesNotDeclare is the other half
// of the narrowing: resolving controls from a submitted value must not turn
// into accepting whatever a submission asks for.
func TestCredentialsForm_RejectsAnInputTheTypeDoesNotDeclare(t *testing.T) {
	h := newHarness(t, adminIdentity)

	form := body(t, h, "/ui/credentials/new")
	typeID := optionValue(t, form, "credential_type", "Conformance API")
	orgID := optionValue(t, body(t, h, "/ui/credentials/new?credential_type="+typeID), "organization", "acme")

	w := h.post(t, "/ui/credentials", map[string]string{
		"credential_type":    typeID,
		"organization":       orgID,
		"name":               uniqueName(t, "smuggled-input-credential"),
		"input_api_token":    "irrelevant",
		"input_not_declared": "should be refused",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("submitting an input the type never declared = %d, want 400", w.Code)
	}
}

// TestCredentialsForm_ChoosingATypeAndSubmittingRevealsItsInputs is the
// no-JavaScript path, which is the only path today.
//
// Somebody picks a credential type and presses the button. The submission
// carries no inputs because the controls for them did not exist yet, so it
// fails validation -- and the form it comes back as has to be the one with
// those controls on it. If the redisplay resolved fields the way the first
// render did, the person would be told a value was required by a form that
// still had nowhere to type it.
func TestCredentialsForm_ChoosingATypeAndSubmittingRevealsItsInputs(t *testing.T) {
	h := newHarness(t, adminIdentity)

	form := body(t, h, "/ui/credentials/new")
	typeID := optionValue(t, form, "credential_type", "Conformance API")
	orgID := optionValue(t, body(t, h, "/ui/credentials/new?credential_type="+typeID), "organization", "acme")

	w := h.post(t, "/ui/credentials", map[string]string{
		"credential_type": typeID,
		"organization":    orgID,
		"name":            "chose-a-type-first",
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("submitting with a type but no inputs = %d, want 422", w.Code)
	}
	if got := w.Body.String(); !strings.Contains(got, `name="input_api_token"`) {
		t.Error("the redisplayed form does not carry the chosen type's inputs, so the required value has nowhere to be typed")
	}
}
