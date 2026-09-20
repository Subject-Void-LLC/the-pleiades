// Package project_test's git coverage: cloning a real repository.
//
// Not a mock and not a fixture directory pretending to be one. go-git
// initialises an actual repository on disk, commits to it, and the syncer
// clones it over a file:// URL through the same code path an https remote
// takes. RULE 0's representative-or-nothing rule is the whole reason: a
// test that faked the clone would assert that this package can call a fake.
package project_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// newRepo builds a real git repository containing the given files and
// returns its path and the commit hash.
// allowLocal is the policy a test that clones a repository on this machine
// needs, and saying so at each call site is the point of the policy existing.
// A deployment refuses a local source by default: go-git treats a bare path as
// one, the file transport needs a git binary the shipped image does not carry,
// and a source on the Controller's own disk is not something a person filling
// in a form should be able to reach for. A test IS a development machine, so it
// opts in, visibly.
var allowLocal = project.SourcePolicy{AllowLocalPath: true}

func newRepo(t *testing.T, files map[string]string) (string, string) {
	t.Helper()

	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit() = %v", err)
	}
	tree, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree() = %v", err)
	}

	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("MkdirAll(%s) = %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) = %v", name, err)
		}
		if _, err := tree.Add(name); err != nil {
			t.Fatalf("Add(%s) = %v", name, err)
		}
	}

	hash, err := tree.Commit("seed", &gogit.CommitOptions{
		Author: &object.Signature{Name: "conformance", Email: "c@example.test", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("Commit() = %v", err)
	}
	return dir, hash.String()
}

// TestGitSyncer_ClonesAndListsPlaybooks is the whole point of the package:
// a repository somebody else owns becomes a set of runnable files here.
func TestGitSyncer_ClonesAndListsPlaybooks(t *testing.T) {
	origin, want := newRepo(t, map[string]string{
		"site.yml":              "- hosts: all\n",
		"playbooks/deploy.yaml": "- hosts: web\n",
		"README.md":             "not a playbook\n",
		".hidden.yml":           "not offered\n",
	})

	syncer := project.NewGitSyncer(t.TempDir(), nil, allowLocal)
	p := project.Project{ID: 1, OrganizationID: 1, SCMType: project.SCMGit, SCMURL: origin}

	got, err := syncer.Sync(t.Context(), p, nil)
	if err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	if got.Status != project.SyncSucceeded {
		t.Fatalf("Sync() status = %q, err %q; want succeeded", got.Status, got.Err)
	}
	if got.Revision != want {
		t.Errorf("Sync() revision = %q, want the origin's own commit %q", got.Revision, want)
	}

	p.LocalPath = got.LocalPath
	books, err := syncer.Playbooks(t.Context(), p)
	if err != nil {
		t.Fatalf("Playbooks() = %v", err)
	}
	found := strings.Join(books, " ")
	for _, want := range []string{"site.yml", "playbooks/deploy.yaml"} {
		if !strings.Contains(found, want) {
			t.Errorf("Playbooks() = %v, want it to include %s", books, want)
		}
	}
	// A README is not runnable, and a dotfile is not offered: both would be
	// noise in a chooser somebody has to pick a playbook out of.
	for _, unwanted := range []string{"README.md", ".hidden.yml"} {
		if strings.Contains(found, unwanted) {
			t.Errorf("Playbooks() = %v, want it to exclude %s", books, unwanted)
		}
	}
}

// TestGitSyncer_SyncingTwiceIsFine covers the ordinary case of pressing
// Sync again, which takes the fetch path rather than the clone path.
func TestGitSyncer_SyncingTwiceIsFine(t *testing.T) {
	origin, want := newRepo(t, map[string]string{"site.yml": "- hosts: all\n"})

	syncer := project.NewGitSyncer(t.TempDir(), nil, allowLocal)
	p := project.Project{ID: 1, OrganizationID: 1, SCMType: project.SCMGit, SCMURL: origin}

	if _, err := syncer.Sync(t.Context(), p, nil); err != nil {
		t.Fatalf("first Sync() = %v", err)
	}
	got, err := syncer.Sync(t.Context(), p, nil)
	if err != nil {
		t.Fatalf("second Sync() = %v", err)
	}
	if got.Status != project.SyncSucceeded {
		t.Fatalf("second Sync() status = %q, err %q; want succeeded", got.Status, got.Err)
	}
	if got.Revision != want {
		t.Errorf("second Sync() revision = %q, want %q", got.Revision, want)
	}
}

// TestGitSyncer_AFailedSyncIsAResultNotAnError is the distinction the
// Syncer contract draws: a repository that cannot be reached is an outcome
// to record against the project and show an operator, not a failure to
// attempt.
func TestGitSyncer_AFailedSyncIsAResultNotAnError(t *testing.T) {
	syncer := project.NewGitSyncer(t.TempDir(), nil, allowLocal)
	p := project.Project{
		ID: 1, OrganizationID: 1, SCMType: project.SCMGit,
		SCMURL: filepath.Join(t.TempDir(), "no-such-repository"),
	}

	got, err := syncer.Sync(t.Context(), p, nil)
	if err != nil {
		t.Fatalf("Sync() = %v, want the failure reported in the Result", err)
	}
	if got.Status != project.SyncFailed {
		t.Errorf("Sync() status = %q, want failed", got.Status)
	}
	if got.Err == "" {
		t.Error("Sync() recorded no reason for the failure, so nothing can be shown to an operator")
	}
}

// TestGitSyncer_AnUnsyncableProjectIsRefusedOutright is the other half:
// there is nothing to attempt, so it is an error rather than a recorded
// failure.
func TestGitSyncer_AnUnsyncableProjectIsRefusedOutright(t *testing.T) {
	syncer := project.NewGitSyncer(t.TempDir(), nil, allowLocal)
	for _, p := range []project.Project{
		{ID: 1, SCMType: project.SCMGit},                              // no URL
		{ID: 2, SCMType: project.SCMManual, SCMURL: "https://x.test"}, // not implemented
	} {
		if _, err := syncer.Sync(t.Context(), p, nil); err == nil {
			t.Errorf("Sync(%+v) = nil, want ErrNotSyncable", p)
		}
	}
}

// TestGitSyncer_NeverStoresACredentialInTheFailureReason is the one that
// matters most.
//
// A sync failure is written to the database and rendered to an operator. A
// URL carrying userinfo is echoed verbatim by transport errors, so without
// scrubbing, the ordinary act of mistyping a private repository's address
// files the token in a column and puts it on a page.
func TestGitSyncer_NeverStoresACredentialInTheFailureReason(t *testing.T) {
	const token = "ghp-SUPERSECRET-TOKEN-0123456789"

	syncer := project.NewGitSyncer(t.TempDir(), nil, allowLocal)
	p := project.Project{
		ID: 1, OrganizationID: 1, SCMType: project.SCMGit,
		SCMURL: "https://someone:" + token + "@git.invalid/private/repo.git",
	}

	got, err := syncer.Sync(t.Context(), p, nil)
	if err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	if got.Status != project.SyncFailed {
		t.Fatalf("Sync() status = %q, want failed against an unreachable host", got.Status)
	}
	if strings.Contains(got.Err, token) {
		t.Fatalf("the recorded failure carries the credential:\n%s", got.Err)
	}
	if strings.Contains(got.Err, "someone:") {
		t.Errorf("the recorded failure carries the userinfo:\n%s", got.Err)
	}
}

// TestGitSyncer_ReportsProgressToTheWriter proves the clone's own output
// reaches the writer a caller hands over, which is what makes watching a slow
// fetch possible. A local origin transfers almost nothing, so this asserts
// that SOMETHING was reported rather than matching git's wording, which is
// the library's to change.
func TestGitSyncer_ReportsProgressToTheWriter(t *testing.T) {
	origin, _ := newRepo(t, map[string]string{"site.yml": "- hosts: all\n"})

	syncer := project.NewGitSyncer(t.TempDir(), nil, allowLocal)
	p := project.Project{ID: 1, OrganizationID: 1, SCMType: project.SCMGit, SCMURL: origin}

	var progress bytes.Buffer
	got, err := syncer.Sync(t.Context(), p, &progress)
	if err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	if got.Status != project.SyncSucceeded {
		t.Fatalf("Sync() status = %q, err %q; want succeeded", got.Status, got.Err)
	}
	if progress.Len() == 0 {
		t.Error("the clone reported nothing to the progress writer, so a reader would watch an empty page")
	}
}

// TestGitSyncer_ANilProgressWriterIsNotAFailure covers the ordinary case of
// nobody watching: a sync with no reader must clone exactly as well.
func TestGitSyncer_ANilProgressWriterIsNotAFailure(t *testing.T) {
	origin, want := newRepo(t, map[string]string{"site.yml": "- hosts: all\n"})

	syncer := project.NewGitSyncer(t.TempDir(), nil, allowLocal)
	p := project.Project{ID: 1, OrganizationID: 1, SCMType: project.SCMGit, SCMURL: origin}

	got, err := syncer.Sync(t.Context(), p, nil)
	if err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	if got.Status != project.SyncSucceeded || got.Revision != want {
		t.Errorf("Sync() = %q/%q, want a clean clone at %s", got.Status, got.Revision, want)
	}
}

// TestGitSyncer_RefusesALocalOriginWhenTheDeploymentHasNotOptedIn is the
// representative test for the source rule, and the assertion that matters is
// the last one.
//
// Everything above it could be satisfied by a check anywhere; that the checkout
// directory was never created is what proves the refusal happened before the
// syncer touched the disk, which no write-path test can establish.
func TestGitSyncer_RefusesALocalOriginWhenTheDeploymentHasNotOptedIn(t *testing.T) {
	origin, _ := newRepo(t, map[string]string{"site.yml": "- hosts: all\n"})

	root := t.TempDir()
	// The zero policy, which is what a Controller started with neither toggle
	// has, and what a forgotten threading produces.
	syncer := project.NewGitSyncer(root, nil, project.SourcePolicy{})
	p := project.Project{ID: 1, OrganizationID: 1, SCMType: project.SCMGit, SCMURL: origin}

	got, err := syncer.Sync(t.Context(), p, nil)
	if err != nil {
		t.Fatalf("Sync() = %v, want a recorded failure rather than an error", err)
	}
	if got.Status != project.SyncFailed {
		t.Fatalf("Sync() status = %q, want failed for a local origin", got.Status)
	}
	if !strings.Contains(got.Err, "PLEIADES_PROJECT_ALLOW_LOCAL_SOURCE") {
		t.Errorf("the recorded failure does not name the toggle that would allow it:\n%s", got.Err)
	}

	// Nothing on disk: no organization directory, no project directory, no
	// partial checkout for a later sync to trip over.
	if entries, err := os.ReadDir(root); err != nil {
		t.Fatalf("reading the checkout root: %v", err)
	} else if len(entries) != 0 {
		t.Errorf("the refused sync left %d entries under the checkout root, want none", len(entries))
	}

	// The same origin with the deployment's consent works, which is what makes
	// this a check rather than a ban on local development.
	permitted := project.NewGitSyncer(t.TempDir(), nil, allowLocal)
	if ok, err := permitted.Sync(t.Context(), p, nil); err != nil || ok.Status != project.SyncSucceeded {
		t.Errorf("Sync() with local paths permitted = %q (%s), err %v; want it to succeed", ok.Status, ok.Err, err)
	}
}

// TestGitSyncer_FetchesFromTheProjectsCurrentURL covers a defect that outlived
// several phases: a fetch passed no remote, so go-git used the URL stored in
// the checkout's own .git/config, which is the one the FIRST clone used.
// Editing a project's address changed nothing about what was fetched, and the
// address this platform validated was not the address it dialed.
func TestGitSyncer_FetchesFromTheProjectsCurrentURL(t *testing.T) {
	first, firstHead := newRepo(t, map[string]string{"site.yml": "- hosts: all\n"})
	second, secondHead := newRepo(t, map[string]string{"other.yml": "- hosts: db\n"})
	if firstHead == secondHead {
		t.Fatal("the two origins share a HEAD, so this test could not tell them apart")
	}

	syncer := project.NewGitSyncer(t.TempDir(), nil, allowLocal)
	p := project.Project{ID: 1, OrganizationID: 1, SCMType: project.SCMGit, SCMURL: first}

	got, err := syncer.Sync(t.Context(), p, nil)
	if err != nil || got.Status != project.SyncSucceeded {
		t.Fatalf("the first sync = %q (%s), err %v", got.Status, got.Err, err)
	}
	if got.Revision != firstHead {
		t.Fatalf("the first sync landed on %q, want the first origin's HEAD %q", got.Revision, firstHead)
	}

	// Repointed, as an operator correcting a typo or moving a repository does.
	// The checkout is still there, so this takes the fetch path rather than the
	// clone path, which is where the defect lived.
	p.SCMURL = second
	got, err = syncer.Sync(t.Context(), p, nil)
	if err != nil {
		t.Fatalf("the second sync = %v", err)
	}
	if got.Status != project.SyncSucceeded {
		t.Fatalf("the second sync = %q (%s), want it to succeed", got.Status, got.Err)
	}
	if got.Revision != secondHead {
		t.Errorf("after repointing, the sync landed on %q, want the new origin's HEAD %q; "+
			"a fetch that ignores the project's own URL fetches from wherever it first cloned",
			got.Revision, secondHead)
	}
}

// TestGitSyncer_AFailedRepointKeepsTheLastGoodCheckout is the hazard the
// repoint fix had to avoid rather than introduce.
//
// Replacing a checkout means removing one, and doing that before the new clone
// succeeds would leave a project with no tree whenever the new address is
// wrong. A project with no tree is one whose every template stops resolving, so
// a typo in a URL would take the automation down until somebody typed a
// working one. The old tree therefore has to keep serving until there is a
// better one.
func TestGitSyncer_AFailedRepointKeepsTheLastGoodCheckout(t *testing.T) {
	origin, head := newRepo(t, map[string]string{"site.yml": "- hosts: all\n"})

	root := t.TempDir()
	syncer := project.NewGitSyncer(root, nil, allowLocal)
	p := project.Project{ID: 1, OrganizationID: 1, SCMType: project.SCMGit, SCMURL: origin}

	first, err := syncer.Sync(t.Context(), p, nil)
	if err != nil || first.Status != project.SyncSucceeded {
		t.Fatalf("the first sync = %q (%s), err %v", first.Status, first.Err, err)
	}

	// Repointed at something that is not there.
	p.SCMURL = filepath.Join(t.TempDir(), "no-such-repository")
	got, err := syncer.Sync(t.Context(), p, nil)
	if err != nil {
		t.Fatalf("Sync() = %v, want a recorded failure", err)
	}
	if got.Status != project.SyncFailed {
		t.Fatalf("Sync() status = %q, want failed against a missing origin", got.Status)
	}

	// The tree the first sync produced is still there, still that revision,
	// and still holds the file a template would name.
	p.SCMURL = origin
	p.LocalPath = first.LocalPath
	if _, err := os.Stat(filepath.Join(first.LocalPath, "site.yml")); err != nil {
		t.Errorf("the previous checkout did not survive a failed repoint: %v", err)
	}
	again, err := syncer.Sync(t.Context(), p, nil)
	if err != nil || again.Status != project.SyncSucceeded {
		t.Fatalf("syncing the original address again = %q (%s), err %v", again.Status, again.Err, err)
	}
	if again.Revision != head {
		t.Errorf("revision = %q, want the original origin's HEAD %q", again.Revision, head)
	}

	// And no staging directory is left behind for the next sync to trip over.
	entries, err := os.ReadDir(filepath.Join(root, "1"))
	if err != nil {
		t.Fatalf("reading the organization's checkout directory: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".incoming") {
			t.Errorf("a staging directory was left behind: %s", e.Name())
		}
	}
}
