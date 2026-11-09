// Package web: the fixed segments the router matches beside a record's {id}
// are exactly the ones view.RecordPath escapes a record's name against.
package web

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
	"github.com/go-chi/chi/v5"
)

// TestRecordSiblings_AreTheRoutersOwn walks the router Routes builds and
// collects every static segment registered where a record's {id} goes
// (/{resource}/<segment>, any method). It must equal view.RecordSiblings:
// a static route added beside {id} without being added there would shadow
// the record of that name, and a name left there after its route is gone
// would escape for nothing.
func TestRecordSiblings_AreTheRoutersOwn(t *testing.T) {
	routes, ok := New(Config{Prefix: "/ui"}).Routes().(chi.Routes)
	if !ok {
		t.Fatal("Routes() is not a chi router, so its registrations cannot be walked")
	}
	found := map[string]bool{}
	sawID := false
	err := chi.Walk(routes, func(_ string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		segs := strings.Split(strings.Trim(route, "/"), "/")
		if len(segs) < 2 || segs[0] != "{resource}" {
			return nil
		}
		if segs[1] == "{id}" {
			sawID = true
			return nil
		}
		if !strings.HasPrefix(segs[1], "{") {
			found[segs[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawID {
		t.Fatal("the walk saw no /{resource}/{id} route, so it is not reading the router it guards")
	}
	var got []string
	for s := range found {
		got = append(got, s)
	}
	want := view.RecordSiblings()
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("the router's fixed segments beside {id} are %v, view.RecordSiblings is %v: they must be the same list", got, want)
	}
}
