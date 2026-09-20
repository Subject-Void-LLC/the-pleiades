// Package project's playbook source: a synced working tree, seen as
// something a template can run out of.
//
// This is what connects a cloned repository to a dispatch. Without it a
// Project is a directory somebody can look at, and a Template still has to
// name content compiled into the binary.
//
// # One resolver, not two
//
// The catalog that offers a definition and the dispatcher that resolves one
// go through this same type. That is deliberate and it is the property
// cmd/controller's own catalog comment already asks for: a definition that
// can be chosen must be one that can be run, or a template saves and then
// fails at launch, which is the worst possible moment to find out.
package project

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/playbook"
)

// definitionSeparator splits a project id from a path within its tree.
const definitionSeparator = "/"

// PlaybookSource resolves "<project id>/<path>" to a playbook's bytes.
type PlaybookSource struct {
	store  Store
	syncer Syncer
}

// NewPlaybookSource returns a playbook.Source over synced projects.
func NewPlaybookSource(store Store, syncer Syncer) *PlaybookSource {
	return &PlaybookSource{store: store, syncer: syncer}
}

// Definition is the id one project playbook is named by.
func Definition(projectID int, path string) string {
	return strconv.Itoa(projectID) + definitionSeparator + path
}

// List returns every runnable definition across every synced project.
func (s *PlaybookSource) List(ctx context.Context) ([]string, error) {
	projects, err := s.store.List(ctx, Query{})
	if err != nil {
		return nil, fmt.Errorf("project: listing projects for the playbook catalog: %w", err)
	}

	var out []string
	for _, p := range projects {
		if p.SyncStatus != SyncSucceeded {
			// A project that has never synced has no tree, and one whose
			// last sync failed has a stale tree that no longer matches what
			// the catalog would claim. Neither is offered.
			continue
		}
		found, err := s.syncer.Playbooks(ctx, p)
		if err != nil {
			// One unreadable project must not empty the whole catalog:
			// every other project is still runnable, and a listing that
			// fails wholesale turns one broken checkout into an outage.
			continue
		}
		for _, path := range found {
			out = append(out, Definition(p.ID, path))
		}
	}
	sort.Strings(out)
	return out, nil
}

// Get resolves a definition to its playbook's bytes.
func (s *PlaybookSource) Get(ctx context.Context, id string) ([]byte, error) {
	projectID, path, ok := splitDefinition(id)
	if !ok {
		return nil, fmt.Errorf("%w: %q is not a project playbook", playbook.ErrNotFound, id)
	}

	p, err := s.store.Get(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("%w: no project %d", playbook.ErrNotFound, projectID)
	}
	if p.SyncStatus != SyncSucceeded || p.LocalPath == "" {
		return nil, fmt.Errorf("%w: project %q has no successful sync, so %q is not checked out",
			playbook.ErrNotFound, p.Name, path)
	}

	body, ok := readWithin(p.LocalPath, path)
	if !ok {
		// A definition is stored on a template and a template is written by
		// a person, so this is reachable input rather than a value this
		// package produced. Refused as not-found rather than reported,
		// because naming what was rejected is itself a probe result.
		return nil, fmt.Errorf("%w: %q does not name a file in project %q", playbook.ErrNotFound, path, p.Name)
	}
	return body, nil
}

// splitDefinition parses "<project id>/<path>".
func splitDefinition(id string) (int, string, bool) {
	numeric, path, found := strings.Cut(id, definitionSeparator)
	if !found {
		return 0, "", false
	}
	projectID, err := strconv.Atoi(numeric)
	if err != nil || projectID < 1 || path == "" {
		return 0, "", false
	}
	return projectID, path, true
}

// readWithin reads the file path names inside root, and reports false for
// anything that is not an ordinary file inside it.
//
// The containment is the point. A definition reaches here from a template
// row, so "3/../../../etc/shadow" is something somebody can actually write,
// and a plain filepath.Join would resolve it happily. And the tree is a
// repository's checkout, whose committers are not necessarily this
// Controller's administrators, and a checkout keeps a repository's
// symlinks: "lib -> /" is content somebody can commit, and a lexical check
// of "lib/etc/..." passes it (FAILURE_PATTERNS 260). So the file is opened
// through an os.Root, which resolves every component, symlinks included,
// inside root and refuses any that leaves it, in the same openat walk that
// opens the file, so nothing can be swapped between a check and the read.
// A symlink that stays inside the tree still works, as a repository's own
// layout expects. The lexical check stays in front as a cheap refusal of
// what could never be inside.
func readWithin(root, path string) ([]byte, bool) {
	if root == "" {
		return nil, false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, false
	}
	full := filepath.Clean(filepath.Join(absRoot, path))
	if full == absRoot || !strings.HasPrefix(full, absRoot+string(filepath.Separator)) {
		return nil, false
	}
	rel, err := filepath.Rel(absRoot, full)
	if err != nil {
		return nil, false
	}
	tree, err := os.OpenRoot(absRoot)
	if err != nil {
		return nil, false
	}
	defer func() { _ = tree.Close() }()
	f, err := tree.Open(rel)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	// A directory is not a playbook, and neither is a device or a pipe.
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	body, err := io.ReadAll(f)
	if err != nil {
		return nil, false
	}
	return body, true
}
