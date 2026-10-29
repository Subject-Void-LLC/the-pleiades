// Package jsonschema builds the JSON Schema of a Go model by reflection, its
// descriptions read from the model's own doc comments, and checks a decoded
// document against such a schema.
//
// It is how every --json document a tool reads as a contract gets a
// published schema that cannot drift from the code: tools/gendocs writes the
// schema from the model's types, and a test validates the command's real
// output against the same schema. The builder writes only the subset of JSON
// Schema the validator checks (type, properties, required,
// additionalProperties, enum, items, oneOf, $ref and examples), so the two
// agree on what a schema means.
package jsonschema

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"reflect"
	"strings"
	"time"
)

// Builder turns a model's Go types into JSON Schema, each struct once under
// Defs. Its zero options describe a closed contract: an object refuses a
// property it does not declare, and a nil pointer is null.
type Builder struct {
	// Docs holds each struct type's doc comment and each field's, keyed
	// "Type" and "Type.Field", as FieldDocs reads them.
	Docs map[string]string
	// Defs collects each struct's schema by its Go type name.
	Defs map[string]any
	// Source names where Docs came from, for an error about a field with
	// no doc comment.
	Source string
	// Known lists, by "Type.Field", the values a field holds today, which
	// the schema publishes as examples and describes as an open set.
	Known map[string][]string
	// Fixed gives the schema of a type the reflection would describe
	// wrongly, such as an enum held as an integer and written as its name.
	Fixed map[reflect.Type]map[string]any
	// Open leaves an object open to properties it does not declare, for a
	// contract whose producer may add fields within a major version.
	Open bool
	// AbsentNotNull writes an omitempty pointer as its value's schema,
	// since a nil one is absent, never null.
	AbsentNotNull bool
}

// Root returns the schema document for t: t's object schema with the draft,
// the title and every definition it reached.
func (s *Builder) Root(t reflect.Type, title string) (map[string]any, error) {
	if s.Defs == nil {
		s.Defs = map[string]any{}
	}
	root, err := s.Object(t)
	if err != nil {
		return nil, err
	}
	root["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	root["title"] = title
	root["$defs"] = s.Defs
	return root, nil
}

// Of returns t's schema.
func (s *Builder) Of(t reflect.Type) (map[string]any, error) {
	if fixed, ok := s.Fixed[t]; ok {
		// A copy, since Object writes each property's description into the
		// map it is given.
		return maps.Clone(fixed), nil
	}
	if t == reflect.TypeFor[time.Time]() {
		return map[string]any{"type": "string", "format": "date-time"}, nil
	}
	switch t.Kind() {
	case reflect.Pointer:
		elem, err := s.Of(t.Elem())
		if err != nil {
			return nil, err
		}
		return map[string]any{"oneOf": []any{elem, map[string]any{"type": "null"}}}, nil
	case reflect.Struct:
		if _, done := s.Defs[t.Name()]; !done {
			s.Defs[t.Name()] = nil // taken, so a type that holds itself stops here
			obj, err := s.Object(t)
			if err != nil {
				return nil, err
			}
			s.Defs[t.Name()] = obj
		}
		return map[string]any{"$ref": "#/$defs/" + t.Name()}, nil
	case reflect.Slice:
		items, err := s.Of(t.Elem())
		if err != nil {
			return nil, err
		}
		// A nil slice encodes as null.
		return map[string]any{"type": []any{"array", "null"}, "items": items}, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			break
		}
		values, err := s.Of(t.Elem())
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "object", "additionalProperties": values}, nil
	case reflect.Interface:
		// An any holds whatever a method reported: every JSON value fits,
		// which a schema with no type says.
		return map[string]any{}, nil
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Int:
		return map[string]any{"type": "integer"}, nil
	case reflect.Float64:
		return map[string]any{"type": "number"}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	}
	return nil, fmt.Errorf("the model holds a %s, which this schema does not describe", t)
}

// Object returns t's object schema: each serialized field a property,
// described by its doc comment, required unless omitempty. A field with no
// JSON name is written under its Go name, as encoding/json writes it.
func (s *Builder) Object(t reflect.Type) (map[string]any, error) {
	props := map[string]any{}
	var required []string
	for i := range t.NumField() {
		f := t.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !f.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		doc := s.Docs[t.Name()+"."+f.Name]
		if doc == "" {
			return nil, fmt.Errorf("%s.%s has no doc comment in %s, so the schema cannot describe it", t.Name(), f.Name, s.Source)
		}
		ft := f.Type
		if s.AbsentNotNull && ft.Kind() == reflect.Pointer && strings.Contains(opts, "omitempty") {
			ft = ft.Elem()
		}
		prop, err := s.Of(ft)
		if err != nil {
			return nil, err
		}
		if values, ok := s.Known[t.Name()+"."+f.Name]; ok {
			prop["examples"] = values
			doc += " Known values: " + strings.Join(values, ", ") + ". A newer producer may add others, and a reader treats one it does not know as not a pass."
		}
		prop["description"] = doc
		props[name] = prop
		if !strings.Contains(opts, "omitempty") {
			required = append(required, name)
		}
	}
	obj := map[string]any{
		"type":        "object",
		"description": s.Docs[t.Name()],
		"properties":  props,
		"required":    required,
	}
	if !s.Open {
		obj["additionalProperties"] = false
	}
	return obj, nil
}

// FieldDocs reads every struct type's doc comment and each field's, from
// file, keyed "Type" and "Type.Field". A comment above several names on one
// line describes each of them.
func FieldDocs(file string) (map[string]string, error) {
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
			docs[ts.Name.Name] = OneLine(gen.Doc.Text())
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			// last is the doc of the field before, which documents a field
			// with none of its own when it names that field: "DeviceID and
			// DeviceName name the device" above two lines.
			var last string
			for _, field := range st.Fields.List {
				doc := OneLine(field.Doc.Text())
				for _, name := range field.Names {
					switch {
					case doc != "":
						docs[ts.Name.Name+"."+name.Name] = doc
					case namesField(last, name.Name):
						docs[ts.Name.Name+"."+name.Name] = last
					default:
						docs[ts.Name.Name+"."+name.Name] = ""
					}
				}
				if doc != "" || len(field.Names) == 0 || !namesField(last, field.Names[0].Name) {
					last = doc
				}
			}
		}
	}
	return docs, nil
}

// FilesDocs is FieldDocs over several files, merged.
func FilesDocs(files ...string) (map[string]string, error) {
	docs := map[string]string{}
	for _, file := range files {
		d, err := FieldDocs(file)
		if err != nil {
			return nil, err
		}
		maps.Copy(docs, d)
	}
	return docs, nil
}

// namesField reports whether doc names field as a whole word.
func namesField(doc, field string) bool {
	for _, word := range strings.FieldsFunc(doc, func(r rune) bool { return r == ' ' || r == ',' || r == '.' || r == ':' || r == ';' }) {
		if word == field {
			return true
		}
	}
	return false
}

// OneLine joins a doc comment's lines into one paragraph.
func OneLine(doc string) string {
	return strings.Join(strings.Fields(doc), " ")
}
