// Package catalogdata is the single source of truth for the native module
// catalog docs/hephaestus.md describes: every collectionscaffold.Config and
// devicescaffold.Config Phase 34 hands to the real pleiades forge CLI.
//
// After editing anything in this package, regenerate the catalog by
// running:
//
//	go generate ./internal/forge/catalogdata
//
// This shells out to the actual `pleiades forge new-collection`/
// `new-device` commands (tools/gencatalog), never calling
// collectionscaffold.Generate/devicescaffold.Generate directly: Phase 34's
// own Pattern Entry Gate requires the real CLI surface be exercised, not
// just the library underneath it. Generated output lands under
// internal/catalog/ and internal/inventory/devices/{windows,aws}/ and is
// committed like any other generated source: never hand-edited. If the
// generated shape is wrong, the fix belongs in this package's data or in
// Phase 33's collectionscaffold/devicescaffold templates, never in the
// generated files themselves.
package catalogdata

//go:generate go run ../../../tools/gencatalog
