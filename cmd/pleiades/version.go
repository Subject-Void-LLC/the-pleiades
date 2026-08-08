package main

import (
	"flag"
	"fmt"
)

// version is overridden at build time with -ldflags "-X main.version=...".
// "dev" until a real release process exists to set it: nothing has
// shipped a tagged release yet (see pkg/collection.Doc.SinceVersion's own
// doc comment, which notes the same thing from the module-catalog side).
var version = "dev"

// runVersion prints the pleiades version and exits.
func runVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Println("pleiades " + version)
	return nil
}
