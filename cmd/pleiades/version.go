package main

import (
	"flag"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
)

// version is this build's version, from internal/buildinfo: the release
// version a release build is stamped with, or 0.0.0-dev+<commit> for a
// development build. The Runner reports the same value, and both hand it
// to the external Collection loader, so the CLI and a Runner cannot
// disagree about whether a program's engine version constraint is met.
var version = buildinfo.Version()

// runVersion prints the pleiades version and exits.
func runVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Println("pleiades " + version)
	return nil
}
