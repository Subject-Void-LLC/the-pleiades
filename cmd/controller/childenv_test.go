// Package main_test's shared child-process environment, kept in a file
// with no build constraint.
//
// It lives here rather than beside the setup command's own gate because
// that file is //go:build linux: its other tests drive real pseudo
// terminals, which is a POSIX facility. This helper has nothing to do
// with terminals and nothing platform-specific in it, and leaving it
// there meant any test on any platform that wanted a clean child
// environment silently depended on a Linux-only file. The mesh URL
// refusal gate did exactly that and broke `go vet` on Windows and macOS
// while passing every local check, because everything run locally was
// Linux.
package main_test

import (
	"os"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// setupEnv is the child environment: this process's, minus every variable
// setup owns (a developer's shell might export one), plus DB_PATH.
//
// Stripping rather than overriding is what makes a gate hermetic: an
// exported JWT_SECRET or MASTER_ENCRYPTION_KEY in the developer's own
// shell would otherwise reach the child and decide the outcome of a test
// about what happens when it is absent.
func setupEnv(dbPath string) []string {
	owned := map[string]bool{"DB_DSN": true, "DB_PATH": true}
	for _, n := range setup.ComposeVariables() {
		owned[n] = true
	}
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !owned[name] {
			env = append(env, kv)
		}
	}
	return append(env, "DB_PATH="+dbPath, "OTEL_TRACES_EXPORTER=none")
}
