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
