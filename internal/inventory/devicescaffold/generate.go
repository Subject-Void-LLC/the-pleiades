package devicescaffold

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
	Vendor          string
	TypeKey         string
	StructName      string
	ConstructorName string
	ReceiverName    string
	Capabilities    []string
}

var funcMap = template.FuncMap{
	"quote": strconv.Quote,
}

var (
	deviceTemplate = template.Must(template.New("device").Funcs(funcMap).Parse(deviceTemplateSource))
	testTemplate   = template.Must(template.New("device_test").Funcs(funcMap).Parse(deviceTestTemplateSource))
)

// Generate renders cfg into a new vendor device-type package: the vendor
// source file and its starter test. It performs no filesystem I/O and
// returns paths relative to the repository root; the caller
// (cmd/pleiades/forge_new_device.go) decides where, or whether, to write
// the returned files.
func Generate(cfg Config) ([]GeneratedFile, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	data := templateData{
		Vendor:          cfg.Vendor,
		TypeKey:         cfg.TypeKey,
		StructName:      cfg.StructName(),
		ConstructorName: "New" + cfg.StructName(),
		ReceiverName:    cfg.Vendor[:1],
		Capabilities:    make([]string, 0, len(cfg.Capabilities)),
	}
	for _, c := range cfg.Capabilities {
		data.Capabilities = append(data.Capabilities, string(c))
	}

	sourceFile, err := renderAndFormat(deviceTemplate, data)
	if err != nil {
		return nil, fmt.Errorf("devicescaffold: rendering %s.go: %w", cfg.Kind(), err)
	}
	testFile, err := renderAndFormat(testTemplate, data)
	if err != nil {
		return nil, fmt.Errorf("devicescaffold: rendering %s_test.go: %w", cfg.Kind(), err)
	}

	base := "internal/inventory/devices/" + cfg.Vendor + "/"
	return []GeneratedFile{
		{Path: base + cfg.Kind() + ".go", Content: sourceFile},
		{Path: base + cfg.Kind() + "_test.go", Content: testFile},
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
