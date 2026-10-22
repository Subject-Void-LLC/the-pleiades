// Tests for the census rule: which changes add cmd/pleiades to the scope,
// which do not, and that the rule still names real paths in this module.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// censusModule is a module laid out like this one, in a fresh git
// repository with one commit on main and a branch checked out from it:
// the census package, a package under each code trigger, an example
// project with no Go files, a package outside every trigger, and one whose
// name only begins like a trigger.
func censusModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                                    "module example.com/census\n\ngo 1.22\n",
		"cmd/pleiades/main.go":                      "// Command pleiades is a fixture.\npackage main\n\nfunc main() {}\n",
		"internal/validate/validate.go":             "// Package validate is a fixture.\npackage validate\n",
		"internal/engine/engine.go":                 "// Package engine is a fixture.\npackage engine\n",
		"internal/engineering/notes.go":             "// Package engineering is a fixture.\npackage engineering\n",
		"pkg/collection/collection.go":              "// Package collection is a fixture.\npackage collection\n",
		"pkg/capability/capability.go":              "// Package capability is a fixture.\npackage capability\n",
		"internal/catalog/exec/command.go":          "// Package exec is a fixture.\npackage exec\n",
		"internal/catalog/exec/command_test.go":     "package exec\n",
		"internal/inventory/devices/linux/linux.go": "// Package linux is a fixture.\npackage linux\n",
		"internal/other/other.go":                   "// Package other is a fixture.\npackage other\n",
		"examples/lab/pleiades/runbooks/check.yaml": "id: check\n",
		"examples/lab/pleiades/hosts.yaml":          "hosts: []\n",
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

// TestCensus_AChangeThatCanAlterValidationAddsCmdPleiades changes one file
// at a time through the command the Makefile runs and requires exactly the
// packages each one names. Before the census rule, a change to the engine
// or a method's manifest named only its own package, and a change to an
// example named nothing (FAILURE_PATTERNS 559).
func TestCensus_AChangeThatCanAlterValidationAddsCmdPleiades(t *testing.T) {
	const census = "example.com/census/cmd/pleiades"
	cases := []struct {
		name   string
		change string
		remove bool
		want   []string
	}{
		{name: "the validation rules", change: "internal/validate/validate.go", want: []string{census, "example.com/census/internal/validate"}},
		{name: "the engine", change: "internal/engine/engine.go", want: []string{census, "example.com/census/internal/engine"}},
		{name: "the collection contract", change: "pkg/collection/collection.go", want: []string{census, "example.com/census/pkg/collection"}},
		{name: "the capability vocabulary", change: "pkg/capability/capability.go", want: []string{census, "example.com/census/pkg/capability"}},
		{name: "a method's manifest", change: "internal/catalog/exec/command.go", want: []string{census, "example.com/census/internal/catalog/exec"}},
		{name: "a device type", change: "internal/inventory/devices/linux/linux.go", want: []string{census, "example.com/census/internal/inventory/devices/linux"}},
		{name: "an example runbook", change: "examples/lab/pleiades/runbooks/check.yaml", want: []string{census}},
		{name: "an example inventory", change: "examples/lab/pleiades/hosts.yaml", want: []string{census}},
		{name: "a removed example runbook", change: "examples/lab/pleiades/runbooks/check.yaml", remove: true, want: []string{census}},
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
			var stdout, stderr bytes.Buffer
			if code := realMain(dir, []string{"-base", "main"}, &stdout, &stderr); code != 0 {
				t.Fatalf("exit %d: %s", code, stderr.String())
			}
			if got, want := stdout.String(), strings.Join(tc.want, "\n")+"\n"; got != want {
				t.Errorf("changing %s printed %q, want %q", tc.change, got, want)
			}
			// The reason is printed exactly when the census was added.
			added := strings.Contains(stderr.String(), "so cmd/pleiades runs too (FAILURE_PATTERNS 559)")
			if wantAdded := tc.want[0] == census; added != wantAdded || (added && !strings.Contains(stderr.String(), tc.change)) {
				t.Errorf("changing %s: standard error %q, want the census reason naming it: %v", tc.change, stderr.String(), wantAdded)
			}
		})
	}
}

// TestCensus_ATriggerWithNoCensusPackageIsRefused fires a trigger in a
// module with no cmd/pleiades: the rule must refuse rather than quietly
// select nothing, since that is what a moved package would look like.
func TestCensus_ATriggerWithNoCensusPackageIsRefused(t *testing.T) {
	dir := censusModule(t)
	if err := os.RemoveAll(filepath.Join(dir, "cmd")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "internal/engine/engine.go", "// changed\npackage engine\n")
	var stdout, stderr bytes.Buffer
	if code := realMain(dir, []string{"-base", "main"}, &stdout, &stderr); code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "cmd/pleiades, which validates every runbook, is not a package here") {
		t.Errorf("exit %d, printed %q, stderr %q; want a refusal naming cmd/pleiades", code, stdout.String(), stderr.String())
	}
}

// TestCensus_TheRuleNamesThisModulesRealPaths holds the rule to this
// repository: every trigger is a directory here and the census package
// still declares the examples test, so a rename cannot switch the rule off
// while its own tests, which build their own module, keep passing.
func TestCensus_TheRuleNamesThisModulesRealPaths(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, trigger := range censusTriggers {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(trigger))); err != nil || !info.IsDir() {
			t.Errorf("census trigger %s is not a directory in this module: %v", trigger, err)
		}
	}
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(censusPackage), "examples_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "func TestExamples_EveryRunbookBuildsAndValidates(") {
		t.Errorf("%s no longer declares TestExamples_EveryRunbookBuildsAndValidates; point the census at the package that does", censusPackage)
	}
}
