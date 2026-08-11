package viewscaffold

import (
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/genutil"
)

// Config is the input to Generate: everything needed to emit one new web UI
// view resource package.
//
// It is deliberately small. A view's real content is its field declaration
// and the port it adapts, and a generator can know neither -- guessing at
// them would produce a file whose first edit is deleting most of it. What it
// can know, and what is worth reproducing exactly rather than re-deriving,
// is the shape: register into the right registry, start declared rather than
// pretending to work, and carry the reminder that a package nobody imports
// never runs.
type Config struct {
	// Name is the view's registration key, its URL path segment, and its
	// Go package name. It is a single lowercase segment because it becomes
	// a URL segment and a metrics label.
	Name string

	// Title is the page heading and the document title.
	Title string

	// NavLabel is the sidebar text. It defaults to the uppercased Title
	// when empty, matching what the built-in views do.
	NavLabel string

	// NavOrder places the view in the sidebar. The built-ins occupy 10
	// through 60 in steps of ten, so a new view has room between any two
	// without renumbering the rest.
	NavOrder int

	// Summary is the one line rendered under the heading.
	Summary string
}

// PackageName returns the generated package's Go identifier: Name with its
// hyphens removed, so "access-reviews" becomes "accessreviews".
//
// The registration key keeps its hyphens because it is data -- it is the URL
// segment a user sees -- while a Go package name is code, and the convention
// there is a single lowercase word. This mirrors pluginscaffold's own
// underscore rule rather than inventing a second spelling convention.
func (c Config) PackageName() string {
	return strings.ReplaceAll(c.Name, "-", "")
}

// PackagePath returns the directory the generated package lives in,
// relative to the repository root.
func (c Config) PackagePath() string {
	return "internal/ui/resources/" + c.PackageName()
}

// ImportPath returns the full Go import path of the generated package,
// which is what internal/ui/resources/registrars.go must name.
func (c Config) ImportPath() string {
	return "github.com/Subject-Void-LLC/the-pleiades/" + c.PackagePath()
}

// ResolvedNavLabel is NavLabel, or the uppercased Title when none was given.
func (c Config) ResolvedNavLabel() string {
	if strings.TrimSpace(c.NavLabel) != "" {
		return c.NavLabel
	}
	return strings.ToUpper(c.Title)
}

// viewNamePattern is what a view name must match. It is the same rule
// view.Register enforces, restated here so the generator refuses up front
// rather than emitting a package that panics at process start.
const viewNameChars = "abcdefghijklmnopqrstuvwxyz0123456789-"

// Validate reports whether cfg is safe to generate from.
func (c Config) Validate() error {
	// This package's own name rules run before genutil's, deliberately. Both
	// reject the same names, but genutil's message describes a Go path
	// segment and a view name is a URL segment; the author reading the
	// error is choosing the latter, so they get the rule that applies to
	// what they typed. genutil stays below as the backstop that keeps this
	// from ever emitting an unsafe path.
	if c.Name == "" {
		return fmt.Errorf("viewscaffold: invalid view name: a view needs a name")
	}
	if !strings.ContainsAny(c.Name[:1], "abcdefghijklmnopqrstuvwxyz") {
		return fmt.Errorf("viewscaffold: view name %q must start with a lowercase letter", c.Name)
	}
	for _, r := range c.Name {
		if !strings.ContainsRune(viewNameChars, r) {
			return fmt.Errorf("viewscaffold: view name %q may contain only lowercase letters, digits and hyphens", c.Name)
		}
	}
	if err := genutil.ValidateSegment(c.PackageName()); err != nil {
		return fmt.Errorf("viewscaffold: invalid view name %q: %w", c.Name, err)
	}
	if strings.TrimSpace(c.Title) == "" {
		// A view with no title has no <h1> and no document title, which
		// is an accessibility failure rather than a cosmetic gap, so it is
		// refused here as well as at registration.
		return fmt.Errorf("viewscaffold: view %q has no title", c.Name)
	}
	if strings.TrimSpace(c.Summary) == "" {
		return fmt.Errorf("viewscaffold: view %q has no summary", c.Name)
	}
	if c.NavOrder <= 0 {
		return fmt.Errorf("viewscaffold: view %q needs a positive nav order", c.Name)
	}
	return nil
}
