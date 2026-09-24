// Command gendocs regenerates docs/reference/ from the registries the
// engine, `pleiades validate`, and the real dispatcher read: the live
// pkg/collection registry (populated by the blank import below), the
// pkg/capability vocabulary, the live inventory syncplugin registry
// (plugins.go), internal/forge/catalogdata's device table, and
// engine.ReservedTaskKeys. A generated page is committed like any other
// generated source: never hand-edited. If a page reads wrong, the fix
// belongs in the data (catalogdata, a Manifest.Doc block) or in this
// tool's own rendering, never in the generated Markdown itself.
//
// The one table not read from a registry is handWrittenDevices in
// devices.go, guarded by completeness_test.go; see that variable's own doc
// comment for why it exists and what fails if it drifts.
//
// Usage, after editing internal/forge/catalogdata or a Manifest.Doc
// block, from the repository root:
//
//	go run ./tools/gendocs
//
// Run it from the repository root and nowhere else. outputDir below and
// writeSchema's wellKnownDir are both repo-root-relative, so this tool
// writes its pages under whatever directory it is started in. That is
// also why this file carries no go:generate directive, deliberately: go
// generate runs a directive in its own package's directory, so
// `go generate ./tools/gendocs` wrote a full copy of docs/reference and
// internal/api/wellknown under tools/gendocs/ instead, where the
// docs-gen-check target's own `git diff` never looked at them.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	// Triggers every generated Collection method's own init()
	// registration into pkg/collection, the same blank import
	// cmd/pleiades/catalog_builtins.go carries for the same reason:
	// without it, pkg/collection.Lookup finds nothing, and this tool
	// would have no Manifest to read for any FQCN in catalogdata.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog"
)

// outputDir is docs/reference/, relative to the repository root this
// tool is expected to run from (matching tools/gencatalog's own
// convention of a fixed, repo-root-relative destination).
const outputDir = "docs/reference"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
}

func run() error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil { // #nosec G301 -- generated docs, not secret material
		return fmt.Errorf("creating %s: %w", outputDir, err)
	}

	steps := []struct {
		name string
		fn   func(string) error
	}{
		{"modules", generateModules},
		{"capabilities", generateCapabilities},
		{"devices", generateDevices},
		{"plugins", generatePlugins},
		{"filters", generateFilters},
		{"task keys", generateTaskKeys},
		{"implementation status", generateStatus},
		{"cli reference", generateCLI},
		{"runbook schema", generateRunbookSchema},
		{"inventory schema", generateInventorySchema},
		{"module catalog schema", generateModuleCatalogSchema},
		{"ansible modules", generateAnsibleModules},
		{"migration report schema", generateMigrationReportSchema},
		{"openapi", generateOpenAPI},
	}

	for _, step := range steps {
		if err := step.fn(outputDir); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}

	if err := writeReferenceIndex(); err != nil {
		return err
	}

	fmt.Printf("gendocs: wrote %s\n", outputDir)
	return nil
}

// writeReferenceIndex emits docs/reference/index.md, the landing page
// linking every generated section this tool produces.
func writeReferenceIndex() error {
	content := frontMatter("beta") + `# Reference

Generated from the registries the engine, ` + "`pleiades validate`" + `, and the real dispatcher
read. Never hand-edited; regenerate by running ` + "`go run ./tools/gendocs`" + ` from the repository
root after changing ` + "`internal/forge/catalogdata`" + ` or a Collection method's ` + "`Manifest.Doc`" + `
block.

**One exception, stated plainly.** The ` + "`cisco_router`" + ` and ` + "`linux_server`" + ` rows on the
[device types](devices.md) page are typed into the generator, not read from the device
registry, because both types predate the Forge. A test in the generator fails if either
row stops matching what a real binary hydrates, so those two rows cannot drift in
silence. They are still the one place on these pages where a human wrote the data.

- [Module catalog](modules/index.md)
- [Capability vocabulary](capabilities.md)
- [Device types](devices.md)
- [Sync plugins](plugins.md)
- [Filter reference](filters/index.md)
- [Runbook and task keys](task-keys.md)
- [Implementation status](implementation-status.md)
- [CLI reference](cli.md)
- [Runbook JSON Schema](schemas/runbook.schema.json)
- [Inventory JSON Schema](schemas/inventory.schema.json)
- [Module catalog](schemas/module-catalog.json) (data, not a schema: every FQCN's full Manifest)
- [Ansible module conversions](ansible-modules.md) (what ` + "`pleiades forge migrate-playbook`" + ` does with each module, and every finding code)
- [Migration report JSON Schema](schemas/migration-report.json)
- [OpenAPI document](schemas/openapi.json) (also served live at ` + "`/api/v1/openapi.json`" + `)
`
	return os.WriteFile(filepath.Join(outputDir, "index.md"), []byte(content), 0o644) // #nosec G306 -- generated docs, not secret material
}
