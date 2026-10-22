// The census rule: a change that can alter what `pleiades validate` says
// about a runbook already in the tree also selects cmd/pleiades, the
// package that validates every one of them.
package main

import (
	"fmt"
	"strings"
)

// censusPackage is the package, relative to the module root, holding the
// census of existing runbooks: the examples test
// (TestExamples_EveryRunbookBuildsAndValidates), the release gates and the
// migrate gate, each of which validates or runs real runbooks through the
// real binary.
const censusPackage = "cmd/pleiades"

// censusTriggers are the paths, relative to the module root, whose change
// can alter what validation or a run decides about a runbook that is
// already in the tree. A change under any of them adds censusPackage to
// the scope.
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
// it, so it cannot change what cmd/pleiades sees.
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

// addCensus adds the census package's import path to found when a changed
// file triggers it, and returns the file that did ("" when none did). A
// trigger firing in a module with no census package is refused, since
// that means the package moved and the rule would otherwise stop selecting
// anything without a word.
func addCensus(files []string, packages map[string]string, found map[string]bool) (string, error) {
	file, ok := censusTrigger(files)
	if !ok {
		return "", nil
	}
	ip, ok := packages[censusPackage]
	if !ok {
		return "", fmt.Errorf("%s changes what validation decides, but %s, which validates every runbook, is not a package here", file, censusPackage)
	}
	found[ip] = true
	return file, nil
}
