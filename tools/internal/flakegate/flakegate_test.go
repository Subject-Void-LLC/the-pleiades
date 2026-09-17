package flakegate

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// TestFlakyPackagesJSONNamesRealPackages proves every entry in the real,
// committed flaky-packages.json (not a fixture) is spelled as a genuine
// Go import path this module actually has, so a typo in the file (which
// would silently mean "this package's failures are never tolerated," the
// opposite of what an entry is meant to do) fails the build instead of
// being discovered the next time that exact package flakes.
func TestFlakyPackagesJSONNamesRealPackages(t *testing.T) {
	tolerated, err := LoadTolerated(filepath.Join("..", "..", "..", "flaky-packages.json"))
	if err != nil {
		t.Fatalf("loading flaky-packages.json: %v", err)
	}
	if len(tolerated) == 0 {
		t.Fatal("flaky-packages.json has no entries; if that is now genuinely true, this test should be removed along with the file")
	}
	const modulePrefix = "github.com/Subject-Void-LLC/the-pleiades/"
	for path, entry := range tolerated {
		if entry.Reason == "" {
			t.Errorf("%s has no reason recorded", path)
		}
		if len(path) <= len(modulePrefix) || path[:len(modulePrefix)] != modulePrefix {
			t.Errorf("%s does not start with this module's own path %s", path, modulePrefix)
			continue
		}
		dir := filepath.Join("..", "..", "..", path[len(modulePrefix):])
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("%s does not resolve to a real directory in this module (%v)", path, err)
		}
	}
}

func TestClassify_TestFailureInToleratedPackageIsWarned(t *testing.T) {
	events := []Event{
		{Action: "fail", Package: "tests/e2e", Test: "TestGrandIntegration"},
	}
	tolerated := map[string]Tolerance{"tests/e2e": {Reason: "known Docker contention"}}

	hard, warned := Classify(events, tolerated)
	if len(hard) != 0 {
		t.Errorf("hard = %+v, want none", hard)
	}
	want := []Failure{{Package: "tests/e2e", Test: "TestGrandIntegration"}}
	if !reflect.DeepEqual(warned, want) {
		t.Errorf("warned = %+v, want %+v", warned, want)
	}
}

func TestClassify_TestFailureOutsideToleratedPackagesIsHard(t *testing.T) {
	events := []Event{
		{Action: "fail", Package: "internal/dispatch", Test: "TestWorker_HandleJobRequested_DispatchesHealthyDevice"},
	}
	tolerated := map[string]Tolerance{"tests/e2e": {Reason: "known Docker contention"}}

	hard, warned := Classify(events, tolerated)
	if len(warned) != 0 {
		t.Errorf("warned = %+v, want none", warned)
	}
	want := []Failure{{Package: "internal/dispatch", Test: "TestWorker_HandleJobRequested_DispatchesHealthyDevice"}}
	if !reflect.DeepEqual(hard, want) {
		t.Errorf("hard = %+v, want %+v", hard, want)
	}
}

func TestClassify_BuildFailureIsAlwaysHard(t *testing.T) {
	events := []Event{{Action: "build-fail", Package: "tests/e2e"}}
	tolerated := map[string]Tolerance{"tests/e2e": {Reason: "known Docker contention"}}

	hard, warned := Classify(events, tolerated)
	if len(warned) != 0 {
		t.Errorf("warned = %+v, want none", warned)
	}
	want := []Failure{{Package: "tests/e2e", Test: ""}}
	if !reflect.DeepEqual(hard, want) {
		t.Errorf("hard = %+v, want %+v", hard, want)
	}
}

func TestClassify_FailedBuildMarkerIsAlwaysHard(t *testing.T) {
	events := []Event{{Action: "fail", Package: "tests/e2e", FailedBuild: "tests/e2e"}}
	tolerated := map[string]Tolerance{"tests/e2e": {Reason: "known Docker contention"}}

	hard, warned := Classify(events, tolerated)
	if len(warned) != 0 {
		t.Errorf("warned = %+v, want none", warned)
	}
	want := []Failure{{Package: "tests/e2e", Test: ""}}
	if !reflect.DeepEqual(hard, want) {
		t.Errorf("hard = %+v, want %+v", hard, want)
	}
}

