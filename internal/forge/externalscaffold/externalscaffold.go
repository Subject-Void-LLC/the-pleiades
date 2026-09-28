// Package externalscaffold generates a complete, buildable external
// Collection program: a Go main package, built outside this repository,
// that The Pleiades runs as a child process beside itself and talks to over
// the pkg/external contract. It is the generator behind cmd/pleiades's
// `forge new-external` subcommand.
//
// An external Collection is the other half of what
// internal/forge/collectionscaffold makes. That package writes a method
// into internal/catalog, compiled into the pleiades binary, reachable
// only from inside this module. This one writes a separate program that
// imports only pkg/ (pkg/external, pkg/collection, pkg/sdk), so a
// third party can build it against a released Pleiades and install it
// without a fork or a rebuild. The program never runs on a managed
// device: The Pleiades starts it on the machine running `pleiades run` or on
// the Runner, hands it one task and the credential the credential manager
// resolved for it on stdin, and
// the method reaches the device the way a built-in method does, through
// pkg/sdk.Connect.
//
// # What it writes
//
// Four files, into one program directory:
//
//   - main.go: package main, whose main is one call to external.Main.
//   - <method>.go: the method's collection.Descriptor and a body that
//     actually works, so the program runs the moment it is built. The body
//     connects with sdk.Connect, runs `uname -a`, and records the output
//     as a stat. Because that body only reads, the descriptor honestly
//     declares check support with the same function as its Check, and says
//     in a comment what the author must do the moment the body writes.
//   - <method>_test.go: a test that runs external.Serve for "describe"
//     over buffers, which proves the descriptor passes collection.Register
//     and that the describe document names this method.
//   - README.md: how to build the program and where to install it.
//
// Unlike collectionscaffold's declared stub, the generated method is
// StatusImplemented from the start. external.Serve refuses a declared
// method outright, since a program exists to run code and a stub has
// none, so a scaffold that generated one would build a program that
// cannot even describe itself.
//
// # No go.mod
//
// The generator does not write a go.mod. The only correct require line
// for github.com/Subject-Void-LLC/the-pleiades names a version the go
// command computes itself (a pseudo-version from a commit, since the
// module has no tagged release yet), and a generator guessing at one
// would produce a module that fails on its first build with an error far
// from its cause. `go mod init` then `go get` writes the correct one, and
// the generated README says so. Leaving it out also lets the release gate
// build the program inside this module, against the pkg/ code in the same
// tree, with no replace directive involved.
//
// Like every forge generator, Generate performs no filesystem I/O. It
// returns paths relative to the program directory, and the caller
// (cmd/pleiades/forge_new_external.go) decides where to write them.
package externalscaffold
