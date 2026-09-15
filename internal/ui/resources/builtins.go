// Package resources is the web UI's composition point: the one place a
// view resource package is made reachable at all.
//
// It contains blank imports and nothing else, deliberately. An init() only
// runs if something imports the package it lives in, so a resource nobody
// imports is a resource that compiles, passes its own tests, and is
// invisible to the running binary. That exact failure is recorded twice in
// this repository's own history -- FAILURE_PATTERNS.md #52 and
// LESSONS_LEARNED.md #56 -- which is why this file exists rather than the
// registry scanning for implementations.
//
// It is hand-maintained rather than generated. internal/catalog/builtins.go
// is regenerated because it mirrors a hundred-entry data table no human
// should transcribe; views are few and curated, with no upstream table, so
// generating this one would mean inventing a table to generate it from.
//
// Adding a view is one line here. Forgetting that line is the whole hazard
// this file's existence is a warning about, so `pleiades forge new-view`
// prints the reminder as its last output.
package resources

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/announce"
	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Deps is every port the built-in views adapt, supplied by the composition
// root.
//
// It is one struct rather than a growing parameter list because the set
// grows with each view, and a positional signature that changes every time
// somebody adds a resource is a signature every caller has to be edited
// for. Views themselves never see this type: each resource's own Register
// takes only the ports it actually uses, which keeps each one narrow enough
// to test with a single fake, and registrars() is the one place the wide
// struct is taken apart.
type Deps struct {
	// Access administers organizations, teams, users and grants. It is one
	// value rather than four because the four views between them need all
	// of it, while each view's own Register still takes only the slice it
	// uses.
	Access access.Store

	// Activity is the append-only record of who changed what. Read-only
	// here: the views never write to it, and the only writer is the
	// audited store the composition root wraps Access with.
	Activity   activity.Store
	Inventory  inventory.Repository
	Sets       inventory.SetStore
	Announce   announce.Store
	Factory    *inventory.ItemFactory
	Jobs       dispatch.JobStore
	Runbooks   runbook.Source
	Dispatcher *api.Dispatcher

	// Templates is the saved definitions this platform launches, and the
	// one port the Templates view both reads and writes.
	Templates launch.Store

	// Projects is where automation content comes from, and ProjectSync is
	// the half of it that touches a network and a disk. Two ports rather
	// than one because they fail in unrelated ways: a store failure is a
	// database problem, a sync failure is somebody else's repository.
	Projects    project.Store
	ProjectSync project.Syncer

	// ProjectRunner starts a Sync's clone in the background so the button
	// does not block the page on it. The syncer above is still the port the
	// Playbooks tab reads a synced tree through.
	ProjectRunner *project.Runner

	// Schedules is when those definitions run without anybody pressing
	// launch. The same store value the controller hands its scheduler,
	// narrowed here to the administration half: this side can describe
	// when a schedule should fire and cannot fire one.
	Schedules schedule.Store

	// Catalog is every definition the deployment can launch, feeding the
	// Templates form's RUNS picker. The same value the template store
	// verifies creates against, wired once in the composition root, so
	// what the form offers and what the store accepts are one list.
	Catalog launch.Catalog

	// Credentials is the control-plane credential store, which is the
	// REDACTED half of the pair: its projection has no field for a secret
	// value, so a view holding it cannot disclose one. The plaintext half
	// lives in internal/credstore/resolve, which internal/archtest forbids
	// this side of the system from importing at all.
	Credentials credstore.Store

	// Render is the one template engine, handed to the Credential Types
	// view so its Test action renders an injector document the same way a
	// dispatch does. A second engine here would mean an author's test and
	// their run could disagree.
	Render render.Engine
}

// Registrar registers one view over the available ports.
//
// Views are registered through a function rather than an init() because a
// view backed by a port needs that port, and there is none at
// package-initialisation time. The composition root owns constructing the
// concrete drivers; this is where the list of what to register lives.
type Registrar func(Deps) error

// RegisterAll wires every built-in view, failing on the first refusal
// rather than starting with a half-registered UI.
//
// Failing fast matters more here than it looks. A half-registered UI is not
// a UI missing a page: it is a sidebar that silently lost an entry, which a
// user reads as "I do not have permission for that" rather than as a
// misconfiguration.
func RegisterAll(deps Deps) error {
	for _, register := range registrars() {
		if err := register(deps); err != nil {
			return err
		}
	}
	// Checked here rather than inside Register, because a view may
	// legitimately reference one that has not registered yet: the order
	// above is fixed, but nothing about a cross-view reference should
	// depend on it. Failing closed at startup is the point -- the
	// alternative is a link that 404s the first time somebody follows it.
	return view.CheckReferences()
}
