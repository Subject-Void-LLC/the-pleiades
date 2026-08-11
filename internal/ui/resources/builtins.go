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
	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
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
	Inventory  inventory.Repository
	Factory    *inventory.ItemFactory
	Jobs       dispatch.JobStore
	Runbooks   runbook.Source
	Dispatcher *api.Dispatcher
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
	return nil
}
