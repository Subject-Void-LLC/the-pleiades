// Package project_test's playbook source coverage: a synced working tree
// seen as something a template can run out of.
//
// It uses a real directory as the checkout rather than a fake filesystem,
// because the thing most worth proving here is the containment check, and
// containment is a property of real paths. A fake would let the test assert
// its own idea of what "inside the tree" means.
package project_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// stubStore serves a fixed set of projects.
type stubStore struct{ projects []project.Project }

func (s stubStore) Get(_ context.Context, id int) (project.Project, error) {
	for _, p := range s.projects {
		if p.ID == id {
			return p, nil
		}
	}
	return project.Project{}, project.ErrNotFound
}
func (s stubStore) List(context.Context, project.Query) ([]project.Project, error) {
	return s.projects, nil
}
func (s stubStore) Create(context.Context, project.Project) (project.Project, error) {
	return project.Project{}, nil
}
func (s stubStore) Update(context.Context, project.Project) error         { return nil }
func (s stubStore) Delete(context.Context, int) error                     { return nil }
func (s stubStore) RecordSync(context.Context, int, project.Result) error { return nil }

// stubSyncer lists what is actually on disk under the project's path.
type stubSyncer struct{}

func (stubSyncer) Sync(context.Context, project.Project, project.Auth) (project.Result, error) {
	return project.Result{}, nil
}
func (stubSyncer) Playbooks(_ context.Context, p project.Project) ([]string, error) {
	entries, err := os.ReadDir(p.LocalPath)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yml") {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// newSource builds a source over one synced project with the given files.
func newSource(t *testing.T, files map[string]string) (*project.PlaybookSource, project.Project) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	p := project.Project{
		ID: 1, Name: "playbooks", OrganizationID: 1,
		SCMType: project.SCMGit, SCMURL: "https://git.example.test/p.git",
		SyncStatus: project.SyncSucceeded, LocalPath: dir,
	}
	return project.NewPlaybookSource(stubStore{projects: []project.Project{p}}, stubSyncer{}), p
}

// TestPlaybookSource_ResolvesASyncedProjectsPlaybook is the connection this
// type exists to make: a definition a template can store becomes bytes a
// dispatch can run.
func TestPlaybookSource_ResolvesASyncedProjectsPlaybook(t *testing.T) {
	src, _ := newSource(t, map[string]string{"site.yml": "- hosts: all\n"})

	ids, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	want := project.Definition(1, "site.yml")
	if len(ids) != 1 || ids[0] != want {
		t.Fatalf("List() = %v, want exactly [%s]", ids, want)
	}

	// The same id the catalog offered has to be the one that resolves.
	// Listing something Get cannot return is how a template saves and then
	// fails at launch.
	body, err := src.Get(t.Context(), want)
	if err != nil {
		t.Fatalf("Get(%s) = %v", want, err)
	}
	if !strings.Contains(string(body), "hosts: all") {
		t.Errorf("Get(%s) returned %q, want the file's contents", want, body)
	}
}

// TestPlaybookSource_AnUnsyncedProjectOffersNothing covers the state a
// project spends its first minutes in.
//
// Offering a definition out of a tree that is not checked out would put a
// row in the chooser that cannot run, which is the failure this whole type
// is arranged to avoid.
func TestPlaybookSource_AnUnsyncedProjectOffersNothing(t *testing.T) {
	dir := t.TempDir()
	p := project.Project{
		ID: 1, Name: "playbooks", SyncStatus: project.SyncNever, LocalPath: dir,
	}
	src := project.NewPlaybookSource(stubStore{projects: []project.Project{p}}, stubSyncer{})

	ids, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("List() = %v, want nothing from a project that has never synced", ids)
	}
	if _, err := src.Get(t.Context(), project.Definition(1, "site.yml")); err == nil {
		t.Error("Get() resolved a playbook out of a project that has never synced")
	}
}

// TestPlaybookSource_RefusesAPathOutsideTheTree is the one that matters.
//
// A definition is stored on a template and a template is written by a
// person, so these are values somebody can actually supply rather than
// values this package produced. A plain filepath.Join would resolve every
// one of them happily.
func TestPlaybookSource_RefusesAPathOutsideTheTree(t *testing.T) {
	src, _ := newSource(t, map[string]string{"site.yml": "- hosts: all\n"})

	outside := filepath.Join(t.TempDir(), "secret.yml")
	if err := os.WriteFile(outside, []byte("- hosts: stolen\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	for _, path := range []string{
		"../../etc/passwd",
		"../" + filepath.Base(filepath.Dir(outside)) + "/" + filepath.Base(outside),
		"/etc/passwd",
		"subdir/../../escape.yml",
	} {
		body, err := src.Get(t.Context(), project.Definition(1, path))
		if err == nil {
			t.Errorf("Get(%q) returned %d bytes, want it refused", path, len(body))
			continue
		}
		// Refused as not-found rather than as a distinct error: naming what
		// was rejected is itself a probe result.
		if !strings.Contains(err.Error(), playbook.ErrNotFound.Error()) {
			t.Errorf("Get(%q) = %v, want a not-found refusal", path, err)
		}
	}
}

// TestPlaybookSource_RefusesAMalformedDefinition covers the ids that name
// no project at all.
func TestPlaybookSource_RefusesAMalformedDefinition(t *testing.T) {
	src, _ := newSource(t, map[string]string{"site.yml": "- hosts: all\n"})

	for _, id := range []string{"site.yml", "", "abc/site.yml", "0/site.yml", "1/", "-1/site.yml"} {
		if _, err := src.Get(t.Context(), id); err == nil {
			t.Errorf("Get(%q) = nil, want it refused", id)
		}
	}
}
