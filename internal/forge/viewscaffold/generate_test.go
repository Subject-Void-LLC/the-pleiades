package viewscaffold_test

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/viewscaffold"
)

func validConfig() viewscaffold.Config {
	return viewscaffold.Config{
		Name:     "access-reviews",
		Title:    "Access Reviews",
		NavOrder: 70,
		Summary:  "Who approved what, and when.",
	}
}

// TestGenerate_EmitsTwoParseableFiles is the baseline: whatever else the
// generator does, its output has to be Go.
//
// go/format already runs inside Generate, so an unparseable template fails
// there. Parsing again here is what catches the subtler case: output that
// formats cleanly because it is syntactically valid, while being the wrong
// package or missing the entry point the composition root calls.
func TestGenerate_EmitsTwoParseableFiles(t *testing.T) {
	files, err := viewscaffold.Generate(validConfig())
	if err != nil {
		t.Fatalf("Generate() = %v, want nil", err)
	}
	if len(files) != 2 {
		t.Fatalf("Generate() produced %d files, want 2 (implementation and test)", len(files))
	}

	wantPaths := map[string]bool{
		"internal/ui/resources/accessreviews/accessreviews.go":      false,
		"internal/ui/resources/accessreviews/accessreviews_test.go": false,
	}

	fset := token.NewFileSet()
	for _, f := range files {
		if _, ok := wantPaths[f.Path]; !ok {
			t.Errorf("Generate() wrote unexpected path %q", f.Path)
			continue
		}
		wantPaths[f.Path] = true

		if _, err := parser.ParseFile(fset, f.Path, f.Content, parser.AllErrors); err != nil {
			t.Errorf("%s does not parse as Go: %v", f.Path, err)
		}
	}
	for path, seen := range wantPaths {
		if !seen {
			t.Errorf("Generate() did not produce %q", path)
		}
	}
}

// TestGenerate_StartsDeclared is the rule that matters most about what this
// generator emits.
//
// A generated view that claimed to be implemented would be a view whose
// handlers are stubs, and a stub that silently succeeds is indistinguishable
// from a working implementation with nothing to do. This project has shipped
// that failure twice; the generator must not reintroduce it as a default.
func TestGenerate_StartsDeclared(t *testing.T) {
	files, err := viewscaffold.Generate(validConfig())
	if err != nil {
		t.Fatalf("Generate() = %v, want nil", err)
	}

	// The struct field, not the file. The doc comment legitimately names
	// StatusImplemented while explaining how to get there, so a whole-file
	// grep would be checking the prose rather than the descriptor.
	source := string(files[0].Content)
	status := fieldValue(source, "Status:")

	if status != "view.StatusDeclared," {
		t.Errorf("the generated descriptor has Status %q, want view.StatusDeclared", status)
	}
	if fieldValue(source, "Handlers:") != "" {
		t.Error("the generated view carries handlers, which view.Register refuses for a declared view")
	}
}

// fieldValue reads the value assigned to a struct field in generated
// source, so an assertion can be about the descriptor rather than about
// every mention of a symbol in the file.
func fieldValue(source, field string) string {
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || !strings.HasPrefix(trimmed, field) {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(trimmed, field))
	}
	return ""
}

// TestGenerate_CarriesTheReachabilityWarning asserts the generated package
// names the one step that fails silently.
//
// A view absent from registrars.go produces no error, no warning and no
// route. It is the failure recorded twice in this repository's own history,
// and a generator that emitted a package without saying so would be
// reproducing it on purpose.
func TestGenerate_CarriesTheReachabilityWarning(t *testing.T) {
	files, err := viewscaffold.Generate(validConfig())
	if err != nil {
		t.Fatalf("Generate() = %v, want nil", err)
	}

	if !strings.Contains(string(files[0].Content), "registrars.go") {
		t.Error("the generated package does not mention registrars.go, so nothing tells its " +
			"author that an unregistered view is invisible")
	}

	reminder := viewscaffold.Reminder(validConfig())
	for _, want := range []string{"registrars.go", "accessreviews", "FAILURE_PATTERNS"} {
		if !strings.Contains(reminder, want) {
			t.Errorf("the CLI reminder does not mention %q", want)
		}
	}
}

// TestConfig_RefusesWhatRegisterWouldRefuse checks the generator fails up
// front rather than emitting a package that panics at process start.
//
// Every case below is a rule view.Register also enforces. Duplicating them
// here is deliberate and is not drift: Register protects the running binary,
// this protects the author, and finding out at generation time is strictly
// better than finding out when the controller refuses to boot.
func TestConfig_RefusesWhatRegisterWouldRefuse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*viewscaffold.Config)
		want   string
	}{
		{"no title", func(c *viewscaffold.Config) { c.Title = "" }, "no title"},
		{"no summary", func(c *viewscaffold.Config) { c.Summary = "" }, "no summary"},
		{"no nav order", func(c *viewscaffold.Config) { c.NavOrder = 0 }, "positive nav order"},
		{"uppercase name", func(c *viewscaffold.Config) { c.Name = "AccessReviews" }, "lowercase"},
		{"name with a slash", func(c *viewscaffold.Config) { c.Name = "access/reviews" }, "lowercase letters, digits and hyphens"},
		{"name with a dot", func(c *viewscaffold.Config) { c.Name = "access.reviews" }, "lowercase letters, digits and hyphens"},
		{"leading digit", func(c *viewscaffold.Config) { c.Name = "1reviews" }, "lowercase letter"},
		{"empty name", func(c *viewscaffold.Config) { c.Name = "" }, "a view needs a name"},
		{"traversal name", func(c *viewscaffold.Config) { c.Name = "../escape" }, "must start with a lowercase letter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(&cfg)

			_, err := viewscaffold.Generate(cfg)
			if err == nil {
				t.Fatalf("Generate() = nil, want an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Generate() = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestConfig_NavLabelDefaultsToTheTitle covers the one derived value, since
// a nav entry with no text has no accessible name.
func TestConfig_NavLabelDefaultsToTheTitle(t *testing.T) {
	cfg := validConfig()
	if got := cfg.ResolvedNavLabel(); got != "ACCESS REVIEWS" {
		t.Errorf("ResolvedNavLabel() = %q, want the uppercased title", got)
	}

	cfg.NavLabel = "REVIEWS"
	if got := cfg.ResolvedNavLabel(); got != "REVIEWS" {
		t.Errorf("ResolvedNavLabel() = %q, want the explicit label", got)
	}
}

// TestConfig_PackageNameDropsHyphens documents the split between the
// registration key (data, hyphenated, appears in URLs) and the Go package
// name (code, one lowercase word).
func TestConfig_PackageNameDropsHyphens(t *testing.T) {
	cfg := validConfig()

	if got := cfg.PackageName(); got != "accessreviews" {
		t.Errorf("PackageName() = %q, want %q", got, "accessreviews")
	}
	if got := cfg.PackagePath(); got != "internal/ui/resources/accessreviews" {
		t.Errorf("PackagePath() = %q", got)
	}
	if !strings.HasSuffix(cfg.ImportPath(), "/internal/ui/resources/accessreviews") {
		t.Errorf("ImportPath() = %q", cfg.ImportPath())
	}
}
