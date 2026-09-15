// Package resources_test's project coverage: registering a repository and
// pressing Sync, which is the whole of "get a git repo in" from a browser.
//
// The clone itself is not exercised here. internal/project's own tests
// drive a real git repository through the real syncer; this suite stands a
// fake in its place deliberately, because a conformance run that cloned
// from the internet would measure somebody else's uptime rather than this
// view.
package resources_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// TestProjectsView_CreatesAGitProject is the create path, including the one
// validation that cannot wait for a sync to discover.
func TestProjectsView_CreatesAGitProject(t *testing.T) {
	h := newHarness(t, adminIdentity)

	form := body(t, h, "/ui/projects/new")
	orgID := optionValue(t, form, "organization", "acme")

	w := h.post(t, "/ui/projects", map[string]string{
		"name":         uniqueName(t, "infra-automation"),
		"description":  "created through the real form",
		"organization": orgID,
		"scm_type":     "git",
		"scm_url":      "https://git.example.test/team/infra.git",
		"scm_branch":   "main",
	})
	if w.Code >= http.StatusBadRequest {
		t.Fatalf("creating a project = %d: %s", w.Code, w.Body.String())
	}

	list := body(t, h, "/ui/projects")
	if !strings.Contains(list, "infra-automation") {
		t.Fatalf("the project created through the form is not in the list:\n%s", list)
	}
}

