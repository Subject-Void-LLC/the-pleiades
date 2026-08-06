// Package pluginscaffold generates a new inventory sync plugin package:
// the PLAN.md Section 6a connector every external inventory source
// implements.
//
// It is the Forge's third generator, after internal/inventory/devicescaffold
// (device types) and internal/forge/collectionscaffold (Collection methods),
// and it deliberately copies their shape rather than inventing a fourth
// approach: raw-string text/template sources post-processed through
// go/format.Source, no filesystem I/O in this package, and paths returned
// relative to the repository root for the caller to write or discard.
//
// Why a generator rather than hand-writing each plugin: the parts of a sync
// plugin that are easy to get wrong are the parts that are identical across
// every plugin. Registering into the right registry, refusing rather than
// faking while still declared, quarantining instead of erroring in
// Classify, and delegating reconciliation to the shared driver are all
// decisions that should be made once and reproduced exactly, not
// re-derived by whoever writes the next connector.
package pluginscaffold

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
	Name        string
	PackageName string
	TypeName    string
	Description string
	Endpoint    string
	ReadOnly    bool
}

var funcMap = template.FuncMap{
	"quote": strconv.Quote,
}

var (
	pluginTemplate     = template.Must(template.New("plugin").Funcs(funcMap).Parse(pluginTemplateSource))
	pluginTestTemplate = template.Must(template.New("plugin_test").Funcs(funcMap).Parse(pluginTestTemplateSource))
)

// Generate renders cfg into a new sync plugin package: the skeleton and its
// starter test. It performs no filesystem I/O; the caller
// (cmd/pleiades/forge_new_plugin.go) decides where, or whether, to write
// the returned files.
func Generate(cfg Config) ([]GeneratedFile, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	data := templateData{
		Name:        cfg.Name,
		PackageName: cfg.PackageName(),
		TypeName:    cfg.TypeName(),
		Description: cfg.Description,
		Endpoint:    cfg.Endpoint,
		ReadOnly:    cfg.ReadOnly,
	}

	sourceFile, err := renderAndFormat(pluginTemplate, data)
	if err != nil {
		return nil, fmt.Errorf("pluginscaffold: rendering %s.go: %w", cfg.PackageName(), err)
	}
	testFile, err := renderAndFormat(pluginTestTemplate, data)
	if err != nil {
		return nil, fmt.Errorf("pluginscaffold: rendering %s_test.go: %w", cfg.PackageName(), err)
	}

	base := cfg.PackagePath() + "/"
	return []GeneratedFile{
		{Path: base + cfg.PackageName() + ".go", Content: sourceFile},
		{Path: base + cfg.PackageName() + "_test.go", Content: testFile},
	}, nil
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
