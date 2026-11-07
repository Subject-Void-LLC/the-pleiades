// Tests of the shape of a built-in method's fact names (facts_names.go).
package collection_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func TestRegister_HoldsABuiltInsFactNamesToTheirShape(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	spec := func(key string, prefix bool) collection.FactSpec {
		return collection.FactSpec{Key: key, Prefix: prefix, Kind: collection.FactStable, Type: collection.FactString, Description: "a fact"}
	}
	for _, tc := range []struct {
		name  string
		specs []collection.FactSpec
		want  string
	}{
		{"names.bare", []collection.FactSpec{spec("devices", false)}, "has no family"},
		{"names.leading", []collection.FactSpec{spec(".version", false)}, "has no family"},
		{"names.trailing", []collection.FactSpec{spec("os.", false)}, "has no family"},
		{"names.barefamily", []collection.FactSpec{spec("ansible_", true)}, "has no family"},
		{"names.nested", []collection.FactSpec{spec("namestest.version", false), spec("namestest.version.major", false)}, "inside it"},
		{"names.good", []collection.FactSpec{spec("namestest.version", false), spec("namestest.version_major", false), spec("namesitem.", true)}, ""},
	} {
		err := collection.Register(factMethod(tc.name, tc.specs...))
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: Register = %v, want %q", tc.name, err, tc.want)
		}
	}
}
