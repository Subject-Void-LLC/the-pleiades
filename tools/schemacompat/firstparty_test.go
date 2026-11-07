// Tests for the first-party lists' contract (Phase 136a).
package main

import (
	"slices"
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

// TestCompareFirstParty_GrowsOnAMinorOnly: a namespace, a fact prefix, a
// group or an owner added is a break on a patch line and nothing on a minor; a
// new method routed to a group is nothing on either.
func TestCompareFirstParty_GrowsOnAMinorOnly(t *testing.T) {
	grown := `{"schema_version":1,
 "groups":[{"name":"facts","core":true},{"name":"file"},{"name":"net"},{"name":"virt.clab"}],
 "namespaces":["facts","file","net","virt"],
 "fact_floor":["file.","net.","os.","virt."],
 "fact_family_owners":{"os.":["facts","net"],"file.":["facts","net"],"net.":[],"virt.":[]},
 "methods":{"facts.gather":"facts","file.copy":"file","file.stat":"file","net.ios.config":"net","virt.clab.lab.deploy":"virt.clab"}}`
	if got := rules(t, grown, false); len(got) != 0 {
		t.Errorf("a minor line reported %v for growth alone", got)
	}
	want := []string{"first-party-fact-owner-added-on-a-patch", "first-party-fact-prefix-added-on-a-patch", "first-party-group-added-on-a-patch", "first-party-namespace-added-on-a-patch"}
	if got := rules(t, grown, true); !slices.Equal(got, want) {
		t.Errorf("a patch line reported %v, want %v", got, want)
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
