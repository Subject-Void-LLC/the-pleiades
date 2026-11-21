// Command gendocs's JSON Schema of `controller facts inspect --json`, the
// contract a script reads instead of the inspection's text, built from
// internal/facts/factsreport's model.
package main

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factsreport"
)

// generateFactsInspectionSchema writes factsreport's schema under outDir's
// schemas directory, read from the repository root gendocs runs in. Nothing
// serves it: it is the contract a reader of the command's output checks
// against.
func generateFactsInspectionSchema(outDir string) error {
	schema, err := factsreport.Schema(".")
	if err != nil {
		return err
	}
	return writeDocsSchema(outDir, factsreport.SchemaFile, schema)
}
