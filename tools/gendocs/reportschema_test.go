// Command gendocs's test that a real migration report fits the schema
// generated for it.
package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/jsonschema"
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
    - name: wait for a person
      ansible.builtin.pause: {prompt: continue}
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
	v := jsonschema.NewValidator(schema)
	for _, problem := range v.Check("report", report, schema) {
		t.Error(problem)
	}
	for _, declared := range v.Declared {
		if !v.Seen[declared] {
			t.Errorf("the fixture never gives %s a value, so the schema's claim about it is untested", declared)
		}
		if v.Nullable[declared] && !v.SeenNull[declared] {
			t.Errorf("the fixture never leaves %s null, so the schema's null alternative is untested", declared)
		}
	}
}
