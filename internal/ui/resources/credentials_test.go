package resources_test

import (
	"net/http"
	"strings"
	"testing"
)

// The credential views' one non-negotiable property, driven through the
// real router with a real session and a real ent-backed store.
//
// The store's projection has no field for a plaintext input, so this
// cannot fail unless somebody adds one, and that is exactly why it is
// worth asserting here rather than only in internal/credstore: a leak
// would arrive as a new field on the projection plus a cell rendering it,
// which is two changes that each look reasonable alone.

// credentialPages are every page these two views serve, so a new template
// cannot be the one that renders the value.
func credentialPages() []string {
	return []string{
		"/ui/credentials",
		"/ui/credentials/1",
		"/ui/credential-types",
		"/ui/credential-types/1",
	}
}

// TestCredentialViewsNeverRenderAStoredSecret is the leak assertion.
func TestCredentialViewsNeverRenderAStoredSecret(t *testing.T) {
	h := newHarness(t, adminIdentity)

	for _, path := range credentialPages() {
		w := h.get(t, path)
		if w.Code >= http.StatusInternalServerError {
			t.Fatalf("GET %s = %d, want a page rather than a server error", path, w.Code)
		}
		if strings.Contains(w.Body.String(), credentialCanary) {
			t.Errorf("GET %s rendered the stored secret", path)
		}
	}
}

// TestCredentialsListNamesWhatExistsWithoutItsValue covers the revision
// this view's package doc records: the list ships, and what it discloses is
// the existence of a credential rather than its contents.
func TestCredentialsListNamesWhatExistsWithoutItsValue(t *testing.T) {
	h := newHarness(t, adminIdentity)

	body := h.get(t, "/ui/credentials").Body.String()
	for _, want := range []string{"conformance credential", "Conformance API"} {
		if !strings.Contains(body, want) {
			t.Errorf("the credentials list does not mention %q, so it enumerates nothing useful", want)
		}
	}
	// The redaction marker rather than the value, which is the shape a
	// reader should meet: withheld and absent are different facts.
	if strings.Contains(body, credentialCanary) {
		t.Error("the credentials list rendered the stored secret")
	}
}

// TestCredentialTypesListsTheShippedCatalog proves the reconcile and the
// view meet: the types this build ships are on the page, marked as the
// platform's, alongside the custom one.
func TestCredentialTypesListsTheShippedCatalog(t *testing.T) {
	h := newHarness(t, adminIdentity)

	body := h.get(t, "/ui/credential-types").Body.String()
	for _, want := range []string{"Machine", "Amazon Web Services", "Conformance API", "platform", "custom"} {
		if !strings.Contains(body, want) {
			t.Errorf("the credential types list does not mention %q", want)
		}
	}
}

// TestCredentialViewsRefuseAnUnscopedReader is the other half of the
// package doc's argument. The list ships BECAUSE the scope is the control,
// so a reader without it seeing the page would remove the reason.
func TestCredentialViewsRefuseAnUnscopedReader(t *testing.T) {
	h := newHarness(t, viewerIdentity)

	for _, path := range credentialPages() {
		w := h.get(t, path)
		// The status, not merely the absence of the name. An empty list
		// rendered at 200 would pass an absence check while proving the
		// opposite of what this test is for.
		if w.Code != http.StatusForbidden {
			t.Errorf("GET %s as a viewer = %d, want %d: the scope is the control this view's package doc rests on",
				path, w.Code, http.StatusForbidden)
		}
		if strings.Contains(w.Body.String(), "conformance credential") {
			t.Errorf("GET %s as a viewer listed credentials", path)
		}
	}
}

