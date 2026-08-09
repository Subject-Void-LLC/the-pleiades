package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	// Triggers every built-in sync plugin package's own init()
	// registration into internal/inventory/syncplugin, the same blank
	// import cmd/pleiades/inventory.go carries for the same reason. Without
	// it the registry is empty and this page would render a product with no
	// sync plugins at all (FAILURE_PATTERNS.md #52).
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
)

// forgeGenerated reports whether the Forge created the plugin registered
// under name. catalogdata.Plugins is the table `pleiades forge new-plugin`
// appends to, so a registered name absent from it is a plugin that was
// written by hand before the Forge existed (static_yaml, per
// catalogdata.Plugins's own doc comment).
func forgeGenerated(name string) bool {
	for _, cfg := range catalogdata.Plugins {
		if cfg.Name == name {
			return true
		}
	}
	return false
}

// generatePlugins emits outDir/plugins.md: every registered inventory
// sync plugin, read from the live syncplugin registry rather than from a
// list typed into this generator. The registry is the same one
// `pleiades inventory plugins` prints and `pleiades inventory sync`
// resolves --plugin against, so a row on this page and a row in a real
// terminal cannot describe two different plugins. catalogdata.Plugins is
// consulted only for the Origin column, which asks a question the
// registry cannot answer: whether the plugin's source was generated or
// hand-written.
func generatePlugins(outDir string) error {
	page, err := renderPlugins(syncplugin.Names())
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "plugins.md"), []byte(page), 0o644) // #nosec G306 -- generated docs, not secret material
}

// renderPlugins builds plugins.md's body from the plugin names given,
// resolving each one against the live registry. It takes the name list as a
// parameter rather than reading it itself so a test can drive both refusals
// below: an empty registry, and a name the registry does not hold.
func renderPlugins(names []string) (string, error) {
	if len(names) == 0 {
		// An empty registry is not a product with no sync plugins. It means
		// the blank import above was dropped, so failing loudly here is the
		// entire point of checking.
		return "", fmt.Errorf("no sync plugins are registered, which means internal/inventory/plugins was not linked in")
	}

	var b strings.Builder
	b.WriteString(frontMatter("beta"))
	b.WriteString("# Sync plugins\n\n")
	b.WriteString("Every registered inventory sync plugin, read from the live registry `pleiades " +
		"inventory sync` resolves `--plugin` against. Run `pleiades inventory plugins` for the same " +
		"list from a live binary. A `declared` plugin is registered but its methods return a " +
		"not-implemented error; only an `implemented` plugin has run against its real upstream " +
		"system.\n\n")

	rows := make([][]string, 0, len(names))
	for _, name := range names {
		desc, ok := syncplugin.Lookup(name)
		if !ok {
			// name came out of the same registry one line ago, so a miss here
			// means the registry changed underneath this loop.
			return "", fmt.Errorf("plugin %q is listed by syncplugin.Names but absent from the registry", name)
		}

		status := string(syncplugin.StatusDeclared)
		if desc.Implemented() {
			status = string(syncplugin.StatusImplemented)
		}
		origin := "hand-written, predates the Forge"
		if forgeGenerated(name) {
			origin = "generated"
		}

		rows = append(rows, []string{code(name), desc.Description, yesNo(desc.DefaultConfig.ReadOnly), code(status), origin})
	}

	b.WriteString(table([]string{"Name", "Description", "Read-only", "Status", "Origin"}, rows))
	b.WriteString(fmt.Sprintf("\n%d sync plugins registered.\n", len(rows)))

	return b.String(), nil
}
