// The shipped examples, checked through the real binary.
package main_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestExamples_EveryRunbookBuildsAndValidates runs the real `pleiades
// validate` over every runbook the examples/ directory ships, against
// that example's own inventory, and requires a clean result. Nothing else
// loads these files, so before this test an example could stop parsing
// (seven of them carried metadata keys the parser now refuses) or pass
// parameters its method silently ignored (the upgrade_ios runbooks did),
// and nothing would say so until a reader copied it.
func TestExamples_EveryRunbookBuildsAndValidates(t *testing.T) {
	runbooks, err := filepath.Glob(filepath.Join("..", "..", "examples", "*", "pleiades", "runbooks", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(runbooks) < 14 {
		t.Fatalf("found %d example runbooks, want at least 14: the glob no longer matches the examples layout", len(runbooks))
	}
	for _, rb := range runbooks {
		project := filepath.Dir(filepath.Dir(rb))
		t.Run(strings.TrimPrefix(rb, filepath.Join("..", "..")+string(filepath.Separator)), func(t *testing.T) {
			out, err := runPleiades(t, project, "validate", filepath.Join("runbooks", filepath.Base(rb)))
			if err != nil || !strings.Contains(out, "validate: no issues found") {
				t.Errorf("pleiades validate %s: err=%v\n%s", rb, err, out)
			}
		})
	}
}
