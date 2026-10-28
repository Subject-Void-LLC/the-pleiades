// Command gendocs's JSON Schemas of the documents `pleiades` prints with
// --json for a tool to read: the run report and the doc topics, each built
// by internal/clireport from its model's types and doc comments.
package main

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
)

// generateCLIReportSchemas writes every clireport.Schemas entry under
// outDir's schemas directory, read from the repository root gendocs runs
// in. Nothing serves them: they are the contract a reader of the command's
// output checks against.
func generateCLIReportSchemas(outDir string) error {
	for _, s := range clireport.Schemas {
		schema, err := s.Build(".")
		if err != nil {
			return fmt.Errorf("%s: %w", s.File, err)
		}
		if err := writeDocsSchema(outDir, s.File, schema); err != nil {
			return err
		}
	}
	return nil
}
