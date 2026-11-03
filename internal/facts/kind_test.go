// Tests of what a declaration does to Prepare and Verify (kind.go).
package facts_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// TestPrepare_HoldsEachValueToItsDeclaredType: a value of the declared JSON
// type is stored with the declared kind; any other, an undeclared key, and a
// null are not.
func TestPrepare_HoldsEachValueToItsDeclaredType(t *testing.T) {
	spec := map[string]facts.Spec{
		"s": {Kind: facts.KindStable, Type: facts.TypeString}, "i": {Kind: facts.KindVolatile, Type: facts.TypeInteger},
		"big": {Kind: facts.KindStable, Type: facts.TypeInteger}, "n": {Kind: facts.KindStable, Type: facts.TypeNumber},
		"ni": {Kind: facts.KindStable, Type: facts.TypeNumber}, "b": {Kind: facts.KindStable, Type: facts.TypeBoolean},
		"l": {Kind: facts.KindStable, Type: facts.TypeList}, "o": {Kind: facts.KindStable, Type: facts.TypeObject},
		"frac": {Kind: facts.KindStable, Type: facts.TypeInteger}, "null": {Kind: facts.KindStable, Type: facts.TypeString},
		"wrong": {Kind: facts.KindStable, Type: facts.TypeBoolean}, "untyped": {Kind: facts.KindStable},
	}
	declared := func(key string) (facts.Spec, bool) { s, ok := spec[key]; return s, ok }
	observed := map[string]any{
		"s": "x", "i": 3, "big": uint64(1) << 63, "n": 1.5, "ni": 2, "b": true, "l": []any{1}, "o": map[string]any{"a": 1},
		"frac": 1.5, "null": nil, "wrong": "yes", "untyped": []any{"anything"}, "undeclared": "x",
	}
	entries, problems, err := facts.Prepare(observed, facts.Guard{Declared: declared})
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string]facts.Kind{}
	for _, e := range entries {
		stored[e.Key] = e.Kind
	}
	for _, key := range []string{"s", "i", "big", "n", "ni", "b", "l", "o", "untyped"} {
		if stored[key] != spec[key].Kind {
			t.Errorf("%s: stored as %q, want %q", key, stored[key], spec[key].Kind)
		}
	}
	why := map[string]string{}
	for _, p := range problems {
		why[p.Key] = p.Reason
	}
	for key, want := range map[string]string{"frac": "a number", "null": "null", "wrong": "a string", "undeclared": "does not declare"} {
		if !strings.Contains(why[key], want) {
			t.Errorf("%s: reason %q, want one naming %q", key, why[key], want)
		}
	}
}

// TestVerify_KeepsAKindOnlyWhereTheDeclarationAgrees: without a declaration
// an entry keeps the kind its Runner sent; with one, a disagreeing kind is
// refused; a kind that is no kind is refused either way.
func TestVerify_KeepsAKindOnlyWhereTheDeclarationAgrees(t *testing.T) {
	prep := func(kind facts.Kind) facts.Entry {
		entries, _, _ := facts.Prepare(map[string]any{"up": 5}, facts.Guard{Declared: func(string) (facts.Spec, bool) { return facts.Spec{Kind: kind}, true }})
		return entries[0]
	}
	volatile := prep(facts.KindVolatile)
	got, refused, err := facts.Verify([]facts.Entry{volatile}, facts.Guard{})
	if err != nil || refused != 0 || len(got) != 1 || got[0].Kind != facts.KindVolatile {
		t.Fatalf("no declaration: %+v, %d, %v", got, refused, err)
	}
	stableOnly := func(string) (facts.Spec, bool) {
		return facts.Spec{Kind: facts.KindStable, Type: facts.TypeInteger}, true
	}
	if got, refused, _ := facts.Verify([]facts.Entry{volatile}, facts.Guard{Declared: stableOnly}); refused != 1 || len(got) != 0 {
		t.Fatalf("a disagreeing kind: %+v, refused %d", got, refused)
	}
	odd := volatile
	odd.Kind = "sometimes"
	if got, refused, _ := facts.Verify([]facts.Entry{odd}, facts.Guard{}); refused != 1 || len(got) != 0 {
		t.Fatalf("a kind that is no kind: %+v, refused %d", got, refused)
	}
	none := volatile
	none.Kind = ""
	if got, _, _ := facts.Verify([]facts.Entry{none}, facts.Guard{}); len(got) != 1 || got[0].Kind != facts.KindStable {
		t.Fatalf("no kind at all reads as stable: %+v", got)
	}
}

// TestPrepare_AFamilyMembersNameIsDataNotAFieldName: in a declared family
// (package.), the part after the prefix is an item's identifier, so a
// package called password-store is kept, while a key declared on its own
// and named like a secret is still refused, and a family member's value is
// still held to the secret checks.
func TestPrepare_AFamilyMembersNameIsDataNotAFieldName(t *testing.T) {
	declared := func(key string) (facts.Spec, bool) {
		if strings.HasPrefix(key, "package.") {
			return facts.SpecOf("stable", "object", "package.", true), true
		}
		return facts.SpecOf("stable", "string", key, false), true
	}
	secretName := func(name string) bool { return strings.Contains(name, "password") || strings.Contains(name, "token") }
	shaped := func(text string) bool { return strings.Contains(text, "BEGIN OPENSSH PRIVATE KEY") }
	entries, problems, err := facts.Prepare(map[string]any{
		"package.password-store": map[string]any{"version": "1.7.4"},
		"package.token-sdk":      map[string]any{"version": "2"},
		"package.leaky":          map[string]any{"version": "-----BEGIN OPENSSH PRIVATE KEY-----"},
		"db_password":            "x",
	}, facts.Guard{Declared: declared, SecretName: secretName, Shaped: shaped})
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, e := range entries {
		kept[e.Key] = true
	}
	if !kept["package.password-store"] || !kept["package.token-sdk"] || kept["package.leaky"] || kept["db_password"] {
		t.Errorf("kept %v (problems %v): want the two packages, not the secret-shaped value or the secret-named key", kept, problems)
	}
}

// TestPrepare_AnObservationPastItsByteBoundIsRefusedWhole: many keys each
// within the per-value bound still cannot together pass the observation's.
func TestPrepare_AnObservationPastItsByteBoundIsRefusedWhole(t *testing.T) {
	big := strings.Repeat("x", facts.MaxValueBytes-16)
	observed := map[string]any{}
	for i := 0; i <= facts.MaxObservationBytes/len(big); i++ {
		observed[fmt.Sprintf("k%d", i)] = big
	}
	if _, _, err := facts.Prepare(observed, facts.Guard{}); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("an observation past its byte bound: %v", err)
	}
}
