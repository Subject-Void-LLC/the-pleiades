// Package containerengine: what a container's command line reports. A
// command line's arguments often carry a secret (redis-server --requirepass
// X, a --password= flag, a token), and container.list reads every container
// on the engine, including ones no runbook made, so a method reports the
// program and how many arguments follow it, never an argument, the way an
// environment is reported by its names alone.
package containerengine

import "strings"

// ArgvProgram and ArgvArgCount are the keys of an ArgvSummary.
const (
	// ArgvProgram is the program: argv[0], or empty when there is none.
	ArgvProgram = "program"
	// ArgvArgCount is how many arguments follow the program.
	ArgvArgCount = "arg_count"
)

// ArgvSummary is what a container's command or entrypoint reports: its
// program (argv[0]) and the number of arguments after it. An empty argv is
// an empty program and no arguments.
func ArgvSummary(argv []string) map[string]any {
	program := ""
	if len(argv) > 0 {
		program = argv[0]
	}
	return map[string]any{ArgvProgram: program, ArgvArgCount: max(len(argv)-1, 0)}
}

// CommandLineSummary is ArgvSummary for the one-line command an engine's
// container list gives (the program, then its arguments joined by spaces):
// the line is split at whitespace, so an argument holding a space counts
// once per word.
func CommandLineSummary(line string) map[string]any {
	return ArgvSummary(strings.Fields(line))
}
