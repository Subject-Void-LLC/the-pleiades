// The census rule: a change that can alter what validation decides about a
// runbook already in the tree also selects the packages that validate or
// run those runbooks.
package main

import (
	"fmt"
	"strings"
)

// censusPackages are the packages, relative to the module root, that hold
// the census of existing runbooks: each validates or runs runbooks that
// are already in the tree, through internal/validate.
//
//   - cmd/pleiades: the examples test
//     (TestExamples_EveryRunbookBuildsAndValidates), the release gates and
//     the migrate gate, through the real binary.
//   - cmd/runner: its release gates run runbooks through the Runner, which
//     validates each one before it runs it.
//   - internal/adapters/native: the Runner's validation itself
//     (validate.go) and its tests' runbooks.
//   - internal/forge/playbook: it validates the converted twin under its
//     testdata.
//
// tests/e2e also runs runbooks through the real binaries and is left out
// on purpose: it takes about fourteen minutes alone and this machine's
// memory cannot spare it before every push. CI still runs it.
var censusPackages = []string{
	"cmd/pleiades",
	"cmd/runner",
	"internal/adapters/native",
	"internal/forge/playbook",
}

// censusTriggers are the paths, relative to the module root, whose change
// can alter what validation or a run decides about a runbook that is
// already in the tree. A change under any of them adds every census
// package to the scope.
//
// prscope selects only the packages a branch changes, so a branch that
// made validation stricter passed pr-check and failed CI in cmd/pleiades,
// where the runbooks it now refused live (FAILURE_PATTERNS 559, a repeat
// of 550). A lesson written as a sentence did not hold under pressure, so
// the census is selected here instead of remembered.
//
// Each entry and why:
//   - internal/validate: the rules themselves.
//   - internal/engine: the builder and DAG the rules read, and the run-time
//     gates the release gates reach.
//   - pkg/collection: CheckCapabilities, CheckTransports and the Descriptor
//     every rule compares a device against.
//   - pkg/capability: the transport vocabulary and the capability reaching
//     each one (ReachedBy), the other half of the transport check.
//   - internal/catalog: each method's manifest (required capabilities,
//     transports, parameters, check support) is what the rules compare
//     against, and the release gates run the methods' bodies. A manifest
//     shares its files with its method's body, so no narrower path
//     separates the two.
//   - internal/inventory/devices: which capabilities each device type
//     declares, the device half of the capability check.
//   - examples: the runbooks and inventories the examples test validates.
//     No package sits above them, so without this entry a change to an
//     example selected nothing at all.
var censusTriggers = []string{
	"internal/validate",
	"internal/engine",
	"pkg/collection",
	"pkg/capability",
	"internal/catalog",
	"internal/inventory/devices",
	"examples",
}

// censusTrigger returns the first changed file, slash-separated and
// relative to the module root, that lies under a census trigger, and
// whether one does. A test file never triggers: no other package compiles
// it, so it cannot change what a census package sees.
func censusTrigger(files []string) (string, bool) {
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		for _, trigger := range censusTriggers {
			if file == trigger || strings.HasPrefix(file, trigger+"/") {
				return file, true
			}
		}
	}
	return "", false
}

// addCensus adds every census package's import path to found when a
// changed file triggers the census, and returns the file that did (""
// when none did). A trigger firing in a module missing a census package is
// refused, since that means the package moved and the rule would otherwise
// stop selecting it without a word.
func addCensus(files []string, packages map[string]string, found map[string]bool) (string, error) {
	file, ok := censusTrigger(files)
	if !ok {
		return "", nil
	}
	for _, dir := range censusPackages {
		ip, ok := packages[dir]
		if !ok {
			return "", fmt.Errorf("%s changes what validation decides, but %s, which validates existing runbooks, is not a package here", file, dir)
		}
		found[ip] = true
	}
	return file, nil
}
