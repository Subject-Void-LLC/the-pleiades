package render_test

import (
	"context"
	"io"
	"strconv"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/render"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// BenchmarkRenderList measures the cost of rendering a list at four sizes.
//
// The comparison it exists to support is against internal/api's own JSON
// encoding of the same rows, benchmarked in this repository under the same
// conditions on the same machine. That in-repo pairing is the only honest
// reference available: quoting a figure from a "reference platform" nobody
// here can reproduce is theatre, and a server-rendered page will always look
// slower than a JSON array that a browser then has to turn into a page
// anyway. What matters is that rendering stays linear in row count -- a
// quadratic template is the failure mode that only shows up on the fleet
// somebody actually has.
func BenchmarkRenderList(b *testing.B) {
	for _, size := range []int{1, 10, 100, 1000} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			model := listModel(size)
			ctx := context.Background()

			b.ReportAllocs()
			for b.Loop() {
				if err := render.List(model).Render(ctx, io.Discard); err != nil {
					b.Fatalf("Render() = %v, want nil", err)
				}
			}
		})
	}
}

// BenchmarkRenderDetail is the single-record path, which is what an operator
// actually waits on most often.
func BenchmarkRenderDetail(b *testing.B) {
	list := listModel(1)
	model := view.DetailModel{
		Page:       list.Page,
		Descriptor: list.Descriptor,
		Row:        list.Rows[0],
		Aff:        view.NewAffordances([]auth.LinkRel{auth.RelSelf}),
	}
	ctx := context.Background()

	b.ReportAllocs()
	for b.Loop() {
		if err := render.Detail(model).Render(ctx, io.Discard); err != nil {
			b.Fatalf("Render() = %v, want nil", err)
		}
	}
}

// listModel builds a descriptor and rows without touching the registry, so
// the benchmark measures rendering rather than registration.
func listModel(rows int) view.ListModel {
	descriptor := view.Descriptor{
		Name:     "bench",
		Title:    "Bench",
		NavLabel: "BENCH",
		Summary:  "A synthetic resource used only by benchmarks.",
		Status:   view.StatusImplemented,
		IDField:  "name",
		Fields: []view.Field{
			{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true},
			{Name: "type", Label: "TYPE", Kind: view.KindText, InList: true},
			{Name: "state", Label: "STATE", Kind: view.KindBadge, InList: true, BadgeClass: benchBadge},
			{Name: "tags", Label: "TAGS", Kind: view.KindTags, InList: true},
		},
		Ops: view.Ops{List: &apispec.ListDevices, Get: &apispec.GetDevice},
		// A non-nil List handler is what makes the descriptor report
		// ListsRecords, which is what makes the template render the table
		// at all. Without it this benchmark reported the same cost for one
		// row and a thousand -- because it was rendering neither.
		Handlers: &view.Handlers{
			List: func(context.Context, view.Query) (view.RowPage, error) { return view.RowPage{}, nil },
			Get:  func(context.Context, string) (view.Row, error) { return view.Row{}, nil },
		},
	}

	model := view.ListModel{
		Page: view.PageModel{
			Title:   "Bench",
			Prefix:  "/ui",
			Version: "bench",
			Skin:    string(view.SkinBrutalist),
			Subject: "bench@example",
			Nav: []view.NavSection{{
				Label: "VIEWS",
				ID:    "nav-group-views",
				Items: []view.NavItem{{Label: "BENCH", Href: "/ui/bench", Current: true}},
			}},
		},
		Descriptor: descriptor,
		Aff:        view.NewAffordances([]auth.LinkRel{auth.RelCollection}),
		Rows:       make([]view.Row, 0, rows),
	}

	for i := range rows {
		id := "device-" + strconv.Itoa(i)
		model.Rows = append(model.Rows, view.Row{ID: id, Cells: view.Cells{
			"name":  id,
			"type":  "linux_server",
			"state": "active",
			"tags":  "edge, critical",
		}})
	}
	return model
}

func benchBadge(string) string { return "badge-ok" }
