// Where the Crawl CLI keeps devices' facts (Phase 41): .pleiades/facts in the
// project, beside the journal.
package main

import "path/filepath"

// factsDir is the project's fact store directory.
func factsDir(projectDir string) string {
	return filepath.Join(projectDir, ".pleiades", "facts")
}
