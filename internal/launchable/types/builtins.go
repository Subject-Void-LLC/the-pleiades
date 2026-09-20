// Package types is the composition root for built-in launchable types. It
// contains no logic: importing it blank-imports every type package below it,
// whose init() functions are the only thing that populates the
// internal/launchable registry.
//
// A separate package from internal/launchable rather than a file inside it,
// because a type imports the registry it registers into and the registry
// cannot import its types back. That is the shape internal/launch/kinds and
// internal/inventory/plugins already take, for the same reason.
//
// The registered-but-unreachable failure has happened three times in this
// repository (FAILURE_PATTERNS.md #52, #96, #101), and an init() only runs if
// something imports the package it lives in. Here it would be worse than
// invisible: internal/launchable.NewRouter refuses to build when a registered
// type has no launcher, so a type imported without being composed stops the
// Controller at startup, and one composed without being imported is refused
// the same way. An archtest asserts cmd/controller imports this package.
//
// Two types are registered. The others AWX has are absent for a reason
// rather than pending: an inventory source has no Controller-side entity yet
// (a sync runs from the CLI), and a workflow does not exist (Phase 27). A
// type registered before something can run it would put an option on the
// schedule form that produces nothing.
package types

import (
	// The job template, run by the Controller's Dispatcher.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launchable/types/jobtemplate"

	// The project, whose run is a sync, performed by internal/project's
	// Runner.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launchable/types/projectsync"
)
