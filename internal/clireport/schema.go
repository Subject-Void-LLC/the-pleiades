// The JSON Schema of each model, built by reflection from its types and
// described by their doc comments. tools/gendocs writes them under
// docs/reference/schemas/, and cmd/pleiades's tests validate the command's
// real output against the same schemas.
package clireport

import (
	"path/filepath"
	"reflect"

	"github.com/Subject-Void-LLC/the-pleiades/internal/jsonschema"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// Schema is one published schema: the file tools/gendocs writes, the model
// it describes, and the Go files whose doc comments describe that model.
type Schema struct {
	// File is the schema's name under docs/reference/schemas/.
	File string
	// Command is the command whose --json output it describes.
	Command string
	// Title is the schema's title.
	Title string
	// Model is the type the command encodes.
	Model reflect.Type
	// Known lists, by "Type.Field", the values a field holds today, which
	// the schema publishes as examples of an open set.
	Known map[string][]string
	// ModelFiles are the repository-relative Go files whose doc comments
	// describe Model and every type it holds.
	ModelFiles []string
}

// The published schemas, each by the model it describes.
var (
	// RunReportSchema is RunReport's, the report of a run, an ad-hoc call
	// and a rollback.
	RunReportSchema = Schema{
		File: "run-report.json", Command: "pleiades run, adhoc and rollback --json", Title: "Pleiades run report",
		Model: reflect.TypeFor[RunReport](),
		Known: map[string][]string{
			"RunReport.Mode":      {string(collection.ModeExecute), string(collection.ModeCheck)},
			"PlanSection.Section": {"pretasks", "tasks", "posttasks"},
			"PlanTask.Kind":       {"block", "parallel"},
			"TaskReport.Status":   KnownTaskStatuses,
			"RunOutcome.Status":   KnownOutcomeStatuses,
			"RunWarning.Kind":     KnownWarningKinds,
			"RunWarning.When":     {"start", "end"},
		},
		ModelFiles: []string{"internal/clireport/run.go", "internal/rollback/plan.go", "pkg/collection/collection.go"},
	}
	// FiltersSchema is FiltersTopic's.
	FiltersSchema = Schema{
		File: "doc-filters.json", Command: "pleiades doc filters --json", Title: "Pleiades doc filters",
		Model:      reflect.TypeFor[FiltersTopic](),
		ModelFiles: []string{"internal/clireport/topics.go", "internal/render/filter.go", "internal/render/syntax_doc.go"},
	}
	// ConditionsSchema is ConditionsTopic's.
	ConditionsSchema = Schema{
		File: "doc-conditions.json", Command: "pleiades doc conditions --json", Title: "Pleiades doc conditions",
		Model:      reflect.TypeFor[ConditionsTopic](),
		ModelFiles: []string{"internal/clireport/topics.go", "internal/engine/cel_docs.go"},
	}
)

// Schemas are every schema this package publishes.
var Schemas = []Schema{RunReportSchema, FiltersSchema, ConditionsSchema}

// Build returns the schema, its descriptions read from ModelFiles under the
// repository root given. It is open where the contract is: an object may
// carry a property it does not declare, and a field's known values are
// examples, not an enum. The root's description carries the package's
// contract, so a reader of the schema alone reads the rules.
func (s Schema) Build(root string) (map[string]any, error) {
	paths := make([]string, 0, len(s.ModelFiles))
	for _, f := range s.ModelFiles {
		paths = append(paths, filepath.Join(root, f))
	}
	docs, err := jsonschema.FilesDocs(paths...)
	if err != nil {
		return nil, err
	}
	b := &jsonschema.Builder{Docs: docs, Source: "the model files of " + s.Title, Known: s.Known, Open: true, AbsentNotNull: true}
	schema, err := b.Root(s.Model, s.Title)
	if err != nil {
		return nil, err
	}
	schema["description"] = schema["description"].(string) + " Printed by " + s.Command + ". " + Contract
	return schema, nil
}

// Contract is the package's compatibility rule, as each schema states it.
const Contract = "schema_version is the major version: it goes up only for a change an existing reader could misread, a field removed or renamed or given another type or meaning. Within a major version a newer producer may add fields, and new values to the open sets. So a reader ignores a field it does not know, treats a status or kind it does not know as not a pass, and refuses a major version it does not know."