func TestClassify_PackageFailWithNoTestFailureIsHard(t *testing.T) {
	events := []Event{{Action: "fail", Package: "tests/e2e"}}
	tolerated := map[string]Tolerance{"tests/e2e": {Reason: "known Docker contention"}}

	hard, warned := Classify(events, tolerated)
	if len(warned) != 0 {
		t.Errorf("warned = %+v, want none", warned)
	}
	want := []Failure{{Package: "tests/e2e", Test: ""}}
	if !reflect.DeepEqual(hard, want) {
		t.Errorf("hard = %+v, want %+v", hard, want)
	}
}

func TestClassify_MixOfToleratedAndHardFailuresSeparatesCorrectly(t *testing.T) {
	events := []Event{
		{Action: "fail", Package: "tests/e2e", Test: "TestGrandIntegration"},
		{Action: "fail", Package: "internal/dispatch", Test: "TestWorker_HandleJobRequested_DispatchesHealthyDevice"},
		{Action: "pass", Package: "pkg/wire", Test: "TestDispatchPayload_JSONRoundTrip"},
	}
	tolerated := map[string]Tolerance{"tests/e2e": {Reason: "known Docker contention"}}

	hard, warned := Classify(events, tolerated)
	if len(hard) != 1 || hard[0].Package != "internal/dispatch" {
		t.Errorf("hard = %+v, want exactly the internal/dispatch failure", hard)
	}
	if len(warned) != 1 || warned[0].Package != "tests/e2e" {
		t.Errorf("warned = %+v, want exactly the tests/e2e failure", warned)
	}
}

func TestClassify_NoFailuresIsClean(t *testing.T) {
	events := []Event{{Action: "pass", Package: "pkg/wire", Test: "TestDispatchPayload_JSONRoundTrip"}}

	hard, warned := Classify(events, map[string]Tolerance{})
	if len(hard) != 0 || len(warned) != 0 {
		t.Errorf("hard = %+v, warned = %+v, want both empty", hard, warned)
	}
}

func TestFailedPackages(t *testing.T) {
	fs := []Failure{
		{Package: "tests/e2e", Test: "TestA"},
		{Package: "tests/e2e", Test: "TestB"},
		{Package: "internal/dispatch", Test: "TestC"},
	}
	got := FailedPackages(fs)
	want := map[string]bool{"tests/e2e": true, "internal/dispatch": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FailedPackages() = %+v, want %+v", got, want)
	}
}

// TestClassify_ANarrowedEntryOnlyToleratesTheTestsItNames is the rule that
// would have caught FAILURE_PATTERNS #222, and it is worth stating what it
// cost not to have.
//
// Every job in the system hung in "running" forever. Four tests/e2e tests
// failed on it in five consecutive push-gate runs, and every one of those
// runs printed "testgate: passed (warnings above)", because one entry
// covered the whole package. The entry's own prose named exactly one test
// as the observed flake; the code read only the import path.
func TestClassify_ANarrowedEntryOnlyToleratesTheTestsItNames(t *testing.T) {
	events := []Event{
		{Action: "fail", Package: "tests/e2e", Test: "TestGrandIntegration_EachKindReachesItsOwnAdapter"},
		{Action: "fail", Package: "tests/e2e", Test: "TestCredentialInjection_ReachesARealPlaybookThroughTheRealBinaries"},
	}
	tolerated := map[string]Tolerance{"tests/e2e": {
		Reason: "container contention on the named test",
		Tests:  map[string]bool{"TestGrandIntegration_EachKindReachesItsOwnAdapter": true},
	}}

	hard, warned := Classify(events, tolerated)

	wantWarned := []Failure{{Package: "tests/e2e", Test: "TestGrandIntegration_EachKindReachesItsOwnAdapter"}}
	if !reflect.DeepEqual(warned, wantWarned) {
		t.Errorf("warned = %+v, want %+v", warned, wantWarned)
	}
	wantHard := []Failure{{Package: "tests/e2e", Test: "TestCredentialInjection_ReachesARealPlaybookThroughTheRealBinaries"}}
	if !reflect.DeepEqual(hard, wantHard) {
		t.Errorf("hard = %+v, want %+v: a test nobody has ever seen flake was tolerated anyway", hard, wantHard)
	}
}

