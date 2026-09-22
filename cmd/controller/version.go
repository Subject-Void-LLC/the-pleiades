// This file is `controller version`: the build's version, the one fact an
// operator needs before and after every upgrade and could not get from this
// binary until now (the runner and the CLI have always answered it).
package main

import (
	"fmt"
	"io"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
)

// versionCommand is the subcommand that prints the build's version.
const versionCommand = "version"

// isVersionCommand reports whether args ask for the version. It is checked
// before the admin guard, which treats any non-flag word as a subcommand.
func isVersionCommand(args []string) bool {
	return len(args) > 0 && args[0] == versionCommand
}

// runVersion prints the version (internal/buildinfo: a release build's own
// version, or 0.0.0-dev+<commit> for anything else) and returns exit code 0.
func runVersion(out io.Writer) int {
	fmt.Fprintln(out, buildinfo.Version())
	return 0
}
