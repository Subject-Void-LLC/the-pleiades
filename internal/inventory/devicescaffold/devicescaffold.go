// Package devicescaffold generates a new vendor device-type package
// mirroring internal/inventory/devices/cisco and devices/linux: a
// record.Base embed, a capability-baseline constructor, HasCapability, and
// a starter test. It is Phase 33's ("The Scaffolds") device generator,
// consumed by cmd/pleiades's `forge new-device` subcommand.
//
// This package consumes the existing Registry pattern
// (internal/inventory/record's RegisterType/AllTypes); it does not rebuild
// one. A generated package imports only internal/inventory/record,
// pkg/capability, pkg/inventory, and pkg/policy, exactly as the two
// hand-written vendor packages do, so it never risks the import cycle
// internal/inventory/record's own doc comment describes.
//
// Reachability is a real, honestly-documented gap, not an oversight: a
// generated device type registers itself via its own init(), but nothing
// calls that init() until a human adds a blank import of the generated
// package to internal/inventory/builtins.go (or their own composition
// root). Generate never edits that file itself. See the generated
// package's own doc comment for the full explanation, repeated there so
// it travels with the generated code, not just this generator.
package devicescaffold
