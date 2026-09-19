// Package archtest: where the external Collection loader may be imported from.
package archtest

import (
	"strings"
	"testing"
)

// loaderPath is the external Collection loader's import path.
const loaderPath = modulePath + "/internal/loader"

// TestEngineNeverReachesTheLoader is Phase 45's adversarial audit, made a
// rule: the engine gained no knowledge of external Collections. An
// external method reaches the engine as an ordinary registered
// collection.Descriptor, so nothing under internal/engine may import the
// loader, directly or through anything it imports. A loader branch inside
// the engine would throw away the property that a Collection result is
// indistinguishable to the DAG whoever provided it.
func TestEngineNeverReachesTheLoader(t *testing.T) {
	pkgs := goList(t, true, modulePath+"/internal/engine/...")
	if len(pkgs) == 0 {
		t.Fatalf("go list matched no packages under %s/internal/engine/..., so this rule examined nothing", modulePath)
	}
	for _, pkg := range pkgs {
		for _, dep := range pkg.Deps {
			if dep == loaderPath {
				t.Errorf("%s depends on %s: the engine must not know external Collections exist; "+
					"they reach it as registered descriptors, wired in a cmd/ composition root", pkg.ImportPath, loaderPath)
			}
		}
	}
}

// TestOnlyCompositionRootsImportTheLoader keeps the loader where Section
// 25 puts every concrete choice: in a cmd/ composition root. Loading
// third-party programs is a deployment decision (which directory, which
// engine version), and a library package importing the loader would make
// that decision for every binary that links it.
//
// The control is the second half: at least one composition root must be
// found importing it, or the query is misaimed and the first half proved
// nothing.
func TestOnlyCompositionRootsImportTheLoader(t *testing.T) {
	pkgs := goList(t, false, modulePath+"/...")
	if len(pkgs) == 0 {
		t.Fatal("go list matched no packages in the module, so this rule examined nothing")
	}
	var roots []string
	for _, pkg := range pkgs {
		for _, imp := range pkg.Imports {
			if imp != loaderPath {
				continue
			}
			if !strings.HasPrefix(pkg.ImportPath, modulePath+"/cmd/") {
				t.Errorf("%s imports %s; only a cmd/ composition root may load external Collections", pkg.ImportPath, loaderPath)
				continue
			}
			roots = append(roots, pkg.ImportPath)
		}
	}
	if len(roots) == 0 {
		t.Errorf("no cmd/ composition root imports %s, so either external Collections are wired nowhere or this query is misaimed", loaderPath)
	}
}
