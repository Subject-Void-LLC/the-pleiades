// Tests for the census rule: which changes add the census packages to the
// scope, which do not, and that the rule still names real paths in this
// module.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// censusWant is the census the scratch module's packages make, written out
// rather than read from censusPackages so a package dropped from the rule
// fails here.
var censusWant = []string{
	"example.com/census/cmd/pleiades",
	"example.com/census/cmd/runner",
	"example.com/census/internal/adapters/native",
	"example.com/census/internal/forge/playbook",
}

// censusModule is a module laid out like this one, in a fresh git
// repository with one commit on main and a branch checked out from it:
// the census packages, a package under each code trigger, an example
// project with no Go files (one runbook named with a character git quotes),
// a package outside every trigger, and one whose name only begins like a
// trigger.
func censusModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                                            "module example.com/census\n\ngo 1.22\n",
		"cmd/pleiades/main.go":                              "// Command pleiades is a fixture.\npackage main\n\nfunc main() {}\n",
		"cmd/runner/main.go":                                "// Command runner is a fixture.\npackage main\n\nfunc main() {}\n",
		"internal/adapters/native/native.go":                "// Package native is a fixture.\npackage native\n",
		"internal/forge/playbook/playbook.go":               "// Package playbook is a fixture.\npackage playbook\n",
		"internal/validate/validate.go":                     "// Package validate is a fixture.\npackage validate\n",
		"internal/engine/engine.go":                         "// Package engine is a fixture.\npackage engine\n",
		"internal/engineering/notes.go":                     "// Package engineering is a fixture.\npackage engineering\n",
		"pkg/collection/collection.go":                      "// Package collection is a fixture.\npackage collection\n",
		"pkg/capability/capability.go":                      "// Package capability is a fixture.\npackage capability\n",
		"internal/catalog/exec/command.go":                  "// Package exec is a fixture.\npackage exec\n",
		"internal/catalog/exec/command_test.go":             "package exec\n",
		"internal/inventory/devices/linux/linux.go":         "// Package linux is a fixture.\npackage linux\n",
		"internal/other/other.go":                           "// Package other is a fixture.\npackage other\n",
		"examples/lab/pleiades/runbooks/check.yaml":         "id: check\n",
		"examples/lab/pleiades/runbooks/r\xc3\xa9seau.yaml": "id: reseau\n",
		"examples/lab/pleiades/hosts.yaml":                  "hosts: []\n",
	}
	for name, body := range files {
		write(t, dir, name, body)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "."},
		{"-c", "user.name=census", "-c", "user.email=census@example.com", "commit", "-q", "-m", "base"},
		{"checkout", "-q", "-b", "topic"},
	} {
		git(t, dir, args...)
	}
	return dir
}

// withCensus is the census packages plus the others named, sorted as
// prscope prints them.
func withCensus(others ...string) []string {
	out := append(append([]string{}, censusWant...), others...)
	sort.Strings(out)
	return out
}

// TestCensus_AChangeThatCanAlterValidationAddsTheCensus changes one file
// at a time through the command the Makefile runs and requires exactly the
// packages each one names. Before the census rule, a change to the engine
// or a method's manifest named only its own package, and a change to an
// example named nothing (FAILURE_PATTERNS 559).
func TestCensus_AChangeThatCanAlterValidationAddsTheCensus(t *testing.T) {
	cases := []struct {
		name   string
		change string
		remove bool
		want   []string
	}{
		{name: "the validation rules", change: "internal/validate/validate.go", want: withCensus("example.com/census/internal/validate")},
		{name: "the engine", change: "internal/engine/engine.go", want: withCensus("example.com/census/internal/engine")},
		{name: "the collection contract", change: "pkg/collection/collection.go", want: withCensus("example.com/census/pkg/collection")},
		{name: "the capability vocabulary", change: "pkg/capability/capability.go", want: withCensus("example.com/census/pkg/capability")},
		{name: "a method's manifest", change: "internal/catalog/exec/command.go", want: withCensus("example.com/census/internal/catalog/exec")},
		{name: "a device type", change: "internal/inventory/devices/linux/linux.go", want: withCensus("example.com/census/internal/inventory/devices/linux")},
		{name: "an example runbook", change: "examples/lab/pleiades/runbooks/check.yaml", want: withCensus()},
		{name: "an example git quotes the name of", change: "examples/lab/pleiades/runbooks/r\xc3\xa9seau.yaml", want: withCensus()},
		{name: "an example inventory", change: "examples/lab/pleiades/hosts.yaml", want: withCensus()},
		{name: "a removed example runbook", change: "examples/lab/pleiades/runbooks/check.yaml", remove: true, want: withCensus()},
		{name: "a method's test alone", change: "internal/catalog/exec/command_test.go", want: []string{"example.com/census/internal/catalog/exec"}},
		{name: "a package outside every trigger", change: "internal/other/other.go", want: []string{"example.com/census/internal/other"}},
		{name: "a package whose name only begins like a trigger", change: "internal/engineering/notes.go", want: []string{"example.com/census/internal/engineering"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := censusModule(t)
			if tc.remove {
				if err := os.Remove(filepath.Join(dir, filepath.FromSlash(tc.change))); err != nil {
					t.Fatal(err)
				}
			} else {
				// Appending keeps each Go file's package clause, so go list
				// still reads the module.
				before, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(tc.change)))
				if err != nil {
					t.Fatal(err)
				}
				marker := "# changed\n"
				if strings.HasSuffix(tc.change, ".go") {
					marker = "// changed\n"
				}
				write(t, dir, tc.change, string(before)+marker)
			}
			requireScope(t, dir, tc.want, tc.change)
		})
	}
}

