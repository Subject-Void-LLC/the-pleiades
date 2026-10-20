// The shipped examples, checked through the real binary.
package main_test

import (
	"os"
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
//
// An example whose devices start discovered (a type onboarding proves,
// such as ai_provider) cannot validate as shipped: the platform refuses to
// target a device until onboarding records what it proved, and shipping a
// discovery would claim a proof nobody made. Such an example is named in
// onboardedFirst with the test that onboards its devices against real
// servers and then validates it, which is the stronger check.
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
		if by, ok := onboardedFirst[filepath.Base(filepath.Dir(project))]; ok {
			if !testExists(t, by) {
				t.Errorf("%s is said to be validated by %s, which no test file declares", rb, by)
			}
			t.Logf("%s is validated after onboarding, by %s", rb, by)
			continue
		}
		name := strings.TrimPrefix(rb, filepath.Join("..", "..")+string(filepath.Separator))
		t.Run(name, func(t *testing.T) {
			out, err := runPleiades(t, project, "validate", filepath.Join("runbooks", filepath.Base(rb)))
			// A warning counts here: an example shows how a runbook is
			// written, header comment and all. The one exception is a
			// warning named in expectedWarnings for this file, which must
			// still be there, so the exception ends when its cause does.
			expected, allowed := expectedWarnings[filepath.ToSlash(name)]
			rest := out
			if allowed {
				if !strings.Contains(out, expected.warning) {
					t.Errorf("%s no longer warns %q, so remove it from expectedWarnings (%s)\n%s", rb, expected.warning, expected.why, out)
				}
				rest = strings.ReplaceAll(out, expected.warning, "")
			}
			if err != nil || !strings.Contains(out, "validate: no issues found") || strings.Contains(rest, "[warning]") {
				t.Errorf("pleiades validate %s: err=%v\n%s", rb, err, out)
			}
		})
	}
}

// expectedWarnings names each example runbook allowed one warning, the
// warning's opening, and why the example keeps what causes it.
var expectedWarnings = map[string]struct{ warning, why string }{
	"examples/upgrade_ios/pleiades/runbooks/upgrade_ios_xe.yaml": {
		warning: "[warning] handlers: tasks[0]'s rescue:",
		why: "the example is the native form of an Ansible playbook whose rescue records the failure and prints rollback " +
			"instructions, and its README says rescue does not run yet; the rescue stays so that it runs once the engine runs it",
	},
}

// onboardedFirst names each example whose devices must be onboarded before
// it validates, and the test that does that and then validates it.
var onboardedFirst = map[string]string{
	"ai_lab":   "TestCLI_AIExampleChecksWithoutChanging",
	"edge_lab": "TestCLI_EdgeExampleReadsAndNamesEveryDifference",
}

// testExists reports whether a test file in this package declares the test
// function name, so an exemption cannot outlive the test it relies on.
func testExists(t *testing.T, name string) bool {
	t.Helper()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "func "+name+"(t *testing.T)") {
			return true
		}
	}
	return false
}
