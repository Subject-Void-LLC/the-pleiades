// Package clireport holds the models `pleiades` prints with --json, where a
// tool or an agent reads them as a contract: the run report of `pleiades
// run`, `pleiades adhoc` and `pleiades rollback`, and the `pleiades doc
// filters` and `pleiades doc conditions` topics. Each command's text view
// renders the same model, so the two cannot disagree. tools/gendocs
// publishes each model's JSON Schema under docs/reference/schemas/ from
// these types and their doc comments (schema.go), so a schema cannot drift
// from the code, and cmd/pleiades's tests validate the command's real output
// against it.
//
// # The contract
//
// schema_version is the major version. It goes up only for a change an
// existing reader could misread: a field removed or renamed, or given
// another type or meaning. Within a major version a newer producer may add
// fields, and new values to the open sets (task statuses, outcome statuses,
// warning kinds). So a reader:
//   - ignores a field it does not know;
//   - treats a status or kind it does not know as not a pass, and shows it
//     as it is;
//   - refuses a major version it does not know, rather than guessing.
package clireport