// TestClassify_AnUnnarrowedEntryStillCoversItsWholePackage keeps the
// migration honest.
//
// An entry with no test list is not a second policy, it is an entry nobody
// has narrowed yet, and it must behave exactly as every entry did before
// the field existed. Without this, adding the field would silently turn
// eighteen unnarrowed entries into hard failures at the next flake.
func TestClassify_AnUnnarrowedEntryStillCoversItsWholePackage(t *testing.T) {
	events := []Event{
		{Action: "fail", Package: "internal/lock", Test: "TestSomethingNobodyListed"},
	}
	tolerated := map[string]Tolerance{"internal/lock": {Reason: "starts seven real containers"}}

	hard, warned := Classify(events, tolerated)
	if len(hard) != 0 {
		t.Errorf("hard = %+v, want none: an unnarrowed entry must behave as it did before the field existed", hard)
	}
	if len(warned) != 1 {
		t.Errorf("warned = %+v, want the one failure", warned)
	}
}

// TestTolerance_CoversMatchesOnTheTopLevelTestName is why the match is not
// a plain string equality.
//
// go test reports a failing subtest as "Parent/Sub". An entry recording
// that a test flakes is recording something about that test, not about
// which of its cases lost the race on the day somebody wrote the entry, so
// a named parent covers its subtests. The negative half matters as much:
// a DIFFERENT test that merely starts with the same characters must not be
// covered, which a prefix match would get wrong.
func TestTolerance_CoversMatchesOnTheTopLevelTestName(t *testing.T) {
	entry := Tolerance{Tests: map[string]bool{"TestGrandIntegration": true}}

	covered := []string{"TestGrandIntegration", "TestGrandIntegration/runbook_kind", "TestGrandIntegration/a/b"}
	for _, name := range covered {
		if !entry.Covers(name) {
			t.Errorf("Covers(%q) = false, want true", name)
		}
	}
	// Not a prefix match: this is a separate test with its own history.
	if entry.Covers("TestGrandIntegration_EachKindReachesItsOwnAdapter") {
		t.Error("Covers matched a different test by prefix, so naming one test would tolerate its whole family")
	}
}

// TestFlakyPackagesJSON_NarrowedEntriesNameTestsThatLookLikeTests is a
// spelling guard, the same one TestFlakyPackagesJSONNamesRealPackages is
// for import paths.
//
// A misspelled test name in a narrowed entry tolerates nothing, which is
// the opposite of what writing it down was for, and it fails silently: the
// entry looks present and the failure goes hard at the worst moment.
func TestFlakyPackagesJSON_NarrowedEntriesNameTestsThatLookLikeTests(t *testing.T) {
	tolerated, err := LoadTolerated(filepath.Join("..", "..", "..", "flaky-packages.json"))
	if err != nil {
		t.Fatalf("loading flaky-packages.json: %v", err)
	}
	for path, entry := range tolerated {
		for name := range entry.Tests {
			if !strings.HasPrefix(name, "Test") {
				t.Errorf("%s names %q, which is not a Go test name", path, name)
			}
			if strings.Contains(name, "/") {
				t.Errorf("%s names %q; entries name the top-level test, and its subtests are covered automatically", path, name)
			}
		}
	}
}

