// The engine runs native facts only: Ansible's fact names and values are a
// conversion concern, mapped by pkg/ansiblefacts for the converter and
// facts.gather, and a runbook the engine runs reads native keys
// (result.r1["os.distribution"]). An engine that reached the mapping could
// start answering an Ansible name at run time, so two spellings of one fact
// would both work and neither would be the contract. This test keeps the
// engine from reaching it at all.
package archtest

import (
	"sort"
	"testing"
)

// ansibleFactsPackage is the mapping between native and Ansible fact names.
const ansibleFactsPackage = modulePath + "/pkg/ansiblefacts"

// TestEngineNeverImportsTheAnsibleFactMapping asserts no package under
// internal/engine reaches pkg/ansiblefacts, directly or through another
// package.
func TestEngineNeverImportsTheAnsibleFactMapping(t *testing.T) {
	// The control: facts.gather does reach the mapping, so a listing that
	// found no dependency anywhere would fail here rather than pass below.
	reached := false
	for _, pkg := range goList(t, false, modulePath+"/internal/catalog/facts") {
		for _, dep := range pkg.Deps {
			reached = reached || dep == ansibleFactsPackage
		}
	}
	if !reached {
		t.Fatalf("internal/catalog/facts does not reach %s, so the query below cannot be trusted", ansibleFactsPackage)
	}

	// Deps rather than Imports: a helper package carrying the mapping into
	// the engine is the same reach as a direct import.
	var offenders []string
	engine := goList(t, false, modulePath+"/internal/engine/...")
	if len(engine) == 0 {
		t.Fatal("go list found no internal/engine package")
	}
	for _, pkg := range engine {
		for _, dep := range pkg.Deps {
			if dep == ansibleFactsPackage {
				offenders = append(offenders, pkg.ImportPath)
			}
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("these engine packages reach %s; the engine runs native facts only, and Ansible's names belong to the converter: %v",
			ansibleFactsPackage, offenders)
	}
}
