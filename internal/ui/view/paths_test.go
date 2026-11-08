// Tests that a record whose id no URL can address ("", "." or "..") is
// never linked to a page it is not, and that every other id is one escaped
// segment of its link.
package view_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// unaddressable are the ids path.Join and every browser resolve away.
var unaddressable = []string{"", ".", ".."}

// TestRecordLinks_WithholdALinkNoURLCanCarry: a list row, a table row, a
// referencing cell and a live stream for a record named ".." used to link
// to /ui (path.Join resolved the segment away), and one named "." to the
// list itself. Each now has no link, while every other id is escaped as one
// segment.
func TestRecordLinks_WithholdALinkNoURLCanCarry(t *testing.T) {
	list := listModel()
	table := view.TableModel{Prefix: "/ui", RecordView: "widgets"}
	ref := view.Field{Name: "owner", References: "users"}
	stream := view.StreamSpec{PathPattern: "/ui/widgets/{id}/logs/stream"}
	for _, id := range unaddressable {
		row := view.Row{ID: id, Cells: view.Cells{"owner": "someone"}, Refs: map[string]string{"owner": id}}
		for name, got := range map[string]string{
			"DetailHref": list.DetailHref(row),
			"RowHref":    table.RowHref(row),
			"RefHref":    table.RefHref(row, ref),
			"ListRef":    list.RefHref(row, ref),
			"StreamPath": stream.StreamPath(id),
		} {
			if got != "" {
				t.Errorf("%s for id %q = %q, want no link", name, id, got)
			}
		}
	}
	for id, want := range map[string]string{
		"a b":    "/ui/widgets/a%20b",
		"a/b":    "/ui/widgets/a%2Fb",
		"a?b":    "/ui/widgets/a%3Fb",
		"a#b":    "/ui/widgets/a%23b",
		"%2E%2E": "/ui/widgets/%252E%252E",
		"%2F":    "/ui/widgets/%252F",
		"../..":  "/ui/widgets/..%2F..",
	} {
		row := view.Row{ID: id}
		if got := list.DetailHref(row); got != want {
			t.Errorf("DetailHref(%q) = %q, want %q", id, got, want)
		}
		if got := table.RowHref(row); got != want {
			t.Errorf("RowHref(%q) = %q, want %q", id, got, want)
		}
	}
}

// TestNothingElseEscapesAnIDIntoAPath holds the rule in one place: outside
// paths.go, no source file of this package or of internal/ui/web calls
// url.PathEscape. A link built any other way skips the Addressable check,
// and path.Join quietly resolves a ".." id to another page.
func TestNothingElseEscapesAnIDIntoAPath(t *testing.T) {
	for _, dir := range []string{".", "../web"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("listing %s: %v", dir, err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") || filepath.Base(file) == "paths.go" {
				continue
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(parsed, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "PathEscape" {
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "url" {
						t.Errorf("%s calls url.PathEscape: build the link with view.RecordPath or view.RowPath", file)
					}
				}
				return true
			})
		}
	}
}

// TestRecordPath_JoinsLiteralSegmentsAfterTheID covers the two helpers
// directly: literal tail segments follow the escaped id, and a row's own id
// is one more escaped segment, refused like the record's.
func TestRecordPath_JoinsLiteralSegmentsAfterTheID(t *testing.T) {
	if got := view.RecordPath("/ui", "devices", "sw/1", "edit"); got != "/ui/devices/sw%2F1/edit" {
		t.Errorf("RecordPath = %q", got)
	}
	if got := view.RowPath("/ui", "devices", "sw/1", "remove", "a b"); got != "/ui/devices/sw%2F1/remove/a%20b" {
		t.Errorf("RowPath = %q", got)
	}
	for _, id := range unaddressable {
		if got := view.RowPath("/ui", "devices", id, "remove", "row"); got != "" {
			t.Errorf("RowPath with record %q = %q, want none", id, got)
		}
		if got := view.RowPath("/ui", "devices", "sw1", "remove", id); got != "" {
			t.Errorf("RowPath with row %q = %q, want none", id, got)
		}
		if view.Addressable(id) {
			t.Errorf("Addressable(%q) = true", id)
		}
	}
}
