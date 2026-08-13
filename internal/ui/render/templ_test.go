package render_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These are the gates on the generated template layer.
//
// This package is excluded from the coverage ratchet, because templ emits a
// _templ.go beside every .templ in the same package and tools/coverage-check
// keys exclusions on a package path rather than a file glob. That exclusion
// is only defensible if "excluded from coverage" does not mean "untested",
// which is what the assertions below are for: the templates carry no
// decisions, the generated files match their sources, and the one escape
// hatch out of templ's automatic escaping is not used anywhere.

// templSources lists this package's .templ files.
func templSources(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob("*.templ")
	if err != nil {
		t.Fatalf("globbing templates: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no .templ files found, so every assertion in this file would pass vacuously")
	}
	return matches
}

func read(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(body)
}

// TestTemplSourcesAndGeneratedFilesAgree catches the two ways a generated
// tree drifts from its source: a template added and never generated, and a
// generated file left behind after its template was renamed or deleted.
//
// make templ-gen-check catches the first. It cannot catch the second, since
// a stale _templ.go is a tracked file that regenerating does not touch, so
// git diff stays clean while the package still compiles the old component.
func TestTemplSourcesAndGeneratedFilesAgree(t *testing.T) {
	sources := templSources(t)

	expected := make(map[string]bool, len(sources))
	for _, src := range sources {
		generated := strings.TrimSuffix(src, ".templ") + "_templ.go"
		expected[generated] = true

		if _, err := os.Stat(generated); err != nil {
			t.Errorf("%s has no generated %s. Run `make templ-gen` and commit the result.", src, generated)
		}
	}

	generated, err := filepath.Glob("*_templ.go")
	if err != nil {
		t.Fatalf("globbing generated files: %v", err)
	}
	for _, g := range generated {
		if !expected[g] {
			t.Errorf("%s is generated from no .templ file. It is stale: delete it.", g)
		}
	}
}

// TestTemplatesUseNoEscapeHatch bans the constructs that turn templ's
// automatic escaping off.
//
// templ escapes every interpolation by default, which is what makes it safe
// to render device names, job ids and log lines that arrived from the
// network. templ.Raw is the one door out of that, and a door out of
// automatic escaping in a control plane that renders remote data is exactly
// where an injection eventually gets written. There is no legitimate use for
// it in this UI, so it is refused outright rather than reviewed case by
// case.
func TestTemplatesUseNoEscapeHatch(t *testing.T) {
	banned := []struct {
		token string
		why   string
	}{
		{"templ.Raw", "renders unescaped markup, which is how remote data becomes an injection"},
		{"templ.ComponentScript", "emits an inline script, which this UI's content security policy forbids"},
		{"templ.JSONScript", "emits an inline script, which this UI's content security policy forbids"},
		{"templ.URL(", "builds a URL without the SafeURL check; use templ.SafeURL"},
	}

	for _, src := range templSources(t) {
		body := read(t, src)
		for _, b := range banned {
			if strings.Contains(body, b.token) {
				t.Errorf("%s uses %s, which %s", src, b.token, b.why)
			}
		}
	}
}

// TestTemplatesCarryNoDecisions is what makes excluding this package from
// the coverage ratchet honest.
//
// The rule: a template may loop, may test a boolean the model already
// computed, and may interpolate. It may not compute anything. Every branch
// worth covering therefore lives in Go, in internal/ui/view or
// internal/ui/web, where it is ordinary tested code with a real coverage
// floor -- rather than inside a generated file that no coverage tool
// reports on.
//
// It is a lexical check, so it is not airtight; it catches the shapes that
// actually show up when someone starts doing arithmetic in a view.
func TestTemplatesCarryNoDecisions(t *testing.T) {
	// Operators that mean a template computed something rather than asked
	// the model for it.
	banned := []string{
		" + 1", " - 1", " * ", " % ",
		"strings.", "strconv.", "fmt.Sprintf",
		"sort.", "append(",
	}

	for _, src := range templSources(t) {
		for i, line := range strings.Split(read(t, src), "\n") {
			trimmed := strings.TrimSpace(line)
			// Comments explain the rules; they do not break them.
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, b := range banned {
				if strings.Contains(line, b) {
					t.Errorf("%s:%d computes with %q. Move it to a model method: a decision "+
						"inside a template is a branch no coverage tool reports on.\n\t%s",
						src, i+1, strings.TrimSpace(b), trimmed)
				}
			}
		}
	}
}

// TestTemplatesDeclareNoInlineStyle is the content security policy's other
// half, checked at the source rather than only in rendered output.
//
// The rendered-output check in the conformance suite covers the pages that
// suite renders. This covers every template, including any that only render
// under a condition no test happens to reach.
func TestTemplatesDeclareNoInlineStyle(t *testing.T) {
	for _, src := range templSources(t) {
		for i, line := range strings.Split(read(t, src), "\n") {
			if strings.Contains(line, "style=") {
				t.Errorf("%s:%d carries an inline style attribute, which the content "+
					"security policy forbids. Use a class from the validated set.", src, i+1)
			}
		}
	}
}
