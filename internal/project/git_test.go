// Package project_test's git coverage: cloning a real repository.
//
// Not a mock and not a fixture directory pretending to be one. go-git
// initialises an actual repository on disk, commits to it, and the syncer
// clones it over a file:// URL through the same code path an https remote
// takes. RULE 0's representative-or-nothing rule is the whole reason: a
// test that faked the clone would assert that this package can call a fake.
package project_test

import (
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

	syncer := project.NewGitSyncer(t.TempDir(), nil)
	p := project.Project{ID: 1, OrganizationID: 1, SCMType: project.SCMGit, SCMURL: origin}

	got, err := syncer.Sync(t.Context(), p)
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

	syncer := project.NewGitSyncer(t.TempDir(), nil)
	p := project.Project{ID: 1, OrganizationID: 1, SCMType: project.SCMGit, SCMURL: origin}

	if _, err := syncer.Sync(t.Context(), p); err != nil {
		t.Fatalf("first Sync() = %v", err)
	}
	got, err := syncer.Sync(t.Context(), p)
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
	syncer := project.NewGitSyncer(t.TempDir(), nil)
	p := project.Project{
		ID: 1, OrganizationID: 1, SCMType: project.SCMGit,
		SCMURL: filepath.Join(t.TempDir(), "no-such-repository"),
	}

	got, err := syncer.Sync(t.Context(), p)
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
	syncer := project.NewGitSyncer(t.TempDir(), nil)
	for _, p := range []project.Project{
		{ID: 1, SCMType: project.SCMGit},                              // no URL
		{ID: 2, SCMType: project.SCMManual, SCMURL: "https://x.test"}, // not implemented
	} {
		if _, err := syncer.Sync(t.Context(), p); err == nil {
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

	syncer := project.NewGitSyncer(t.TempDir(), nil)
	p := project.Project{
		ID: 1, OrganizationID: 1, SCMType: project.SCMGit,
		SCMURL: "https://someone:" + token + "@git.invalid/private/repo.git",
	}

	got, err := syncer.Sync(t.Context(), p)
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
