// Package viewscaffold generates a new web UI view resource package: the
// Phase 19 presentation-layer extension point.
//
// It is the Forge's fourth generator, after devicescaffold (device types),
// collectionscaffold (Collection methods) and pluginscaffold (sync plugins),
// and it deliberately copies their shape rather than inventing a fourth
// approach: raw-string text/template sources post-processed through
// go/format.Source, no filesystem I/O in this package, and paths returned
// relative to the repository root for the caller to write or discard.
//
// It was written last, after all six built-in views existed, inverting the
// instinct to build the generator first. A generator emits the shape it was
// given; if that shape has never been built by hand, it will confidently
// emit the wrong one and every future view will inherit the mistake. This is
// the same order pluginscaffold arrived in -- after staticyaml, not before.
//
// Why a generator at all, when a view is only two files: the parts that are
// easy to get wrong are the parts identical across every view. Registering
// into the right registry, starting declared rather than pretending to work,
// declaring fields that carry a label and a validated autocomplete token,
// and remembering that a package nobody imports never runs. Those are
// decisions to reproduce exactly, not re-derive.
package viewscaffold

import (
	"bytes"
	"fmt"
	"go/format"
	"strconv"
	"text/template"
)

// GeneratedFile is one file Generate produces: a path relative to the
// repository root, and its gofmt-clean contents.
type GeneratedFile struct {
	Path    string
	Content []byte
}

// templateData is the fully-resolved, string-only data the templates render
// from, so the templates' quote function only ever sees a plain string.
type templateData struct {
	Name             string
	PackageName      string
	Title            string
	ResolvedNavLabel string
	NavOrder         int
	Summary          string
}

var funcMap = template.FuncMap{
	"quote": strconv.Quote,
}

var (
	viewTemplate     = template.Must(template.New("view").Funcs(funcMap).Parse(viewTemplateSource))
	viewTestTemplate = template.Must(template.New("view_test").Funcs(funcMap).Parse(viewTestTemplateSource))
)

// Generate renders cfg into a new view resource package: the descriptor and
// its starter test. It performs no filesystem I/O; the caller
// (cmd/pleiades/forge_new_view.go) decides where, or whether, to write the
// returned files.
func Generate(cfg Config) ([]GeneratedFile, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	data := templateData{
		Name:             cfg.Name,
		PackageName:      cfg.PackageName(),
		Title:            cfg.Title,
		ResolvedNavLabel: cfg.ResolvedNavLabel(),
		NavOrder:         cfg.NavOrder,
		Summary:          cfg.Summary,
	}

	sourceFile, err := renderAndFormat(viewTemplate, data)
	if err != nil {
		return nil, fmt.Errorf("viewscaffold: rendering %s.go: %w", cfg.PackageName(), err)
	}
	testFile, err := renderAndFormat(viewTestTemplate, data)
	if err != nil {
		return nil, fmt.Errorf("viewscaffold: rendering %s_test.go: %w", cfg.PackageName(), err)
	}

	base := cfg.PackagePath() + "/"
	return []GeneratedFile{
		{Path: base + cfg.PackageName() + ".go", Content: sourceFile},
		{Path: base + cfg.PackageName() + "_test.go", Content: testFile},
	}, nil
}

// Reminder is the wiring step no generator can perform, rendered for cfg.
//
// It is returned as a value rather than printed here, because this package
// performs no I/O -- but it exists at all because the step it describes
// fails silently. A view absent from registrars.go produces no error, no
// warning and no route; it simply is not there.
func Reminder(cfg Config) string {
	return fmt.Sprintf(reminderSource, cfg.PackagePath(), cfg.PackageName(), cfg.ImportPath())
}

// renderAndFormat executes tmpl and formats the result via go/format rather
// than shelling out to a gofmt binary, so generated output is
// deterministically gofmt-clean with no subprocess involved.
func renderAndFormat(tmpl *template.Template, data templateData) ([]byte, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing template: %w", err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("formatting generated source: %w\n---\n%s", err, buf.String())
	}
	return formatted, nil
}
