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
// A generated package's registration is genuinely reachable: a runbook
// task naming its FQCN reaches engine.NewCollectionActionExecutor's real
// dispatch path (cmd/pleiades/run.go), which refuses with its own
// "declared but not implemented" error because the generated Manifest's
// Status is StatusDeclared, before ever calling the generated stub
// function itself. See the generated package's own doc comment for the
// full explanation, repeated there so it travels with the generated
// code, not just this generator.
//
// The generated manifest states Manifest.SupportsCheck explicitly, and
// always as false. collection.Register refuses check support on a method
// that is not implemented, so false is the only value a declared stub can
// carry. It is written out rather than left to the zero value so the
// comment beside it can tell the implementer how to add check support
// once the method is real: set it to true and add a Check function that
// reads, predicts, changes nothing, and never records an inverse.
package collectionscaffold
