// Package archtest: the engine reaches devices' facts through its own port
// (Phase 41).
package archtest

import (
	"strings"
	"testing"
)

// TestEngineReachesFactsOnlyThroughItsPort: internal/engine may use
// internal/facts (the shared rules, which import no store), but never the
// Controller's fact store, internal/facts/entstore, by any path; a store is
// wired in a cmd/ composition root behind engine.FactRecorder, as the journal
// is behind engine.Journal. (The engine reaches the ent layer through other
// packages, internal/inventory among them, which this does not judge; that it
// imports none directly is TestEngineNeverImportsPersistence's rule.)
func TestEngineReachesFactsOnlyThroughItsPort(t *testing.T) {
	pkgs := goList(t, false, modulePath+"/internal/engine")
	if len(pkgs) == 0 {
		t.Fatalf("go list matched no packages for %s/internal/engine, so this rule examined nothing", modulePath)
	}
	sawFacts := false
	for _, pkg := range pkgs {
		for _, dep := range append(append([]string{}, pkg.Imports...), pkg.Deps...) {
			switch {
			case dep == modulePath+"/internal/facts":
				sawFacts = true
			case strings.HasPrefix(dep, modulePath+"/internal/facts/"):
				t.Errorf("%s reaches %s: the engine stores facts only through engine.FactRecorder", pkg.ImportPath, dep)
			}
		}
	}
	if !sawFacts {
		t.Error("internal/engine no longer reaches internal/facts, so this rule is about nothing: update or remove it")
	}
}
