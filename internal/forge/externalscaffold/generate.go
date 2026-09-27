// Package externalscaffold: Generate, which renders a Config into a program's
// files.
package externalscaffold

import (
	"bytes"
	"fmt"
	"go/format"
	"strconv"
	"text/template"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
)

// GeneratedFile is one file Generate produces: a path relative to the
// program directory, and its contents. Go files are gofmt-clean.
type GeneratedFile struct {
	Path    string
	Content []byte
}

// templateData is the fully resolved, string-only data every template
// renders from, computed once from a validated Config so no template
// derives a name of its own.
type templateData struct {
	// Name is the method's fully-qualified name, for example
	// "acme.motd.read".
	Name string

	// FunctionName is the exported method body's name, for example
	// "Read".
	FunctionName string

	// Prefix is the lowered FunctionName every unexported identifier in
	// the method file starts with, for example "read".
	Prefix string

	// FileBase is the method file's base name, for example "read".
	FileBase string

	// BinaryName is the name the README builds the program under, for
	// example "acme-motd-read".
	BinaryName string

	// GoVersion is the go.mod's go line: the Go version The Pleiades itself
	// requires (goDirective).
	GoVersion string

	// EngineConstraint is the method's engine version constraint:
	// ">=" and the generating build's release, or empty on a development
	// build, in which case the manifest states none (Config.Engine).
	EngineConstraint string
}

// funcMap is shared by every template. quote is how a name reaches Go
// source: as a real Go string literal, never spliced raw between
// quotation marks. code wraps text in a Markdown inline code span for the
// README, whose template is a Go raw string and so cannot hold a backtick
// itself.
var funcMap = template.FuncMap{
	"quote": strconv.Quote,
	"code":  func(s string) string { return "`" + s + "`" },
}

var (
	mainTemplate   = template.Must(template.New("main").Funcs(funcMap).Parse(mainTemplateSource))
	methodTemplate = template.Must(template.New("method").Funcs(funcMap).Parse(methodTemplateSource))
	testTemplate   = template.Must(template.New("method_test").Funcs(funcMap).Parse(methodTestTemplateSource))
	readmeTemplate = template.Must(template.New("readme").Funcs(funcMap).Parse(readmeTemplateSource))
	goModTemplate  = template.Must(template.New("go.mod").Parse(goModTemplateSource))
)

// goDirective is the go line a generated go.mod carries: the one in
// The Pleiades's own go.mod, which is the oldest Go that can build a program
// importing it. TestGoDirectiveMatchesTheModule keeps the two equal.
const goDirective = "1.26.0"

// goModTemplateSource is the program's go.mod: a module line and a go
// line, and nothing else. There is no require, because the right version
// of The Pleiades is one only the go command can work out (go mod tidy
// resolves it through the module proxy and records its checksum), and no
// replace, because a replace to a local path is not checked against
// anything and means something different on every machine. The README
// shows both steps, for the author to take on purpose.
const goModTemplateSource = `module {{.BinaryName}}

go {{.GoVersion}}
`

// output is one file Generate renders: its path, its template, and
// whether it is Go source to format.
type output struct {
	path     string
	tmpl     *template.Template
	goSource bool
}

// Generate renders cfg into a complete external Collection program: its
// entry point, its one method, that method's test, a README, and a go.mod
// unless cfg.NoGoMod. It performs no filesystem I/O and returns paths
// relative to the program directory, in the order a reader would open
// them.
func Generate(cfg Config) ([]GeneratedFile, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	data := templateData{
		Name:         cfg.Name,
		FunctionName: cfg.FunctionName(),
		Prefix:       cfg.identPrefix(),
		FileBase:     cfg.FileBase(),
		BinaryName:   cfg.DefaultDir(),
		GoVersion:    goDirective,
	}
	if release, ok := buildinfo.Release(cfg.Engine); ok {
		data.EngineConstraint = ">=" + release
	}

	// Each entry pairs a template with the path it renders to and
	// whether go/format applies. README.md and go.mod are the files that
	// are not Go source, so they are the ones not run through the
	// formatter.
	outputs := []output{
		{path: "main.go", tmpl: mainTemplate, goSource: true},
		{path: data.FileBase + ".go", tmpl: methodTemplate, goSource: true},
		{path: data.FileBase + "_test.go", tmpl: testTemplate, goSource: true},
		{path: "README.md", tmpl: readmeTemplate},
	}
	if !cfg.NoGoMod {
		outputs = append(outputs, output{path: "go.mod", tmpl: goModTemplate})
	}

	files := make([]GeneratedFile, 0, len(outputs))
	for _, out := range outputs {
		content, err := render(out.tmpl, data, out.goSource)
		if err != nil {
			return nil, fmt.Errorf("externalscaffold: rendering %s: %w", out.path, err)
		}
		files = append(files, GeneratedFile{Path: out.path, Content: content})
	}
	return files, nil
}

// render executes tmpl against data and, for Go source, formats the result
// with go/format rather than a gofmt subprocess, so generated Go is
// deterministically gofmt-clean. A formatting failure carries the
// unformatted text, since that is the only place the broken line shows.
func render(tmpl *template.Template, data templateData, goSource bool) ([]byte, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing template: %w", err)
	}
	if !goSource {
		return buf.Bytes(), nil
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("formatting generated source: %w\n---\n%s", err, buf.String())
	}
	return formatted, nil
}
