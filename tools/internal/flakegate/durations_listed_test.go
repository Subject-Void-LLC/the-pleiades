// Holding tools/testgate/durations.json to the container packages it must
// time, so a new container package fails here, on every branch, rather than
// in the coverage job of a pull request.
package flakegate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modulePrefix is the module path every DOCKER_DEPENDENT_PACKAGES entry
// starts with, which durations.json leaves off its keys.
const modulePrefix = "github.com/Subject-Void-LLC/the-pleiades/"

// TestEveryContainerPackageHasADuration fails when a package in the
// Makefile's DOCKER_DEPENDENT_PACKAGES has no time in
// tools/testgate/durations.json, either as the package itself or, for a
// package durations.json divides by test, as one of its tests.
//
// The containers jobs deal packages to their shards by those times, and the
// coverage job's `testgate -durations-check` fails a container package with
// none. That check reads the containers jobs' measurements, so a local
// pr-check, which leaves container packages to the runners, never meets it:
// internal/catalog/net/netconf joined the list in a branch whose pr-check
// and branch-containers both passed, and only the pull request's coverage
// job failed (2026-11-16). Both lists are files in the tree, so this reads
// them and needs no measurement.
func TestEveryContainerPackageHasADuration(t *testing.T) {
	root := filepath.Join("..", "..", "..")

	rawMakefile, err := os.ReadFile(filepath.Clean(filepath.Join(root, "Makefile")))
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	block := dockerDependentPackages.FindSubmatch(rawMakefile)
	if block == nil {
		t.Fatal("the Makefile no longer assigns DOCKER_DEPENDENT_PACKAGES as one block; " +
			"if it was renamed or reshaped, update this test with it")
	}
	listed := packagePath.FindAllString(string(block[1]), -1)
	if len(listed) == 0 {
		t.Fatal("parsed no packages out of DOCKER_DEPENDENT_PACKAGES, so this test would pass vacuously")
	}

	rawDurations, err := os.ReadFile(filepath.Clean(filepath.Join(root, "tools", "testgate", "durations.json")))
	if err != nil {
		t.Fatalf("reading tools/testgate/durations.json: %v", err)
	}
	var durations map[string]float64
	if err := json.Unmarshal(rawDurations, &durations); err != nil {
		t.Fatalf("parsing tools/testgate/durations.json: %v", err)
	}
	if len(durations) == 0 {
		t.Fatal("tools/testgate/durations.json holds no times, so this test would pass vacuously")
	}

	// resources.json (testgate -resources-check) fails an unknown unit only
	// once it says it is complete; from then on a container package missing
	// there is the same late surprise, so hold it here too.
	rawResources, err := os.ReadFile(filepath.Clean(filepath.Join(root, "tools", "testgate", "resources.json")))
	if err != nil {
		t.Fatalf("reading tools/testgate/resources.json: %v", err)
	}
	var resources struct {
		Complete bool                       `json:"complete"`
		Units    map[string]json.RawMessage `json:"units"`
	}
	if err := json.Unmarshal(rawResources, &resources); err != nil {
		t.Fatalf("parsing tools/testgate/resources.json: %v", err)
	}

	for _, pkg := range listed {
		rel := strings.TrimPrefix(pkg, modulePrefix)
		if resources.Complete && !hasUnit(resources.Units, rel) {
			t.Errorf("%s is in the Makefile's DOCKER_DEPENDENT_PACKAGES but not in tools/testgate/resources.json, "+
				"which says it is complete, so the coverage job's resources check fails the pull request; "+
				"fold a run in with make resources-record", pkg)
		}
		if !hasDuration(durations, rel) {
			t.Errorf("%s is in the Makefile's DOCKER_DEPENDENT_PACKAGES but has no time in "+
				"tools/testgate/durations.json, so the coverage job's durations check fails the pull request "+
				"and -shard cannot balance it. Add a line for it, in seconds, with the time its tests take "+
				"(go test -tags integration -count=1 %s, or the time a containers job measured):\n  %q: <seconds>,",
				pkg, pkg, strings.TrimPrefix(pkg, modulePrefix))
		}
	}
}

// hasDuration reports whether durations times rel, the package's path in
// the module: under rel itself, or, for a package durations.json divides by
// test, under a key naming one of its tests ("<rel> <TestName>").
func hasDuration(durations map[string]float64, rel string) bool {
	if _, ok := durations[rel]; ok {
		return true
	}
	for key := range durations {
		if strings.HasPrefix(key, rel+" ") {
			return true
		}
	}
	return false
}

// hasUnit is hasDuration for resources.json's units, which name a package,
// or a test of a divided package, the same way.
func hasUnit(units map[string]json.RawMessage, rel string) bool {
	for key := range units {
		if key == rel || strings.HasPrefix(key, rel+" ") {
			return true
		}
	}
	return false
}

// TestHasDuration_ReadsBothKeyShapes holds hasDuration to the two key
// shapes durations.json uses, and to not taking one package's time for a
// sibling whose path it prefixes.
func TestHasDuration_ReadsBothKeyShapes(t *testing.T) {
	durations := map[string]float64{
		"internal/catalog/net":                 1,
		"tests/e2e TestBackupReleaseGate_Make": 245,
	}
	cases := []struct {
		rel  string
		want bool
	}{
		{"internal/catalog/net", true},
		{"tests/e2e", true},
		{"internal/catalog/net/netconf", false},
		{"internal/catalog", false},
		{"tests/e2", false},
	}
	for _, c := range cases {
		if got := hasDuration(durations, c.rel); got != c.want {
			t.Errorf("hasDuration(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
}
