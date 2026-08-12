// Package kinds is the composition root for built-in launch kinds. It
// contains no logic: importing it blank-imports every kind package below
// it, whose init() functions are the only thing that populates the
// internal/launch registry.
//
// A separate package from internal/launch rather than a file inside it,
// because a kind imports the registry it registers into and the registry
// cannot import its kinds back. That is the same shape
// internal/inventory/plugins takes over internal/inventory/syncplugin, and
// for the same reason.
//
// This file exists from the registry's first day rather than being
// retrofitted, because the registered-but-unreachable failure has already
// happened three times here: FAILURE_PATTERNS.md #52, #96 and #101. An
// init() only runs if something imports the package it lives in, so a kind
// nobody imports compiles, passes its own tests, and is invisible to the
// running binary.
//
// Two kinds are registered, and PLAN.md Section 28 names seven. The other
// five have no executor. Registering a kind that cannot run would put an
// option on the template form that produces a job nothing ever picks up,
// which is precisely the failure this file's existence is a warning about.
// A kind arrives here when its adapter does.
package kinds

import (
	// The unconverted Ansible playbook, run by internal/adapters/legacy.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds/playbook"

	// This platform's own typed automation format, run by
	// internal/adapters/native.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds/runbook"
)
