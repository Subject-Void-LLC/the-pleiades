// Package factsreport: the report's JSON Schema, built by reflection from
// the model and described by its doc comments, as every published --json
// schema is (internal/jsonschema). tools/gendocs writes it, and the
// controller's tests hold the command's real output to it.
package factsreport

import (
	"path/filepath"
	"reflect"

	"github.com/Subject-Void-LLC/the-pleiades/internal/jsonschema"
)

// SchemaFile is the schema's name under docs/reference/schemas/.
const SchemaFile = "facts-inspection.json"

// modelFiles are the repository-relative Go files whose doc comments
// describe the model.
var modelFiles = []string{"internal/facts/factsreport/report.go"}

// Contract is the report's compatibility rule, as its schema states it.
const Contract = "schema_version is the major version: it goes up only for a change an existing reader could misread, a field removed or renamed or given another type or meaning. Within a major version a newer producer may add fields, and new values to the open sets (a state, a failure kind, a role, a repair). So a reader ignores a field it does not know, treats a state it does not know as needing repair, and refuses a major version it does not know."

// Schema returns the report's schema, its descriptions read from the model
// files under the repository root given. It is open where the contract is:
// an object may carry a property it does not declare, and a field's known
// values are examples, not an enum.
func Schema(root string) (map[string]any, error) {
	paths := make([]string, 0, len(modelFiles))
	for _, f := range modelFiles {
		paths = append(paths, filepath.Join(root, f))
	}
	docs, err := jsonschema.FilesDocs(paths...)
	if err != nil {
		return nil, err
	}
	b := &jsonschema.Builder{Docs: docs, Defs: map[string]any{}, Source: "the facts inspection report's model files", Open: true, AbsentNotNull: true,
		// A gather's number is the database's int64, written as an integer.
		Fixed: map[reflect.Type]map[string]any{reflect.TypeFor[int64](): {"type": "integer"}},
		Known: map[string][]string{
			"Report.Scope":    {ScopeDevice, ScopeFleet},
			"Device.State":    KnownStates,
			"KeyedRows.Role":  {RoleCurrent, RolePrevious, RoleOther},
			"KeyCheck.Role":   {RoleCurrent, RolePrevious},
			"RowFailure.Kind": KnownFailureKinds,
		}}
	schema, err := b.Root(reflect.TypeFor[Report](), "Pleiades facts inspection")
	if err != nil {
		return nil, err
	}
	schema["description"] = schema["description"].(string) + " Printed by controller facts inspect --json. " + Contract
	return schema, nil
}
