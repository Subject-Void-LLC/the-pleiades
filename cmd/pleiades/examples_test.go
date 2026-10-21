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
// such as ai_provider), or need a capability only onboarding records (a
// linux_server's container engine), cannot validate as shipped: the platform refuses to
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
		t.Run(strings.TrimPrefix(rb, filepath.Join("..", "..")+string(filepath.Separator)), func(t *testing.T) {
			out, err := runPleiades(t, project, "validate", filepath.Join("runbooks", filepath.Base(rb)))
			// A warning counts here: an example shows how a runbook is
			// written, header comment and all.
			if err != nil || !strings.Contains(out, "validate: no issues found") || strings.Contains(out, "[warning]") {
				t.Errorf("pleiades validate %s: err=%v\n%s", rb, err, out)
			}
		})
	}
}

// onboardedFirst names each example whose devices must be onboarded before
// it validates, and the test that does that and then validates it.
var onboardedFirst = map[string]string{
	"ai_lab":        "TestCLI_AIExampleChecksWithoutChanging",
	"ai_repair_lab": "TestCLI_AIRepairExampleRepairsAndRollsBack",
	"edge_lab":      "TestCLI_EdgeExampleReadsAndNamesEveryDifference",
	// A linux_server claims a container engine only once onboarding finds
	// one, and the scan methods require it.
	"scan_lab":    "TestCLI_ScanCheckWritesNothingAndTheRunDoesTheWork",
	"upgrade_ios": "TestCLI_UpgradeIOSExamplesAnswerRefuseAndRecover",
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
