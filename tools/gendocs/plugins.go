package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/catalogdata"
)

// handWrittenPlugins names the one sync plugin that predates the Forge
// (static_yaml), the same absence catalogdata.Plugins's own doc comment
// records.
var handWrittenPlugins = []struct {
	Name        string
	Description string
	ReadOnly    bool
}{
	{Name: "static_yaml", Description: "reads devices from the project's own inventory.yaml", ReadOnly: false},
}

// generatePlugins emits outDir/plugins.md: every registered inventory
// sync plugin.
func generatePlugins(outDir string) error {
	var b strings.Builder
	b.WriteString(frontMatter("beta"))
	b.WriteString("# Sync plugins\n\n")
	b.WriteString("Every registered inventory sync plugin. Run `pleiades inventory plugins` for the " +
		"same list from a live binary.\n\n")

	rows := make([][]string, 0, len(catalogdata.Plugins)+len(handWrittenPlugins))
	for _, p := range handWrittenPlugins {
		rows = append(rows, []string{code(p.Name), p.Description, yesNo(p.ReadOnly), "hand-written, predates the Forge"})
	}
	for _, p := range catalogdata.Plugins {
		rows = append(rows, []string{code(p.Name), p.Description, yesNo(p.ReadOnly), "generated"})
	}

	b.WriteString(table([]string{"Name", "Description", "Read-only", "Origin"}, rows))
	b.WriteString(fmt.Sprintf("\n%d sync plugins registered.\n", len(rows)))

	return os.WriteFile(filepath.Join(outDir, "plugins.md"), []byte(b.String()), 0o644) // #nosec G306 -- generated docs, not secret material
}
