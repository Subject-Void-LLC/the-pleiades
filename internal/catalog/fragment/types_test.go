// Tests for Types: the declared type of every parameter a method documents,
// its own and its fragments' alike.
package fragment_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestTypes_OwnParamsWinOverAFragment: a method's own declaration of a name
// is the type it reads, and a fragment's type fills in the rest.
func TestTypes_OwnParamsWinOverAFragment(t *testing.T) {
	d := collection.Descriptor{Manifest: collection.Manifest{Doc: collection.Doc{
		Params:    []collection.Param{{Name: "own", Type: "int"}, {Name: "page_size", Type: "string"}},
		Fragments: []string{"catalyst_pagination"},
	}}}
	got := fragment.Types(d)
	if got["own"] != "int" || got["page_size"] != "string" {
		t.Errorf("Types = %v, want the method's own type for page_size", got)
	}
	if got["insecure_skip_verify"] != "" {
		t.Errorf("Types named a parameter no fragment adds: %v", got)
	}
	shared := fragment.Types(collection.Descriptor{Manifest: collection.Manifest{Doc: collection.Doc{
		Fragments: []string{"catalyst_pagination"},
	}}})
	if shared["page_size"] != "int" {
		t.Errorf("a fragment's type = %q, want int", shared["page_size"])
	}
}
