// Tests for the first-party lists' contract (Phase 136a).
package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// firstPartyBase is a small first-party.json at a base release.
const firstPartyBase = `{"schema_version":1,
 "groups":[{"name":"facts","core":true},{"name":"file"},{"name":"net"}],
 "namespaces":["facts","file","net"],
 "fact_floor":["file.","net.","os."],
 "fact_family_owners":{"os.":["facts","net"],"file.":["facts"],"net.":[]},
 "methods":{"facts.gather":"facts","file.copy":"file","net.ios.config":"net"}}`

// rules returns the rule ids of findings, sorted.
func rules(t *testing.T, cur string, patch bool) []string {
	t.Helper()
	f, err := compareFileOn("first-party.json", firstParty, []byte(firstPartyBase), []byte(cur), patch)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, x := range f {
		out = append(out, x.Rule)
	}
	slices.Sort(out)
	return out
}

// TestCompareFirstParty_NewlyReservedNamesAreBreaks: a namespace, a fact
// prefix or a group added refuses a program the release before loaded, so it
// is a break on any line (a minor names it in a fragment); a first-party group
// gaining a fact prefix to fill refuses nobody, and a new method routed to a
// group is a method added: neither is a break on either line.
func TestCompareFirstParty_NewlyReservedNamesAreBreaks(t *testing.T) {
	grown := `{"schema_version":1,
 "groups":[{"name":"facts","core":true},{"name":"file"},{"name":"net"},{"name":"virt.clab"}],
 "namespaces":["facts","file","net","virt"],
 "fact_floor":["file.","net.","os.","virt."],
 "fact_family_owners":{"os.":["facts","net"],"file.":["facts","net"],"net.":[],"virt.":[]},
 "methods":{"facts.gather":"facts","file.copy":"file","file.stat":"file","net.ios.config":"net","virt.clab.lab.deploy":"virt.clab"}}`
	minor := []string{"first-party-fact-prefix-added", "first-party-group-added", "first-party-namespace-added"}
	if got := rules(t, grown, false); !slices.Equal(got, minor) {
		t.Errorf("a minor line reported %v, want %v", got, minor)
	}
	if got := rules(t, grown, true); !slices.Equal(got, minor) {
		t.Errorf("a patch line reported %v, want %v", got, minor)
	}
	newMethodOnly := `{"schema_version":1,
 "groups":[{"name":"facts","core":true},{"name":"file"},{"name":"net"}],
 "namespaces":["facts","file","net"],
 "fact_floor":["file.","net.","os."],
 "fact_family_owners":{"os.":["facts","net"],"file.":["facts"],"net.":[]},
 "methods":{"facts.gather":"facts","file.copy":"file","file.stat":"file","net.ios.config":"net"}}`
	if got := rules(t, newMethodOnly, true); len(got) != 0 {
		t.Errorf("a new method on a patch line reported %v", got)
	}
}

// fileTree is a tree holding one schema directory's files at every tag.
type fileTree struct{ files map[string]string }

// tags implements tree.
func (fileTree) tags() ([]string, error) { return []string{"v0.4.0"}, nil }

// list implements tree.
func (f fileTree) list(string, string) ([]string, error) {
	var out []string
	for name := range f.files {
		out = append(out, name)
	}
	return out, nil
}

// read implements tree.
func (f fileTree) read(_, path string) ([]byte, error) {
	return []byte(f.files[filepath.Base(path)]), nil
}

// TestRun_TheLineDecidesForTheFirstPartyLists: through run, as the tool
// runs, a namespace added since v0.4.0 fails a patch line outright, fails a
// minor line with no fragment, and passes a minor line whose fragment names it.
func TestRun_TheLineDecidesForTheFirstPartyLists(t *testing.T) {
	grown := strings.Replace(firstPartyBase, `"namespaces":["facts","file","net"]`, `"namespaces":["facts","file","net","os"]`, 1)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "first-party.json"), []byte(grown), 0o600); err != nil {
		t.Fatal(err)
	}
	base := fileTree{files: map[string]string{"first-party.json": firstPartyBase}}
	token := "first-party-namespace-added first-party.json#/namespaces/os"
	for _, tc := range []struct {
		line, fragment string
		pass           bool
	}{
		{line: "0.4.1", fragment: token, pass: false},
		{line: "0.5.0", pass: false},
		{line: "0.5.0", fragment: token, pass: true},
	} {
		fragments := t.TempDir()
		if tc.fragment != "" {
			if err := os.WriteFile(filepath.Join(fragments, "os.breaking.md"), []byte("The os namespace is reserved (`"+tc.fragment+"`).\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		v, err := run(base, "", tc.line, dir, fragments)
		if err != nil {
			t.Fatal(err)
		}
		if v.Pass != tc.pass || len(v.Findings) != 1 || v.Findings[0].Token() != token {
			t.Errorf("line %s fragment %q: pass %v findings %+v, want pass %v and the one finding", tc.line, tc.fragment, v.Pass, v.Findings, tc.pass)
		}
	}
}

// TestCompareFirstParty_RemovalsAndMovesBreakOnAnyLine: a name taken off a
// list frees it for anyone, a moved method changes what a user installs, and a
// group made core or not changes what ships apart: each is a break on a minor
// too.
func TestCompareFirstParty_RemovalsAndMovesBreakOnAnyLine(t *testing.T) {
	shrunk := `{"schema_version":1,
 "groups":[{"name":"facts"},{"name":"file"}],
 "namespaces":["facts","file"],
 "fact_floor":["file.","os."],
 "fact_family_owners":{"os.":["facts"],"file.":["facts"]},
 "methods":{"facts.gather":"facts","file.copy":"net","net.ios.config":"file"}}`
	want := []string{"first-party-fact-owner-removed", "first-party-fact-prefix-removed", "first-party-group-core-changed", "first-party-group-removed", "first-party-method-moved", "first-party-method-moved", "first-party-namespace-removed"}
	for _, patch := range []bool{false, true} {
		if got := rules(t, shrunk, patch); !slices.Equal(got, want) {
			t.Errorf("patch=%v reported %v, want %v", patch, got, want)
		}
	}
}

// TestCompareFirstParty_AnUnreadableBaseComparesNothing: a base with no
// group this check reads is a shape change, not a pass.
func TestCompareFirstParty_AnUnreadableBaseComparesNothing(t *testing.T) {
	if _, err := compareFileOn("first-party.json", firstParty, []byte(`{"groups":"x"}`), []byte(firstPartyBase), false); err == nil {
		t.Error("an unreadable base compared as a pass")
	}
}
