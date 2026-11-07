// Tests of the reserved fact names (facts_reserved.go): Register refuses an
// external method's declaration of one, and a built-in's still registers.
package collection_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func TestRegister_RefusesAnExternalMethodsReservedFact(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	stable := func(key string, prefix bool) collection.FactSpec {
		return collection.FactSpec{Key: key, Prefix: prefix, Kind: collection.FactStable, Type: collection.FactString, Description: "a fact"}
	}
	if err := collection.Register(factMethod("resbuiltin.gather", stable("resbuiltin_kernel", false))); err != nil {
		t.Fatalf("a built-in's own fact: %v", err)
	}
	external := func(name string, s collection.FactSpec) collection.Descriptor {
		d := factMethod(name, s)
		d.Provider = &collection.Provider{Program: "/collections/acme", Digest: "sha256:" + strings.Repeat("a", 64)}
		return d
	}
	for _, tc := range []struct {
		name string
		spec collection.FactSpec
		want string
	}{
		{"acme.spoof", stable("resbuiltin_kernel", false), `built-in method "resbuiltin.gather"`},
		{"acme.family", stable("package.", true), `the reserved family "package."*`},
		{"acme.item", stable("user.root", false), `the reserved family "user."*`},
		{"acme.wide", stable("serv", true), `the reserved family "service."*`},
	} {
		err := collection.Register(external(tc.name, tc.spec))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Register = %v, want a refusal naming %s", tc.name, err, tc.want)
		}
	}
	if err := collection.Register(external("acme.own", stable("acme.widgets", false))); err != nil {
		t.Errorf("an external fact under its own namespace: %v", err)
	}
	// Another external program's fact is not reserved against this one: the
	// two are held only to agree on kind and type, as any two methods are.
	if err := collection.Register(external("acme2.again", stable("acme.widgets", false))); err != nil {
		t.Errorf("a second program sharing an external key with the same kind and type: %v", err)
	}
	// A built-in registered after an external that holds its key is refused,
	// so the rule does not rest on built-ins registering first.
	if err := collection.Register(factMethod("resbuiltin.late", stable("acme.widgets", false))); err == nil || !strings.Contains(err.Error(), `(registered first) declares as "acme.widgets"`) {
		t.Errorf("a built-in after an external holding its key: %v", err)
	}
	if got := collection.OwnFactName("acme", stable("package.", true)); got != "acme.*" {
		t.Errorf("OwnFactName for a family = %q, want acme.*", got)
	}
	// A built-in may still declare into a reserved family.
	if err := collection.Register(factMethod("resbuiltin.packages", stable("package.", true))); err != nil {
		t.Errorf("a built-in's family: %v", err)
	}
}
