package flakegate

import (
	"os"
	"path/filepath"
	"reflect"
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
	for path, reason := range tolerated {
		if reason == "" {
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
	tolerated := map[string]string{"tests/e2e": "known Docker contention"}

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
	tolerated := map[string]string{"tests/e2e": "known Docker contention"}

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
	tolerated := map[string]string{"tests/e2e": "known Docker contention"}

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
	tolerated := map[string]string{"tests/e2e": "known Docker contention"}

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
	tolerated := map[string]string{"tests/e2e": "known Docker contention"}

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
	tolerated := map[string]string{"tests/e2e": "known Docker contention"}

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

	hard, warned := Classify(events, map[string]string{})
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
