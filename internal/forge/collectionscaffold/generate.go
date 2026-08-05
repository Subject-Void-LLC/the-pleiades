package collectionscaffold

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

// templateData is the fully-resolved, string-only data the templates
// render from. Capabilities is []string (not []capability.Name): the
// conversion happens once here in Go code, so the templates' "quote"
// function only ever needs to quote a plain string.
type templateData struct {
	Name              string
	PackageName       string
	PackagePath       string
	FunctionName      string
	Capabilities      []string
	Transports        []string
	RequiresElevation bool
	EngineVersion     string
}

var funcMap = template.FuncMap{
	"quote": strconv.Quote,
}

var (
	collectionTemplate = template.Must(template.New("collection").Funcs(funcMap).Parse(collectionTemplateSource))
	testTemplate       = template.Must(template.New("collection_test").Funcs(funcMap).Parse(collectionTestTemplateSource))
)

// Generate renders cfg into a new namespaced Collection method package:
// the source file and its starter test. It performs no filesystem I/O and
// returns paths relative to the repository root; the caller
// (cmd/pleiades/forge_new_collection.go) decides where, or whether, to
// write the returned files.
func Generate(cfg Config) ([]GeneratedFile, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	data := templateData{
		Name:              cfg.Name,
		PackageName:       cfg.PackageName(),
		PackagePath:       cfg.PackagePath(),
		FunctionName:      cfg.FunctionName(),
		Capabilities:      make([]string, 0, len(cfg.Capabilities)),
		Transports:        cfg.Transports,
		RequiresElevation: cfg.RequiresElevation,
		EngineVersion:     cfg.EngineVersion,
	}
	for _, c := range cfg.Capabilities {
		data.Capabilities = append(data.Capabilities, string(c))
	}

	sourceFile, err := renderAndFormat(collectionTemplate, data)
	if err != nil {
		return nil, fmt.Errorf("collectionscaffold: rendering %s.go: %w", cfg.MethodSegment(), err)
	}
	testFile, err := renderAndFormat(testTemplate, data)
	if err != nil {
		return nil, fmt.Errorf("collectionscaffold: rendering %s_test.go: %w", cfg.MethodSegment(), err)
	}

	base := "internal/catalog/" + cfg.PackagePath() + "/"
	return []GeneratedFile{
		{Path: base + cfg.MethodSegment() + ".go", Content: sourceFile},
		{Path: base + cfg.MethodSegment() + "_test.go", Content: testFile},
	}, nil
}

// renderAndFormat executes tmpl and formats the result via go/format
// (rather than shelling out to a gofmt binary), so generated output is
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
