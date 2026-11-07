package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// version is this build's version, from internal/buildinfo: the release
// version a release build is stamped with, or 0.0.0-dev+<commit> for a
// development build. The Runner reports the same value, and both hand it
// to the external Collection loader, so the CLI and a Runner cannot
// disagree about whether a program's engine version constraint is met.
var version = buildinfo.Version()

// runVersion prints the pleiades version and exits. With --json it prints
// clireport.VersionReport, the same facts plus the external Collection
// protocols this build speaks, as one document.
func runVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the version, release name and external Collection protocols as one JSON document")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(clireport.NewVersionReport("pleiades", version))
	}
	fmt.Println("pleiades " + version)
	// A release build also names its release, on a line of its own so the
	// first line stays what a script reads.
	if name := buildinfo.CodenameFor(version); name != "" {
		fmt.Println(name)
	}
	// Last, so the first line stays the version a script reads.
	fmt.Println("external collection protocols: " + external.SpokenText())
	return nil
}
