// Command gendocs's test that a real migration report fits the schema
// generated for it.
package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
)

// schemaFixture is a playbook whose report exercises every field: a
// converted task and a blocked one, a play-level finding and task-level
// ones with and without a native alternative, a resolved variable, a task
// that cannot run in check mode, and one that can.
const schemaFixture = `- hosts: web
  vars: {dir: /srv/app}
  tasks:
    - name: make the directory
      ansible.builtin.file: {path: "{{ dir }}", state: directory}
    - name: run a command
      ansible.builtin.command: make
      notify: restart
    - name: render
      ansible.builtin.template: {src: a.j2, dest: /etc/a}
`

// TestReportSchema_MatchesModel validates a real report against the
// generated schema, for the subset of JSON Schema the generator writes
// (types, properties, required, additionalProperties, enum, items, oneOf,
// $ref), and requires every property the schema declares to appear with a
// value somewhere in the report, so the fixture proves the whole schema
// rather than the part it happens to reach.
func TestReportSchema_MatchesModel(t *testing.T) {
	built, err := migrationReportSchema(filepath.Join("..", "..", reportModelFile))
	if err != nil {
		t.Fatal(err)
	}
	// Read the schema as a consumer does, from its JSON.
	encoded, err := json.Marshal(built)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	res, err := playbook.Translate(fstest.MapFS{"site.yml": {Data: []byte(schemaFixture)}}, "site.yml", playbook.Options{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(res.Report)
	if err != nil {
		t.Fatal(err)
	}
	var report any
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	v := &schemaValidator{defs: schema["$defs"].(map[string]any), seen: map[string]bool{}, seenNull: map[string]bool{}, nullable: map[string]bool{}}
	for _, problem := range v.check("report", report, schema) {
		t.Error(problem)
	}
	for _, declared := range v.declared {
		if !v.seen[declared] {
			t.Errorf("the fixture never gives %s a value, so the schema's claim about it is untested", declared)
		}
		if v.nullable[declared] && !v.seenNull[declared] {
			t.Errorf("the fixture never leaves %s null, so the schema's null alternative is untested", declared)
		}
	}
}

// schemaValidator checks a decoded JSON value against a generated schema.
type schemaValidator struct {
	defs map[string]any
	// declared and seen are every "Type.property" the schema declares and
	// the ones the value filled; nullable and seenNull are the ones whose
	// schema admits null (a slice's aside, which one report cannot both
	// fill and leave empty) and the ones the value left null.
	declared []string
	seen     map[string]bool
	nullable map[string]bool
	seenNull map[string]bool
}

// check returns every way v breaks schema, at path.
func (s *schemaValidator) check(path string, v any, schema map[string]any) []string {
	if ref, ok := schema["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/$defs/")
		def, _ := s.defs[name].(map[string]any)
		return s.object(path, name, v, def)
	}
	if options, ok := schema["oneOf"].([]any); ok {
		for _, o := range options {
			if len(s.check(path, v, o.(map[string]any))) == 0 {
				return nil
			}
		}
		return []string{fmt.Sprintf("%s matches none of its schema's alternatives", path)}
	}
	if enum, ok := schema["enum"].([]any); ok {
		if !slices.Contains(enum, v) {
			return []string{fmt.Sprintf("%s = %v is not one of %v", path, v, enum)}
		}
		return nil
	}
	if !typeMatches(v, schema["type"]) {
		return []string{fmt.Sprintf("%s = %v is not %v", path, v, schema["type"])}
	}
	if schema["properties"] != nil {
		return s.object(path, "Report", v, schema)
	}
	var problems []string
	if items, ok := v.([]any); ok {
		for i, item := range items {
			problems = append(problems, s.check(fmt.Sprintf("%s[%d]", path, i), item, schema["items"].(map[string]any))...)
		}
	}
	return problems
}

// object checks an object against its definition, typeName naming it.
func (s *schemaValidator) object(path, typeName string, v any, def map[string]any) []string {
	obj, ok := v.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("%s is not an object", path)}
	}
	props := def["properties"].(map[string]any)
	var problems []string
	for _, name := range sortedKeys(props) {
		key := typeName + "." + name
		if !slices.Contains(s.declared, key) {
			s.declared = append(s.declared, key)
		}
		value, present := obj[name]
		if present && value != nil {
			s.seen[key] = true
		}
		if admitsNull(props[name].(map[string]any)) {
			s.nullable[key] = true
			if present && value == nil {
				s.seenNull[key] = true
			}
		}
		if !present {
			if required, _ := def["required"].([]any); slices.Contains(required, any(name)) {
				problems = append(problems, fmt.Sprintf("%s lacks required %s", path, name))
			}
			continue
		}
		problems = append(problems, s.check(path+"."+name, value, props[name].(map[string]any))...)
	}
	for name := range obj {
		if _, ok := props[name]; !ok {
			problems = append(problems, fmt.Sprintf("%s has %s, which the schema does not declare", path, name))
		}
	}
	return problems
}

// admitsNull reports whether a property's schema offers null as its own
// alternative: a pointer's oneOf, or an enum listing null.
func admitsNull(schema map[string]any) bool {
	for _, o := range asList(schema["oneOf"]) {
		if o.(map[string]any)["type"] == "null" {
			return true
		}
	}
	return slices.Contains(asList(schema["enum"]), nil)
}

// asList returns v as a list, or nil.
func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

// typeMatches reports whether v is of JSON type want: one name, or a list.
func typeMatches(v any, want any) bool {
	names, _ := want.([]any)
	if name, ok := want.(string); ok {
		names = []any{name}
	}
	for _, name := range names {
		switch name {
		case "string":
			if _, ok := v.(string); ok {
				return true
			}
		case "integer":
			if f, ok := v.(float64); ok && f == float64(int64(f)) {
				return true
			}
		case "boolean":
			if _, ok := v.(bool); ok {
				return true
			}
		case "array":
			if _, ok := v.([]any); ok {
				return true
			}
		case "object":
			if _, ok := v.(map[string]any); ok {
				return true
			}
		case "null":
			if v == nil {
				return true
			}
		}
	}
	return false
}