// TestProjectsView_AGitProjectWithoutAURLIsRefused covers the validation
// that belongs at save time rather than sync time.
//
// A git project with no URL can never do the one thing it exists for, and
// finding that out by pressing Sync and reading a failure is a worse way to
// be told than a message beside the empty box.
func TestProjectsView_AGitProjectWithoutAURLIsRefused(t *testing.T) {
	h := newHarness(t, adminIdentity)
	orgID := optionValue(t, body(t, h, "/ui/projects/new"), "organization", "acme")

	w := h.post(t, "/ui/projects", map[string]string{
		"name":         uniqueName(t, "no-url-project"),
		"organization": orgID,
		"scm_type":     "git",
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("creating a git project with no URL = %d, want 422", w.Code)
	}
	if got := w.Body.String(); !strings.Contains(got, "repository URL") {
		t.Error("the refusal does not say what is missing, so the empty control is unexplained")
	}
}

// TestProjectsView_SyncRecordsTheRevisionAndRevealsThePlaybooks is the
// payoff: pressing Sync turns a registered URL into files a template can
// name.
func TestProjectsView_SyncRecordsTheRevisionAndRevealsThePlaybooks(t *testing.T) {
	h := newHarness(t, adminIdentity)

	// A project of this test's own rather than the seeded one. The store is
	// process-wide, so asserting "has never synced" against a shared record
	// only holds until something syncs it, which under `go test -count=3`
	// is the previous run of this very test.
	orgID := optionValue(t, body(t, h, "/ui/projects/new"), "organization", "acme")
	created := uniqueName(t, "sync-me")
	if w := h.post(t, "/ui/projects", map[string]string{
		"name":         created,
		"organization": orgID,
		"scm_type":     "git",
		"scm_url":      "https://git.example.test/team/sync-me.git",
	}); w.Code >= http.StatusBadRequest {
		t.Fatalf("creating the project to sync = %d: %s", w.Code, w.Body.String())
	}
	id := recordPath(t, body(t, h, "/ui/projects"), created)

	// It has never synced, so its Playbooks tab is empty. Asserting the
	// before state is what makes the after state mean something.
	before := h.section(t, id, "Playbooks")
	if strings.Contains(before, "site.yml") {
		t.Fatal("the project lists playbooks before it has ever synced")
	}

	if w := h.post(t, id+"/sync", map[string]string{}); w.Code >= http.StatusBadRequest {
		t.Fatalf("syncing = %d: %s", w.Code, w.Body.String())
	}
	// The clone runs off the request now, so wait for it the way the page's
	// Refresh badge does, before asserting what it produced.
	conformanceProjectRunner.Wait()

	after := h.section(t, id, "Playbooks")
	for _, want := range []string{"site.yml", "playbooks/deploy.yml"} {
		if !strings.Contains(after, want) {
			t.Errorf("the Playbooks tab does not list %s after a sync:\n%s", want, after)
		}
	}

	// The revision is what makes "which commit is this" answerable later,
	// including after the checkout is gone.
	detail := body(t, h, id)
	if !strings.Contains(detail, "2f6c1b0") {
		t.Errorf("the project page does not show the synced revision:\n%s", detail)
	}
}

// TestProjectsView_SyncIsGatedOnTheProjectWriteScope proves the action is
// not reachable by somebody who may only look.
//
// A sync makes the controller open a connection to an address the project
// names, which is why it is a write rather than a read. A viewer being able
// to trigger it would be a request-forgery primitive pointed at whatever
// the project names.
func TestProjectsView_SyncIsGatedOnTheProjectWriteScope(t *testing.T) {
	h := newHarness(t, viewerIdentity)

	if w := h.post(t, "/ui/projects/1/sync", map[string]string{}); w.Code < http.StatusBadRequest {
		t.Fatalf("a viewer syncing a project = %d, want it refused", w.Code)
	}
}

// recordPath finds the link to the named record in a rendered list.
//
// The list is where a person would click, so reading the id back out of it
// asserts the link works as a side effect. Deriving it from a counter
// instead would couple the test to how many records earlier tests created,
// which is exactly the process-wide coupling these tests are avoiding.
func recordPath(t *testing.T, list, name string) string {
	t.Helper()
	m := regexp.MustCompile(`href="(/ui/projects/[0-9]+)"[^>]*>\s*` + regexp.QuoteMeta(name)).FindStringSubmatch(list)
	if m == nil {
		t.Fatalf("the projects list has no link to %q:\n%s", name, list)
	}
	return m[1]
}

// TestProjectsView_OffersACredentialAndStoresTheChoice covers the control a
// private repository needs.
//
// The chooser stores an id and never a value. That is not an implementation
// detail worth restating in a test for its own sake: it is why this view can
// exist at all, since internal/archtest fails the build if the UI side can
// reach a plaintext credential, and the resolution happens inside the syncer
// from the id this form saved.
func TestProjectsView_OffersACredentialAndStoresTheChoice(t *testing.T) {
	h := newHarness(t, adminIdentity)

	form := body(t, h, "/ui/projects/new")
	orgID := optionValue(t, form, "organization", "acme")

	// "None" has to be offered, or a public repository would have no way to
	// say so and every project would demand a credential.
	if !strings.Contains(form, "public repository") {
		t.Error("the credential chooser offers no way to say a repository is public")
	}
	credID := optionValue(t, form, "credential", "conformance scm credential (Source Control)")

	name := uniqueName(t, "private-project")
	if w := h.post(t, "/ui/projects", map[string]string{
		"name":         name,
		"organization": orgID,
		"scm_type":     "git",
		"scm_url":      "https://git.example.test/team/private.git",
		"credential":   credID,
	}); w.Code >= http.StatusBadRequest {
		t.Fatalf("creating a project with a credential = %d: %s", w.Code, w.Body.String())
	}

	// The edit form has to come back with the credential still chosen, or
	// an ordinary rename would silently make a private repository public.
	edit := body(t, h, recordPath(t, body(t, h, "/ui/projects"), name)+"/edit")
	block := selectBlock(t, edit, "credential")
	if !strings.Contains(block, "selected") {
		t.Errorf("the saved credential is not selected on the edit form:\n%s", block)
	}
}

// TestProjectsView_RefusesACredentialThatCannotAuthenticateAClone is the
// rule enforced rather than merely rendered.
//
// The chooser hides these, so reaching the refusal needs a submission that
// did not come from the form. That is precisely why it is worth asserting:
// a rule enforced only by what a page happens to offer is not enforced, and
// the same field is accepted over the API.
func TestProjectsView_RefusesACredentialThatCannotAuthenticateAClone(t *testing.T) {
	h := newHarness(t, adminIdentity)

	form := body(t, h, "/ui/projects/new")
	orgID := optionValue(t, form, "organization", "acme")

	offered := selectBlock(t, form, "credential")

	// The cloud credential carries an api_token and an api_url: real
	// inputs, none of which a clone can use.
	if strings.Contains(offered, "Conformance API") {
		t.Error("the chooser offers a cloud credential, which cannot authenticate a clone")
	}

	// The Machine credential is the one that matters. It holds a real SSH
	// key, so a filter keyed on what a credential CARRIES would offer it,
	// and it must still be refused: it was issued to open shells on managed
	// devices, not to read a repository.
	if strings.Contains(offered, "conformance machine credential") {
		t.Error("the chooser offers a Machine credential for a git sync, so a device credential can be spent on a repository")
	}

	// ...and submitting it anyway must be refused rather than saved and
	// discovered at the first sync.
	cloudID := cloudCredentialID(t, h)
	w := h.post(t, "/ui/projects", map[string]string{
		"name":         uniqueName(t, "wrong-credential-project"),
		"organization": orgID,
		"scm_type":     "git",
		"scm_url":      "https://git.example.test/team/private.git",
		"credential":   cloudID,
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("submitting an unusable credential = %d, want 422", w.Code)
	}
	// Caught by view.Validate, which refuses a choice that is not among the
	// control's own options, before the writer's check is reached. That
	// ordering is worth knowing rather than worth changing: the framework
	// rule is the one that fires here, and usableForGit is the backstop for
	// the API path, which has no options to validate against.
	if got := w.Body.String(); !strings.Contains(got, "not a valid choice") {
		t.Errorf("the submission was refused without saying the credential was the problem:\n%s", got)
	}
	if got := w.Body.String(); !strings.Contains(got, "f-credential-error") {
		t.Error("the refusal is not attached to the credential control, so it reads as a form-wide failure")
	}
}

// cloudCredentialID reads the seeded cloud credential's id off the
// Credentials list, which is the only place this suite can learn it without
// reaching into the store.
func cloudCredentialID(t *testing.T, h *harness) string {
	t.Helper()
	list := body(t, h, "/ui/credentials")
	m := regexp.MustCompile(`href="/ui/credentials/([0-9]+)"[^>]*>\s*conformance credential`).FindStringSubmatch(list)
	if m == nil {
		t.Fatalf("the credentials list has no link to the seeded cloud credential:\n%s", list)
	}
	return m[1]
}

// TestProjectsView_SyncHistoryRecordsEachAttempt proves the Sync history tab
// lists a completed attempt, which is the question the project's own badge
// cannot answer: not "is it usable now" but "what has it been doing".
func TestProjectsView_SyncHistoryRecordsEachAttempt(t *testing.T) {
	h := newHarness(t, adminIdentity)

	orgID := optionValue(t, body(t, h, "/ui/projects/new"), "organization", "acme")
	created := uniqueName(t, "history-me")
	if w := h.post(t, "/ui/projects", map[string]string{
		"name":         created,
		"organization": orgID,
		"scm_type":     "git",
		"scm_url":      "https://git.example.test/team/history-me.git",
	}); w.Code >= http.StatusBadRequest {
		t.Fatalf("creating the project = %d: %s", w.Code, w.Body.String())
	}
	id := recordPath(t, body(t, h, "/ui/projects"), created)

	// Nothing has run, so the history is empty rather than absent.
	if before := h.section(t, id, "Sync history"); !strings.Contains(before, "no completed syncs yet") {
		t.Errorf("a never-synced project's history does not say so:\n%s", before)
	}

	if w := h.post(t, id+"/sync", map[string]string{}); w.Code >= http.StatusBadRequest {
		t.Fatalf("syncing = %d: %s", w.Code, w.Body.String())
	}
	conformanceProjectRunner.Wait()

	after := h.section(t, id, "Sync history")
	if !strings.Contains(after, "succeeded") {
		t.Errorf("the Sync history tab does not record the completed attempt:\n%s", after)
	}
}
