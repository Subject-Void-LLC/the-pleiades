// Package plugins is the composition root for built-in sync plugins. It
// contains no logic: importing it blank-imports every plugin package below
// it, whose init() functions are the only thing that populates the
// syncplugin registry.
//
// This file exists from the registry's first day rather than being
// retrofitted, because the registered-but-unreachable failure has already
// happened twice in this codebase. FAILURE_PATTERNS.md #52 records the
// generated Collection catalog compiling and unit-testing cleanly while
// being completely unreachable from the real binary, for exactly this
// reason: an init() only runs if something imports the package, and each
// generated package's own test binary was the only thing that did.
// LESSONS_LEARNED.md #56 generalizes it. internal/archtest is where the
// drift is caught.
//
// Unlike internal/catalog/builtins.go, this list is hand-maintained rather
// than regenerated, matching internal/inventory/builtins.go's treatment of
// device types.
package plugins

import (
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins/aws"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins/catalystcenter"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins/staticyaml"
)
