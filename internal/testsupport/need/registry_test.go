// Tests that Names is complete: every name a Require call is given, and every
// name CI's workflow lists in PLEIADES_TEST_REQUIRE, is a registered need.
package need

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// moduleRoot returns the directory holding go.mod, found by walking up from
// the test's working directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}

// requireAllow lists, by package path within the module and function, the
// functions whose need argument checkRequireCalls cannot resolve, each with
// why the need is still checked.
var requireAllow = map[string]string{
	"internal/testsupport.Download": "its need is pin.What, and checkRequireCalls holds every testsupport.Pin literal's What to the rule a need argument is held to",
}

// TestEveryRequireCallNamesAKnownNeed checks every call of need.Require and
// testsupport.Require in the module, resolving its need through the type
// checker (checkRequireCalls): a literal, a constant, an unexported package
// variable set once from one, or a parameter whose every call is resolved
// the same way. A need that resolves to a name not in Names fails, so a
// need cannot be added without registering it, and CI can list it; and one
// that resolves to nothing it can read fails too, since a name passed in a
// variable is where the old literal-only check saw nothing (the db/pg
// harness passed "pg_dump" so).
func TestEveryRequireCallNamesAKnownNeed(t *testing.T) {
	got, err := checkRequireCalls(moduleRoot(t), Names, requireAllow)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got.problems {
		t.Error(p)
	}
	// The check must have found the calls and resolved them, literals and
	// constants alike (refusing.Need is need.RefusingPort, and the db/pg
	// harness passes need.PostgresClient), or a broken parse or type check
	// would pass.
	for _, want := range []string{Docker, Userns, NonRoot, RefusingPort, PostgresClient, Toybox} {
		if !got.seen[want] {
			t.Errorf("no Require call was resolved to %q: the check is not seeing the module", want)
		}
	}
}

// TestCIRequireListsOnlyKnownNeeds parses the workflow and checks every
// PLEIADES_TEST_REQUIRE it sets against Names, since an unknown name makes
// the first Require of that job fail.
func TestCIRequireListsOnlyKnownNeeds(t *testing.T) {
	path := filepath.Join(moduleRoot(t), ".github", "workflows", "ci.yml")
	data, err := os.ReadFile(path) // #nosec G304 -- the workflow in this module
	if err != nil {
		t.Fatal(err)
	}
	line := regexp.MustCompile(`(?m)^\s*` + Env + `:\s*(\S+)\s*$`)
	matches := line.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatalf("%s sets no %s: the check is not reading the workflow", path, Env)
	}
	for _, m := range matches {
		for _, item := range strings.Split(m[1], ",") {
			if item != All && !slices.Contains(Names, item) {
				t.Errorf("ci.yml lists %q in %s, which is not in need.Names", item, Env)
			}
		}
	}
}
