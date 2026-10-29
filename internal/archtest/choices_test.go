// This file holds Phase 131's R2 to the built-in catalog: a parameter that
// takes one of a fixed set of values declares the set (Param.Choices), so
// pleiades doc prints it and validation refuses a value outside it.
package archtest

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// laterChoicesPass names the method prefixes whose parameters are left for
// a later pass, with the reason. The net.ios.* methods are being changed
// on another branch of work, and their sets (net.ios.parse's commands, for
// one) are declared there.
var laterChoicesPass = map[string]string{
	"net.ios.": "net.ios.* parameters are declared in a later pass (Phase 131, R2)",
}

// TestAParamSayingOneOfDeclaresItsChoices: a built-in parameter whose
// description says its value is "one of" something declares that set as
// Choices. A description that lists the values in prose and declares
// nothing is exactly how the lab runs' agents came to guess a Batfish route
// protocol and a CML connector value the documentation never listed: the
// phrase promises a closed set, and only a declared one is printed by
// pleiades doc and enforced by validation. A description using the words
// for something else ("give exactly one of port or service") says it
// another way.
func TestAParamSayingOneOfDeclaresItsChoices(t *testing.T) {
	type site struct {
		owner string
		p     collection.Param
	}
	var sites []site
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			continue
		}
		for _, p := range desc.Manifest.Doc.Params {
			sites = append(sites, site{cfg.Name, p})
		}
	}
	for name, frag := range fragment.Builtin {
		for _, p := range frag.Params {
			sites = append(sites, site{"fragment " + name, p})
		}
	}
	if len(sites) == 0 {
		t.Fatal("no built-in parameter was read, so this test proved nothing")
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].owner != sites[j].owner {
			return sites[i].owner < sites[j].owner
		}
		return sites[i].p.Name < sites[j].p.Name
	})

	var declared int
	for _, s := range sites {
		if len(s.p.Choices) > 0 {
			declared++
		}
		if !undeclaredOneOf(s.p) {
			continue
		}
		if reason, later := laterFor(s.owner); later {
			t.Logf("%s's %s says \"one of\" and declares no choices: %s", s.owner, s.p.Name, reason)
			continue
		}
		t.Errorf("%s's parameter %s says \"one of\" but declares no Choices: declare the set (collection.Param.Choices) so pleiades doc prints it and validation enforces it, or say it another way if it is not a set of values", s.owner, s.p.Name)
	}
	t.Logf("%d of %d built-in parameters declare a closed set", declared, len(sites))
}

// oneOf is the phrase the rule looks for, as words in any case, so "none
// of" and "a clone of" are not read as it.
var oneOf = regexp.MustCompile(`(?i)\bone of\b`)

// undeclaredOneOf reports whether p's description says "one of" while p
// declares no Choices: the rule above, as one function its control below
// runs too.
func undeclaredOneOf(p collection.Param) bool {
	return len(p.Choices) == 0 && oneOf.MatchString(p.Description)
}

// laterFor reports whether owner is a method left for a later pass, and why.
func laterFor(owner string) (string, bool) {
	for prefix, reason := range laterChoicesPass {
		if strings.HasPrefix(owner, prefix) {
			return reason, true
		}
	}
	return "", false
}

// TestTheRuleCatchesAnUndeclaredSet is the control for the rule above: the
// same test applied to a description that says "one of" without a set
// fails, and one with a set passes, so a pass above is not a rule that
// matches nothing.
func TestTheRuleCatchesAnUndeclaredSet(t *testing.T) {
	cases := map[string]struct {
		p    collection.Param
		fail bool
	}{
		"prose and no set":  {collection.Param{Name: "state", Description: "One of present or absent."}, true},
		"a declared set":    {collection.Param{Name: "state", Choices: []string{"present", "absent"}, Description: "One of present or absent."}, false},
		"no phrase, no set": {collection.Param{Name: "path", Description: "The file's path."}, false},
		"none of, a word":   {collection.Param{Name: "files", Description: "A file of none of these kinds is refused."}, false},
		"clone of, a word":  {collection.Param{Name: "os_type", Description: "It seeds a clone of a Windows type."}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := undeclaredOneOf(tc.p); got != tc.fail {
				t.Errorf("undeclaredOneOf(%+v) = %v, want %v", tc.p, got, tc.fail)
			}
		})
	}
}
