// Command gendocs regenerates docs/reference/ from the same registries
// the engine, `pleiades validate`, and the real dispatcher read: the live
// pkg/collection registry (populated by the blank import below), the
// pkg/capability vocabulary, internal/forge/catalogdata's device and
// plugin tables, and engine.ReservedTaskKeys. A generated page is
// committed like any other generated source: never hand-edited. If a
// page reads wrong, the fix belongs in the data (catalogdata, a
// Manifest.Doc block) or in this tool's own rendering, never in the
// generated Markdown itself.
//
// Usage, after editing internal/forge/catalogdata or a Manifest.Doc
// block:
//
//	go generate ./tools/gendocs
//
// or directly:
//
//	go run ./tools/gendocs
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
	_ "github.com/SubjectVoidLLC/the-pleiades/internal/catalog"
)

//go:generate go run .

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
		{"task keys", generateTaskKeys},
		{"implementation status", generateStatus},
		{"cli reference", generateCLI},
		{"runbook schema", generateRunbookSchema},
		{"inventory schema", generateInventorySchema},
		{"module catalog schema", generateModuleCatalogSchema},
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

Generated from the same registries the engine, ` + "`pleiades validate`" + `, and the real dispatcher
read. Never hand-edited; regenerate with ` + "`go generate ./tools/gendocs`" + ` after changing
` + "`internal/forge/catalogdata`" + ` or a Collection method's ` + "`Manifest.Doc`" + ` block.

- [Module catalog](modules/index.md)
- [Capability vocabulary](capabilities.md)
- [Device types](devices.md)
- [Sync plugins](plugins.md)
- [Runbook and task keys](task-keys.md)
- [Implementation status](implementation-status.md)
- [CLI reference](cli.md)
- [Runbook JSON Schema](schemas/runbook.schema.json)
- [Inventory JSON Schema](schemas/inventory.schema.json)
- [Module catalog](schemas/module-catalog.json) (data, not a schema: every FQCN's full Manifest)
- [OpenAPI document](schemas/openapi.json) (also served live at ` + "`/api/v1/openapi.json`" + `)
`
	return os.WriteFile(filepath.Join(outputDir, "index.md"), []byte(content), 0o644) // #nosec G306 -- generated docs, not secret material
}
