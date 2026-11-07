// Package main: `runner version`, the build's version and the external
// Collection protocols it speaks.
package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// printVersion writes `runner version` to w and returns the exit code: the
// version, the release's name for a release build, and the protocols, or
// with --json the one document `pleiades version --json` prints
// (clireport.VersionReport), so an operator checks a Runner's external
// Collection window before upgrading it.
func printVersion(w io.Writer, args []string) int {
	switch {
	case len(args) == 1 && args[0] == "--json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(clireport.NewVersionReport("runner", buildinfo.Version())); err != nil {
			return 1
		}
		return 0
	case len(args) > 0:
		_, _ = fmt.Fprintf(w, "usage: runner version [--json]\n")
		return 2
	}
	_, _ = fmt.Fprintln(w, "runner "+buildinfo.Version())
	if name := buildinfo.Codename(); name != "" {
		_, _ = fmt.Fprintln(w, name)
	}
	_, _ = fmt.Fprintln(w, "external collection protocols: "+external.SpokenText())
	return 0
}
