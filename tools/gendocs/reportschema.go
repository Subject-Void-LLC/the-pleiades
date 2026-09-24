// Command gendocs's JSON Schema of the migration report, built by
// reflection from the report model and described by its doc comments.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
)

// reportModelFile is the Go file whose doc comments describe the report,
// read so the schema's descriptions have one source: the model's own.
const reportModelFile = "internal/forge/playbook/model.go"

// generateMigrationReportSchema writes the JSON Schema of `pleiades forge
// migrate-playbook --json`, for an editor extension or a script reading
// the report. It is built by reflection from playbook.Report, so it cannot
// list a field the report lacks or miss one it has, and it is written to
// docs only: nothing serves it.
func generateMigrationReportSchema(outDir string) error {
	schema, err := migrationReportSchema(reportModelFile)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(schema); err != nil {
		return err
	}
	dir := filepath.Join(outDir, "schemas")
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- generated docs, not secret material
		return err
	}
	return os.WriteFile(filepath.Join(dir, "migration-report.json"), buf.Bytes(), 0o644) // #nosec G306 -- generated docs, not secret material
}

// migrationReportSchema builds the report's schema, its descriptions read
// from modelFile.
func migrationReportSchema(modelFile string) (map[string]any, error) {
	docs, err := fieldDocs(modelFile)
	if err != nil {
		return nil, err
	}
	s := &schemaBuilder{docs: docs, defs: map[string]any{}}
	root, err := s.object(reflect.TypeFor[playbook.Report]())
	if err != nil {
		return nil, err
	}
	root["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	root["title"] = "Pleiades migration report"
	root["$defs"] = s.defs
	return root, nil
}

// fieldDocs reads every struct type's doc comment and each field's, from
// file, keyed "Type" and "Type.Field".
func fieldDocs(file string) (map[string]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	docs := map[string]string{}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			docs[ts.Name.Name] = oneLine(gen.Doc.Text())
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					docs[ts.Name.Name+"."+name.Name] = oneLine(field.Doc.Text())
				}
			}
		}
	}
	return docs, nil
}

// oneLine joins a doc comment's lines into one paragraph.
func oneLine(doc string) string {
	return strings.Join(strings.Fields(doc), " ")
}

// schemaBuilder turns the report's Go types into JSON Schema, each struct
// once under $defs.
type schemaBuilder struct {
	docs map[string]string
	defs map[string]any
}

// of returns t's schema.
func (s *schemaBuilder) of(t reflect.Type) (map[string]any, error) {
	switch t {
	case reflect.TypeFor[playbook.Class]():
		// An undecided class is null, so it cannot read as a decided one.
		var names []any
		for c := playbook.ClassAsserted; c <= playbook.ClassObserve; c++ {
			names = append(names, c.String())
		}
		return map[string]any{"enum": append(names, nil)}, nil
	case reflect.TypeFor[playbook.Outcome]():
		var names []any
		for o := playbook.OutcomeConverted; o <= playbook.OutcomeBlocked; o++ {
			names = append(names, o.String())
		}
		return map[string]any{"enum": names}, nil
	case reflect.TypeFor[playbook.Code]():
		var codes []string
		for c := range playbook.Codes() {
			codes = append(codes, string(c))
		}
		slices.Sort(codes)
		return map[string]any{"enum": codes}, nil
	}
	switch t.Kind() {
	case reflect.Pointer:
		elem, err := s.of(t.Elem())
		if err != nil {
			return nil, err
		}
		return map[string]any{"oneOf": []any{elem, map[string]any{"type": "null"}}}, nil
	case reflect.Struct:
		if _, done := s.defs[t.Name()]; !done {
			s.defs[t.Name()] = nil // taken, so a type that holds itself stops here
			obj, err := s.object(t)
			if err != nil {
				return nil, err
			}
			s.defs[t.Name()] = obj
		}
		return map[string]any{"$ref": "#/$defs/" + t.Name()}, nil
	case reflect.Slice:
		items, err := s.of(t.Elem())
		if err != nil {
			return nil, err
		}
		// A nil slice encodes as null.
		return map[string]any{"type": []any{"array", "null"}, "items": items}, nil
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Int:
		return map[string]any{"type": "integer"}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	}
	return nil, fmt.Errorf("the report holds a %s, which this schema does not describe", t)
}

// object returns t's object schema: each serialized field a property,
// described by its doc comment, required unless omitempty.
func (s *schemaBuilder) object(t reflect.Type) (map[string]any, error) {
	props := map[string]any{}
	var required []string
	for i := range t.NumField() {
		f := t.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !f.IsExported() || name == "-" {
			continue
		}
		doc := s.docs[t.Name()+"."+f.Name]
		if doc == "" {
			return nil, fmt.Errorf("%s.%s has no doc comment in %s, so the schema cannot describe it", t.Name(), f.Name, reportModelFile)
		}
		prop, err := s.of(f.Type)
		if err != nil {
			return nil, err
		}
		prop["description"] = doc
		props[name] = prop
		if !strings.Contains(opts, "omitempty") {
			required = append(required, name)
		}
	}
	return map[string]any{
		"type":                 "object",
		"description":          s.docs[t.Name()],
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}, nil
}