// TestCensus_ARenameOutOfATriggerStillAddsTheCensus moves a file out of a
// trigger path in a commit. git diff reports a rename by its new path
// alone unless renames are turned off, so the old path, the only one under
// a trigger, used to go unseen.
func TestCensus_ARenameOutOfATriggerStillAddsTheCensus(t *testing.T) {
	cases := []struct {
		name     string
		from, to string
		want     []string
	}{
		{name: "an engine file moved to a new package", from: "internal/engine/engine.go", to: "internal/moved/engine.go", want: withCensus("example.com/census/internal/moved")},
		{name: "an example moved out of examples", from: "examples/lab/pleiades/runbooks/check.yaml", to: "docs/check.yaml", want: withCensus()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := censusModule(t)
			if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(filepath.FromSlash(tc.to))), 0o750); err != nil {
				t.Fatal(err)
			}
			git(t, dir, "mv", tc.from, tc.to)
			git(t, dir, "-c", "user.name=census", "-c", "user.email=census@example.com", "commit", "-q", "-m", "move")
			requireScope(t, dir, tc.want, tc.from)
		})
	}
}

// requireScope runs prscope against main in dir and requires it to print
// exactly want, and to give the census reason, naming trigger, exactly
// when the census is in want.
func requireScope(t *testing.T, dir string, want []string, trigger string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := realMain(dir, []string{"-base", "main"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if got, wantOut := stdout.String(), strings.Join(want, "\n")+"\n"; got != wantOut {
		t.Errorf("changing %s printed %q, want %q", trigger, got, wantOut)
	}
	added := strings.Contains(stderr.String(), "so cmd/pleiades, cmd/runner, internal/adapters/native, internal/forge/playbook run too (FAILURE_PATTERNS 559)")
	wantAdded := strings.Contains(strings.Join(want, " "), "example.com/census/cmd/pleiades")
	if added != wantAdded || (added && !strings.Contains(stderr.String(), trigger)) {
		t.Errorf("changing %s: standard error %q, want the census reason naming it: %v", trigger, stderr.String(), wantAdded)
	}
}

// TestCensus_ATriggerWithACensusPackageMissingIsRefused fires a trigger in
// a module where a census package does not exist: the rule must refuse
// rather than quietly select less, since that is what a moved package
// would look like.
func TestCensus_ATriggerWithACensusPackageMissingIsRefused(t *testing.T) {
	for _, gone := range []string{"cmd/pleiades", "internal/forge/playbook"} {
		dir := censusModule(t)
		if err := os.RemoveAll(filepath.Join(dir, filepath.FromSlash(gone))); err != nil {
			t.Fatal(err)
		}
		write(t, dir, "internal/engine/engine.go", "// changed\npackage engine\n")
		var stdout, stderr bytes.Buffer
		if code := realMain(dir, []string{"-base", "main"}, &stdout, &stderr); code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), gone+", which validates existing runbooks, is not a package here") {
			t.Errorf("without %s: exit %d, printed %q, stderr %q; want a refusal naming it", gone, code, stdout.String(), stderr.String())
		}
	}
}

// TestCensus_TheRuleNamesThisModulesRealPaths holds the rule to this
// repository: every trigger is a directory here, every census package
// exists, and cmd/pleiades still declares the examples test, so a rename
// cannot switch the rule off while its own tests, which build their own
// module, keep passing.
func TestCensus_TheRuleNamesThisModulesRealPaths(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, dir := range append(append([]string{}, censusTriggers...), censusPackages...) {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); err != nil || !info.IsDir() {
			t.Errorf("%s is not a directory in this module: %v", dir, err)
		}
	}
	body, err := os.ReadFile(filepath.Join(root, "cmd", "pleiades", "examples_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "func TestExamples_EveryRunbookBuildsAndValidates(") {
		t.Error("cmd/pleiades no longer declares TestExamples_EveryRunbookBuildsAndValidates; point the census at the package that does")
	}
}
