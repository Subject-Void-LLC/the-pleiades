// Package collectionscaffold generates a new namespaced Collection method
// package: a pkg/collection manifest registration, a stub function built
// on pkg/sdk.RunbookContext, and a starter table-driven test. It is Phase
// 33's ("The Scaffolds") collection generator, consumed by cmd/pleiades's
// `forge new-collection` subcommand, and the mechanism Phase 34 will
// invoke roughly 27+ times to dogfood-generate the actual module catalog.
//
// This package consumes the existing Registry pattern
// (pkg/collection.Register/MustRegister); it does not rebuild one. A
// generated package's own registration validates identically to
// pkg/collection.Register itself (at least one dot, non-empty segments,
// every capability already known to pkg/capability), so a name this
// generator accepts is guaranteed to register successfully the first time
// its package is imported.
//
// Reachability is a real, honestly-documented gap, not an oversight:
// pkg/collection is planning-time metadata only. No dispatcher anywhere in
// this codebase yet consumes pkg/collection or pkg/sdk.RunbookContext to
// actually call a registered method; the only real action executor
// (internal/engine/action.go) dispatches on a hardcoded switch over bare
// task.FQCN strings. A generated stub is real, buildable, testable Go
// code, but it is not reachable from any execution path until a later
// phase builds that dispatcher. See the generated package's own doc
// comment for the full explanation, repeated there so it travels with the
// generated code, not just this generator.
package collectionscaffold
