// Command gendocs's JSON Schema of the migration report, built by
// reflection from the report model and described by its doc comments.
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/jsonschema"
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
	return writeDocsSchema(outDir, "migration-report.json", schema)
}

// writeDocsSchema writes schema as indented JSON to name under outDir's
// schemas directory only: unlike writeSchema's, nothing serves it.
func writeDocsSchema(outDir, name string, schema map[string]any) error {
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
	return os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644) // #nosec G306 -- generated docs, not secret material
}

// migrationReportSchema builds the report's schema, its descriptions read
// from modelFile.
func migrationReportSchema(modelFile string) (map[string]any, error) {
	docs, err := jsonschema.FieldDocs(modelFile)
	if err != nil {
		return nil, err
	}
	s := &jsonschema.Builder{Docs: docs, Defs: map[string]any{}, Source: reportModelFile, Fixed: playbookSchemas()}
	return s.Root(reflect.TypeFor[playbook.Report](), "Pleiades migration report")
}

// playbookSchemas are the report's enums, each written as its names rather
// than the integer reflection sees.
func playbookSchemas() map[reflect.Type]map[string]any {
	// An undecided class is null, so it cannot read as a decided one.
	var classes []any
	for c := playbook.ClassAsserted; c <= playbook.ClassObserve; c++ {
		classes = append(classes, c.String())
	}
	var outcomes []any
	for o := playbook.OutcomeConverted; o <= playbook.OutcomeBlocked; o++ {
		outcomes = append(outcomes, o.String())
	}
	var codes []string
	for c := range playbook.Codes() {
		codes = append(codes, string(c))
	}
	slices.Sort(codes)
	return map[reflect.Type]map[string]any{
		reflect.TypeFor[playbook.Class]():   {"enum": append(classes, nil)},
		reflect.TypeFor[playbook.Outcome](): {"enum": outcomes},
		reflect.TypeFor[playbook.Code]():    {"enum": codes},
	}
}
