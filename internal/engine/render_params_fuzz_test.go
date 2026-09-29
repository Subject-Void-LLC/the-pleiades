// Fuzz and benchmark for rendered task parameters (Phase 117a): the two
// readers of data that decide where a task goes, and the cost of rendering.
package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// FuzzURLPrefixFixed: whatever the source, a prefix the rule accepts either
// begins with "/" or holds a scheme and a host that no expression touches,
// ended by "/", "?" or "#". That is the property that keeps data from
// choosing, or extending, the host a request goes to.
func FuzzURLPrefixFixed(f *testing.F) {
	for _, seed := range []string{
		"https://itsm.example.com/api/{{ x }}", "/api/{{ x }}", "https://{{ x }}/api", "https://itsm.example.com{{ x }}",
		"{{ x }}", "https://a/{{ x }}?q={{ y }}", "http://h?{{ x }}", "http://h#{{ x }}", "://{{ x }}/", "", "no template",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if !URLPrefixFixed(source) {
			return
		}
		prefix, _, _ := strings.Cut(source, "{{")
		if strings.HasPrefix(prefix, "/") {
			return
		}
		_, rest, ok := strings.Cut(prefix, "://")
		if !ok {
			t.Fatalf("URLPrefixFixed(%q) accepted a prefix with no scheme", source)
		}
		end := strings.IndexAny(rest, "/?#")
		if end <= 0 || strings.Contains(rest[:end], "{{") {
			t.Fatalf("URLPrefixFixed(%q) accepted a host an expression could reach", source)
		}
	})
}

// FuzzRenderedNames: reading a rendered target never panics, and every name
// it returns is non-empty, trimmed and free of the comma that separates
// names, so no name it hands resolution can be a list in disguise.
func FuzzRenderedNames(f *testing.F) {
	for _, seed := range []string{"sw1", "sw1, sw2", " , ", "", "a,,b", "sw-gate; reboot", "switches", "\x00", strings.Repeat("a,", 100)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		names, err := renderedNames(raw)
		if err != nil {
			return
		}
		if len(names) == 0 {
			t.Fatalf("renderedNames(%q) returned no names and no error", raw)
		}
		for _, n := range names {
			if n == "" || n != strings.TrimSpace(n) || strings.Contains(n, ",") {
				t.Fatalf("renderedNames(%q) returned the name %q", raw, n)
			}
		}
	})
}

// BenchmarkRenderTaskParams renders one task's params, a URL, a JSON body
// and a typed list, against the context a 200-task runbook has built by its
// last task: 200 registered results. It is the per-task cost rendering adds
// to a run.
func BenchmarkRenderTaskParams(b *testing.B) {
	tree := map[string]any{}
	for i := 0; i < 200; i++ {
		tree[fmt.Sprintf("r%d", i)] = map[string]any{"dev": map[string]any{"stdout": "Linux", "json": map[string]any{"id": i}}}
	}
	ctx := map[string]any{"vars": map[string]any{"ticket": "INC0010001"}, "nodes": tree, "result": singleWriters(tree)}
	params := map[string]any{
		"url":  "/api/now/table/incident/{{ vars.ticket | urlencode }}",
		"body": `{"body": {{ result.r199.stdout | to_json }}}`,
		"ids":  "{{ result.r10.json }}",
		"flat": "no template",
	}
	eng := render.New()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := renderValue(eng, params, ctx); err != nil {
			b.Fatal(err)
		}
	}
}
