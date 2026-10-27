// Tests of the filter listing `pleiades doc filters` prints.
package render_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestFilters_DescribesEveryFilterTheRendererHas: each filter has a usage
// and a description, in name order, and each usage compiles in the renderer;
// each excluded filter is named with a reason. That the renderer refuses an
// excluded filter is not checked here.
func TestFilters_DescribesEveryFilterTheRendererHas(t *testing.T) {
	eng := render.New()
	got := render.Filters()
	if len(got) < 10 {
		t.Fatalf("%d filters described", len(got))
	}
	for i, f := range got {
		if f.Usage == "" || f.Description == "" {
			t.Errorf("filter %q: %+v", f.Name, f)
		}
		if i > 0 && got[i-1].Name >= f.Name {
			t.Errorf("not in name order: %s before %s", got[i-1].Name, f.Name)
		}
		// The usage is a template the renderer compiles: what is listed is
		// what works.
		if _, err := eng.Compile(f.Usage); err != nil {
			t.Errorf("filter %s usage %q does not compile: %v", f.Name, f.Usage, err)
		}
	}
	for _, e := range render.ExcludedFilters() {
		if e.Name == "" || e.Why == "" {
			t.Errorf("excluded filter %+v", e)
		}
	}
}
