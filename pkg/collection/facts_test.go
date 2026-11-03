// Tests of fact declarations (facts.go): what Register refuses, and how a
// key finds its declaration.
package collection_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// factMethod is a registrable read-only method declaring specs.
func factMethod(name string, specs ...collection.FactSpec) collection.Descriptor {
	invoke := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	return collection.Descriptor{Name: name, Invoke: invoke, Check: invoke, Manifest: collection.Manifest{
		Status: collection.StatusImplemented, SupportsCheck: true,
		Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a test fixture"}, Facts: specs}}
}

func TestValidateFacts_RefusesEachMalformedDeclaration(t *testing.T) {
	good := collection.FactSpec{Key: "k", Kind: collection.FactStable, Type: collection.FactString, Description: "a key"}
	if err := collection.ValidateFacts([]collection.FactSpec{good, {Key: "k", Prefix: true, Kind: collection.FactVolatile, Type: collection.FactList, Description: "a family"}}); err != nil {
		t.Fatalf("a key and a prefix of the same text: %v", err)
	}
	for name, tc := range map[string]struct {
		mutate func(*collection.FactSpec)
		want   string
	}{
		"an empty key":       {func(s *collection.FactSpec) { s.Key = "" }, "not a fact name"},
		"a key with a space": {func(s *collection.FactSpec) { s.Key = "a b" }, "not a fact name"},
		"a key with a slash": {func(s *collection.FactSpec) { s.Key = "a/b" }, "not a fact name"},
		"a long key":         {func(s *collection.FactSpec) { s.Key = strings.Repeat("k", 257) }, "not a fact name"},
		"no kind":            {func(s *collection.FactSpec) { s.Kind = "" }, "neither"},
		"an unknown type":    {func(s *collection.FactSpec) { s.Type = "date" }, "JSON type"},
		"no description":     {func(s *collection.FactSpec) { s.Description = "  " }, "no description"},
	} {
		t.Run(name, func(t *testing.T) {
			spec := good
			tc.mutate(&spec)
			if err := collection.ValidateFacts([]collection.FactSpec{spec}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if err := collection.ValidateFacts([]collection.FactSpec{good, good}); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("a duplicate: %v", err)
	}
}

func TestFactFor_PrefersAnExactKeyThenTheLongestPrefix(t *testing.T) {
	specs := []collection.FactSpec{
		{Key: "pkg_", Prefix: true, Kind: collection.FactStable},
		{Key: "pkg_nginx_", Prefix: true, Kind: collection.FactVolatile},
		{Key: "pkg_nginx_version", Kind: collection.FactStable, Type: collection.FactString},
	}
	for name, want := range map[string]string{"pkg_nginx_version": "pkg_nginx_version", "pkg_nginx_arch": "pkg_nginx_", "pkg_curl": "pkg_"} {
		got, ok := collection.FactFor(specs, name)
		if !ok || got.Key != want {
			t.Errorf("FactFor(%q) = %q, %v; want %q", name, got.Key, ok, want)
		}
	}
	// A prefix alone is not one of its own family, and an unknown key has none.
	for _, name := range []string{"pkg_", "other"} {
		if _, ok := collection.FactFor(specs, name); ok {
			t.Errorf("FactFor(%q) found a declaration", name)
		}
	}
}

func TestDeclareFacts_ReadsTheDocumentedReturns(t *testing.T) {
	returns := []collection.ReturnField{
		{Name: "s", Type: "string", Description: "a string"},
		{Name: "i", Type: "int", Description: "an int"},
		{Name: "f", Type: "float", Description: "a float"},
		{Name: "b", Type: "bool", Description: "a bool"},
		{Name: "l", Type: "list of map", Description: "a list"},
		{Name: "m", Type: "dict", Description: "a map"},
		{Name: "odd", Type: "timestamp", Description: "an unreadable type"},
	}
	specs := collection.DeclareFacts(returns, []string{"s", "i", "f", "b", "l", "m", "odd", "missing"}, "i")
	want := []collection.FactType{collection.FactString, collection.FactInteger, collection.FactNumber, collection.FactBoolean, collection.FactList, collection.FactObject, "", ""}
	for i, s := range specs {
		if s.Type != want[i] {
			t.Errorf("%s: type %q, want %q", s.Key, s.Type, want[i])
		}
	}
	if specs[1].Kind != collection.FactVolatile || specs[0].Kind != collection.FactStable || specs[7].Description != "" {
		t.Errorf("specs = %+v", specs)
	}
	// What it cannot read stops registration rather than passing silently.
	if err := collection.ValidateFacts(specs); err == nil {
		t.Error("an unreadable type and an undocumented key passed")
	}
}

func TestRegister_RefusesAKeyAnotherMethodDeclaresOtherwise(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	stable := collection.FactSpec{Key: "facttest_kernel", Kind: collection.FactStable, Type: collection.FactString, Description: "the kernel"}
	if err := collection.Register(factMethod("facttest.one", stable)); err != nil {
		t.Fatal(err)
	}
	// The same declaration twice is fine; a different kind or type is not.
	if err := collection.Register(factMethod("facttest.two", stable)); err != nil {
		t.Fatalf("an agreeing declaration: %v", err)
	}
	volatile := stable
	volatile.Kind = collection.FactVolatile
	if err := collection.Register(factMethod("facttest.three", volatile)); err == nil || !strings.Contains(err.Error(), "facttest.one") && !strings.Contains(err.Error(), "facttest.two") {
		t.Fatalf("a disagreeing kind: %v", err)
	}
	typed := stable
	typed.Type = collection.FactInteger
	if err := collection.Register(factMethod("facttest.four", typed)); err == nil {
		t.Fatal("a disagreeing type was registered")
	}
	// A family holding a key another method declares otherwise is refused,
	// and so is a family overlapping another family with another kind; one
	// that agrees, or overlaps nothing, is not.
	family := collection.FactSpec{Key: "facttest_", Prefix: true, Kind: collection.FactVolatile, Type: collection.FactString, Description: "a family"}
	if err := collection.Register(factMethod("facttest.six", family)); err == nil || !strings.Contains(err.Error(), "same key") {
		t.Fatalf("a family holding a stable key, declared volatile: %v", err)
	}
	agreeing := family
	agreeing.Kind = collection.FactStable
	if err := collection.Register(factMethod("facttest.seven", agreeing)); err != nil {
		t.Fatalf("a family agreeing with the key it holds: %v", err)
	}
	inner := collection.FactSpec{Key: "facttest_k", Prefix: true, Kind: collection.FactVolatile, Type: collection.FactString, Description: "an inner family"}
	if err := collection.Register(factMethod("facttest.eight", inner)); err == nil {
		t.Fatal("a family inside a family of another kind was registered")
	}
	apart := collection.FactSpec{Key: "elsewhere_", Prefix: true, Kind: collection.FactVolatile, Type: collection.FactString, Description: "a family apart"}
	if err := collection.Register(factMethod("facttest.nine", apart)); err != nil {
		t.Fatalf("a family overlapping nothing: %v", err)
	}
	// And a key inside an existing family must agree with it.
	if err := collection.Register(factMethod("facttest.ten", collection.FactSpec{Key: "elsewhere_x", Kind: collection.FactStable, Type: collection.FactString, Description: "x"})); err == nil {
		t.Fatal("a stable key inside a volatile family was registered")
	}
	idx := collection.NewFactIndex(map[string]collection.Manifest{"a": {Facts: []collection.FactSpec{stable}}, "b": {Facts: []collection.FactSpec{apart}}})
	if s, ok := idx.Lookup("facttest_kernel"); !ok || s.Kind != collection.FactStable || idx.Len() != 2 {
		t.Errorf("index lookup of an exact key: %+v, %v", s, ok)
	}
	if s, ok := idx.Lookup("elsewhere_x"); !ok || s.Key != "elsewhere_" {
		t.Errorf("index lookup through a family: %+v, %v", s, ok)
	}
	if _, ok := idx.Lookup("nothing"); ok {
		t.Error("an undeclared key was found")
	}
	if err := collection.Register(factMethod("facttest.five", collection.FactSpec{Key: "bad key", Kind: collection.FactStable, Type: collection.FactString, Description: "x"})); err == nil ||
		!strings.Contains(err.Error(), `"facttest.five"`) {
		t.Fatalf("a malformed declaration: %v", err)
	}
}