// TestTopLevel is the unit the isolation pass re-runs: a subtest cannot be
// run without its parent, and the parent owns the fixture a contended
// machine failed to build.
func TestTopLevel(t *testing.T) {
	cases := map[string]string{
		"TestThing":               "TestThing",
		"TestThing/a_case":        "TestThing",
		"TestThing/a_case/deeper": "TestThing",
		"FuzzAgentPayload/seed#0": "FuzzAgentPayload",
		"":                        "",
	}
	for in, want := range cases {
		if got := TopLevel(in); got != want {
			t.Errorf("TopLevel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestIsolate_ABuildFailureIsNeverReRun pins the one shape that is always
// confirmed: a package that does not compile did not lose a race.
func TestIsolate_ABuildFailureIsNeverReRun(t *testing.T) {
	confirmed, contention, err := Isolate(
		[]Failure{{Package: "example.com/broken"}}, nil, nil)
	if err != nil {
		t.Fatalf("Isolate: %v", err)
	}
	if len(contention) != 0 {
		t.Errorf("a build failure was tolerated as contention: %+v", contention)
	}
	if len(confirmed) != 1 || confirmed[0].Package != "example.com/broken" {
		t.Errorf("confirmed = %+v, want the build failure", confirmed)
	}
}

// TestIsolate_TooManyFailuresAreNotReRun guards the bound.
//
// A run with more failures than the cap is not a contended machine losing a
// race; it is a change that broke something, and re-running them one at a
// time would cost more wall clock than the suite did. Past the bound they
// are reported as they stand, which is the stricter answer.
func TestIsolate_TooManyFailuresAreNotReRun(t *testing.T) {
	var many []Failure
	for i := 0; i < 40; i++ {
		many = append(many, Failure{
			Package: "example.com/pkg",
			Test:    "TestNumber" + strconv.Itoa(i),
		})
	}

	var echo bytes.Buffer
	confirmed, contention, err := Isolate(many, nil, &echo)
	if err != nil {
		t.Fatalf("Isolate: %v", err)
	}
	if len(contention) != 0 {
		t.Errorf("%d failures were tolerated without being re-run", len(contention))
	}
	if len(confirmed) != len(many) {
		t.Errorf("confirmed %d of %d failures", len(confirmed), len(many))
	}
	// And it says so, rather than silently applying a cap: a gate that
	// truncates quietly reads as "everything was checked".
	if !strings.Contains(echo.String(), "reporting them as they stand") {
		t.Errorf("the bound was applied silently: %q", echo.String())
	}
}

// TestIsolate_OneReRunPerParentTest proves a parent and its subtests are
// one question, not four.
func TestIsolate_OneReRunPerParentTest(t *testing.T) {
	// A package that does not exist, so every re-run fails fast and lands
	// in confirmed; what is under test here is how many targets there are.
	failures := []Failure{
		{Package: "example.com/nope", Test: "TestThing"},
		{Package: "example.com/nope", Test: "TestThing/one"},
		{Package: "example.com/nope", Test: "TestThing/two"},
		{Package: "example.com/nope", Test: "TestOther"},
	}
	confirmed, _, err := Isolate(failures, nil, nil)
	if err != nil {
		t.Fatalf("Isolate: %v", err)
	}
	if len(confirmed) != 2 {
		t.Errorf("Isolate produced %d targets from four failures across two parents: %+v",
			len(confirmed), confirmed)
	}
}

// TestFirstSentence keeps a tolerated failure's line readable.
//
// The reasons in flaky-packages.json are paragraphs on purpose, and
// printing one in full per failure buries the failures under the
// explanations.
func TestFirstSentence(t *testing.T) {
	long := "CLASS: HARNESS-ONLY (classified 2026-09-15). The rest of this is several hundred words."
	if got := FirstSentence(long); got != "CLASS: HARNESS-ONLY (classified 2026-09-15)." {
		t.Errorf("FirstSentence = %q", got)
	}
	// A reason with no sentence break is returned whole rather than cut at
	// an arbitrary width.
	if got := FirstSentence("no full stop here"); got != "no full stop here" {
		t.Errorf("FirstSentence = %q", got)
	}
}
