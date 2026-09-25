// Tests for the shared parameter sets and the declared-parameter set they
// feed.
package fragment_test

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestParams resolves known fragments in order and reports unknown names
// sorted, adding nothing for them.
func TestParams(t *testing.T) {
	params, unknown := fragment.Params([]string{"catalyst_pagination", "zzz", "catalyst_connection", "aaa"})
	var names []string
	for _, p := range params {
		names = append(names, p.Name)
	}
	if want := []string{"page_size", "insecure_skip_verify"}; !slices.Equal(names, want) {
		t.Errorf("params = %v, want %v", names, want)
	}
	if want := []string{"aaa", "zzz"}; !slices.Equal(unknown, want) {
		t.Errorf("unknown = %v, want %v", unknown, want)
	}
}

// TestDeclared unions a method's own params with its fragments' params.
func TestDeclared(t *testing.T) {
	d := collection.Descriptor{Manifest: collection.Manifest{Doc: collection.Doc{
		Params:    []collection.Param{{Name: "own"}},
		Fragments: []string{"catalyst_connection", "not_a_fragment"},
	}}}
	got := fragment.Declared(d)
	for _, want := range []string{"own", "insecure_skip_verify"} {
		if !got[want] {
			t.Errorf("Declared lacks %q: %v", want, got)
		}
	}
	if len(got) != 2 {
		t.Errorf("Declared = %v, want exactly own and insecure_skip_verify", got)
	}
}
