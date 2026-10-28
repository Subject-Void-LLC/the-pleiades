// The doc topics' models: `pleiades doc filters --json` and `pleiades doc
// conditions --json`.
package clireport

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// FiltersVersion is FiltersTopic's major version.
const FiltersVersion = 1

// ConditionsVersion is ConditionsTopic's major version.
const ConditionsVersion = 1

// DocRoot is one name a template or condition reads, and what it holds.
type DocRoot struct {
	// Name is the name as a template or condition writes it.
	Name string `json:"name"`
	// Holds says what the name holds, in a sentence.
	Holds string `json:"holds"`
}

// FiltersTopic is `pleiades doc filters --json`: what a task parameter's {{
// }} template may read and call, the whole closed set.
type FiltersTopic struct {
	// SchemaVersion is this document's major version, FiltersVersion.
	SchemaVersion int `json:"schema_version"`
	// Roots are the names a template may read.
	Roots []DocRoot `json:"roots"`
	// Filters are every filter a template may use, by name.
	Filters []render.FilterDoc `json:"filters"`
	// Excluded are the Jinja2 filters left out on purpose, each with why.
	Excluded []render.ExcludedFilter `json:"excluded"`
	// Statements are every statement a template may use.
	Statements []render.SyntaxDoc `json:"statements"`
	// Operators are every operator, loosest first.
	Operators []render.SyntaxDoc `json:"operators"`
	// Tests are every test, written x is name or x is not name.
	Tests []render.SyntaxDoc `json:"tests"`
	// NotSupported are the Jinja2 forms this renderer does not have, each
	// with why.
	NotSupported []render.ExcludedFilter `json:"not_supported"`
}

// ConditionsTopic is `pleiades doc conditions --json`: what a when, when_or
// or when_cel condition may read and call.
type ConditionsTopic struct {
	// SchemaVersion is this document's major version, ConditionsVersion.
	SchemaVersion int `json:"schema_version"`
	// Forms are the three ways a task is gated.
	Forms []DocRoot `json:"forms"`
	// Roots are the names a condition may read.
	Roots []DocRoot `json:"roots"`
	// Libraries are what the condition language adds beyond CEL's own
	// operators and macros, one entry per library, read from the engine.
	Libraries []engine.ConditionLibrary `json:"libraries"`
}