// TestLaunchFormPromptsForACredentialInputAndNeverPrefillsIt is the launch
// half of the never-persist rule, driven through the real form.
//
// An ask-at-runtime input is never stored, so there is nothing to prefill
// and the control has to be empty every time. A form that prefilled it
// would be rendering a value the platform is not supposed to have.
func TestLaunchFormPromptsForACredentialInputAndNeverPrefillsIt(t *testing.T) {
	h := newHarness(t, adminIdentity)

	body := h.get(t, "/ui/templates/1/launch").Body.String()

	// The control name is the contract between the form and the
	// submission: credential_<id>_<inputid>, so two bound credentials that
	// both declare "password" cannot collide.
	if !strings.Contains(body, "credential_1_one_time_code") {
		t.Errorf("the launch form does not render the prompted credential control:\n%s", body)
	}
	// A password control, never a text one.
	if !strings.Contains(body, `type="password"`) {
		t.Error("the prompted credential input is not rendered as a password control")
	}
	if strings.Contains(body, credentialCanary) {
		t.Error("the launch form rendered a stored credential value")
	}
}

// TestTemplateCredentialsActionIsGatedByTheCredentialScope is the other
// half of the decision recorded in templates/credentials.go: binding is a
// higher privilege than editing, so it must not be reachable through the
// template's own write scope.
func TestTemplateCredentialsActionIsGatedByTheCredentialScope(t *testing.T) {
	h := newHarness(t, adminIdentity)

	if w := h.get(t, "/ui/templates/1/credentials"); w.Code != http.StatusOK {
		t.Fatalf("GET the credentials action as admin = %d, want 200", w.Code)
	}
	// The choices name the type beside the credential, because the binding
	// rule keys on kind and a reader has to see a collision coming.
	body := h.get(t, "/ui/templates/1/credentials").Body.String()
	if !strings.Contains(body, "conformance credential (Conformance API)") {
		t.Errorf("the bind form does not offer the credential with its type:\n%s", body)
	}

	viewer := newHarness(t, viewerIdentity)
	if w := viewer.get(t, "/ui/templates/1/credentials"); w.Code != http.StatusForbidden {
		t.Errorf("GET the credentials action as a viewer = %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestTemplateCredentialsFormRendersWhatIsAlreadyBound is a data-loss
// regression guard, and the loss it guards against was live.
//
// The control replaces a template's whole credential list, which its own
// help text says. It resolved the currently bound ids, sorted them, and
// dropped them, because a record action had nowhere to put a form value
// before the prefill seam existed. So a template bound to a credential
// rendered a multi-select with nothing selected, and pressing the button as
// drawn replaced that binding with none: the template silently stopped
// authenticating as anything, and nothing on the page suggested it would.
//
// The fixture binds template 1 to one credential, so "selected" appearing
// against that option is the whole assertion.
func TestTemplateCredentialsFormRendersWhatIsAlreadyBound(t *testing.T) {
	h := newHarness(t, adminIdentity)

	body := h.get(t, "/ui/templates/1/credentials").Body.String()

	const bound = "conformance credential (Conformance API)"
	if !strings.Contains(body, bound) {
		t.Fatalf("the bound credential is not offered at all, so this proves nothing:\n%s", body)
	}
	if !strings.Contains(body, "selected>"+bound) {
		t.Errorf("the credential this template is bound to rendered unselected, so saving the form as drawn would unbind it:\n%s", body)
	}
}

// TestRecordActionRedirectsStayInsideTheUIMount is a regression guard for a
// bug all four hand-built action redirects had at once.
//
// Each built the path itself and each left off the UI's mount prefix, so a
// successful write sent the operator to /templates/1 rather than
// /ui/templates/1. The save had already happened; what they saw was a 404,
// which reads as though it had not.
//
// Asserting the prefix rather than the exact path, because the point is
// that the handler owns the prefix and a call site does not.
func TestRecordActionRedirectsStayInsideTheUIMount(t *testing.T) {
	h := newHarness(t, adminIdentity)

	w := h.post(t, "/ui/templates/1/credentials", map[string]string{"credentials": "1"})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("binding credentials = %d, want a redirect: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/ui/") {
		t.Errorf("Location = %q, which is outside the UI mount, so a successful save lands on a 404", loc)
	}
	if !strings.Contains(loc, "templates/1") {
		t.Errorf("Location = %q, want it to return to the record that was written", loc)
	}
}
