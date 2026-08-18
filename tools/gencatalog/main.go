// Command gencatalog builds the real pleiades binary and drives it through
// `forge new-collection`/`forge new-device` once per
// internal/forge/catalogdata entry, so the module catalog
// docs/hephaestus.md describes is produced by the actual CLI a user runs,
// never by calling collectionscaffold.Generate/devicescaffold.Generate
// directly. Phase 34's own Pattern Entry Gate requires this: it is the
// dogfood test of Phase 33's scaffolds, and calling the library underneath
// the CLI would test the library, not the command surface.
//
// Usage: go run ./tools/gencatalog
package main

import (
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/pluginscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devicescaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// modulePath is this repository's own module path, needed to build the
// full import path of each generated catalog package for
// writeCatalogBuiltins.
const modulePath = "github.com/Subject-Void-LLC/the-pleiades"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gencatalog:", err)
		os.Exit(1)
	}
}

func run() error {
	if err := validateCatalogEntries(catalogdata.Collections, catalogdata.Devices, catalogdata.Plugins); err != nil {
		return err
	}

	root, err := findModuleRoot()
	if err != nil {
		return err
	}

	binPath, cleanup, err := buildPleiadesBinary(root)
	if err != nil {
		return err
	}
	defer cleanup()

	written, err := generateEntries(binPath, root, catalogdata.Collections, catalogdata.Devices, catalogdata.Plugins)
	if err != nil {
		return err
	}

	if err := writeCatalogBuiltins(root, catalogdata.Collections); err != nil {
		return err
	}

	// internal/inventory/plugins/builtins.go is deliberately not regenerated
	// here, unlike internal/catalog/builtins.go above. It lists plugins this
	// table does not own: static_yaml predates the Forge and is
	// hand-written, so a fully regenerated aggregator computed from
	// catalogdata.Plugins alone would silently drop its blank import and
	// unregister a working plugin. That is the same reason
	// internal/inventory/builtins.go stays hand-maintained for device types.
	// internal/archtest is what catches a missing entry.
	//
	// The count reported is files newly written, not entries visited,
	// and on a repository whose catalog is already generated it is
	// legitimately zero. That is the difference this run has to make
	// visible: every subcommand is invoked with --skip-existing, so a
	// second run over an unchanged table is a no-op by design rather
	// than by accident, and printing only the entry counts would make a
	// run that wrote three new files look exactly like one that wrote
	// none.
	fmt.Printf("gencatalog: visited %d collection(s), %d device type(s), %d sync plugin(s); wrote %d new file(s), left the rest alone, and regenerated the catalog builtins aggregator\n",
		len(catalogdata.Collections), len(catalogdata.Devices), len(catalogdata.Plugins), written)
	return nil
}

// generateEntries drives binPath through `forge new-collection`,
// `new-device` and `new-plugin` once per entry, against root, and returns
// how many files those invocations actually wrote.
//
// The three tables are parameters rather than reads of catalogdata, the
// same shape validateCatalogEntries already uses and for the same reason:
// it is the only way to exercise this against a small synthetic set. The
// alternative is a test that regenerates the real seventy-seven-entry
// catalog, which is slow enough that nobody runs it and destructive
// enough that nobody should.
//
// A failure stops the run rather than being collected. Every invocation
// after the first is generating into the same tree, so continuing past a
// failure means writing more files while already in an unknown state.
func generateEntries(
	binPath, root string,
	collections []collectionscaffold.Config,
	devices []devicescaffold.Config,
	plugins []pluginscaffold.Config,
) (int, error) {
	var written int

	for _, cfg := range collections {
		args, err := newCollectionArgs(cfg)
		if err != nil {
			return written, err
		}
		n, err := runPleiades(binPath, root, args...)
		if err != nil {
			return written, err
		}
		written += n
	}
	for _, cfg := range devices {
		n, err := runPleiades(binPath, root, newDeviceArgs(cfg)...)
		if err != nil {
			return written, err
		}
		written += n
	}
	for _, cfg := range plugins {
		n, err := runPleiades(binPath, root, newPluginArgs(cfg)...)
		if err != nil {
			return written, err
		}
		written += n
	}
	return written, nil
}

// newPluginArgs builds the exact `forge new-plugin` argument list for cfg,
// mirroring cmd/pleiades/forge_new_plugin.go's own flag surface.
func newPluginArgs(cfg pluginscaffold.Config) []string {
	args := []string{"forge", "new-plugin", cfg.Name, "--description", cfg.Description}
	if cfg.Endpoint != "" {
		args = append(args, "--endpoint", cfg.Endpoint)
	}
	if cfg.ReadOnly {
		args = append(args, "--read-only")
	}
	return append(args, "--skip-existing")
}

// writeCatalogBuiltins writes internal/catalog/builtins.go: a blank
// import of every distinct generated Collection package, computed from
// collections' own PackagePath(), deduplicated and sorted for a stable
// diff. This is internal/inventory/builtins.go's identical pattern
// applied to Collections instead of device types: a generated package's
// own init() only runs once something imports it, and nothing else in
// this codebase has a reason to import a generated catalog package for
// its own sake. Unlike the collection/device source files themselves
// (each written once, by the real CLI, and never touched again), this
// aggregator is fully regenerated every run: it always reflects exactly
// the current collections set, so an entry added or removed there can
// never leave a stale or missing import behind. collections is passed in
// explicitly, rather than read from catalogdata directly, so this
// function is testable against a small synthetic set without touching
// the real catalog.
func writeCatalogBuiltins(root string, collections []collectionscaffold.Config) error {
	seen := make(map[string]bool)
	var paths []string
	for _, cfg := range collections {
		p := cfg.PackagePath()
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	var b strings.Builder
	b.WriteString("// Package catalog exists purely to trigger every generated Collection\n")
	b.WriteString("// package's own init() registration into the shared pkg/collection\n")
	b.WriteString("// registry: see internal/forge/catalogdata for the source of truth these\n")
	b.WriteString("// packages were generated from, tools/gencatalog for what writes this\n")
	b.WriteString("// file, and internal/inventory/builtins.go for the identical pattern\n")
	b.WriteString("// applied to device types. Regenerated by tools/gencatalog every run:\n")
	b.WriteString("// never hand-edited, never hand-added-to.\n")
	b.WriteString("package catalog\n\n")
	b.WriteString("import (\n")
	for _, p := range paths {
		fmt.Fprintf(&b, "\t_ %q\n", modulePath+"/internal/catalog/"+p)
	}
	b.WriteString(")\n")

	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return fmt.Errorf("formatting internal/catalog/builtins.go: %w", err)
	}

	dest := filepath.Join(root, "internal", "catalog", "builtins.go")
	if err := os.WriteFile(dest, formatted, 0o644); err != nil { // #nosec G306 -- generated source, not secret material
		return fmt.Errorf("writing %s: %w", dest, err)
	}
	return nil
}

// validateCatalogEntries runs every entry's own Validate() before touching
// the filesystem or the CLI at all, so a data-table typo fails fast with
// every bad entry named at once, rather than as a single opaque CLI
// failure partway through a run that already wrote real files for every
// entry before it. collections and devices are passed in
// explicitly, rather than read from catalogdata directly, so this
// function is testable against a small synthetic set.
func validateCatalogEntries(
	collections []collectionscaffold.Config,
	devices []devicescaffold.Config,
	plugins []pluginscaffold.Config,
) error {
	var errs []string
	for _, c := range collections {
		if err := c.Validate(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	for _, d := range devices {
		if err := d.Validate(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	for _, p := range plugins {
		if err := p.Validate(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("catalogdata has %d invalid entr(ies):\n  %s", len(errs), strings.Join(errs, "\n  "))
	}
	return nil
}

// findModuleRoot walks upward from the working directory until it finds
// go.mod, so this tool behaves the same whether it is run directly (`go
// run ./tools/gencatalog` from the repository root) or via `go generate
// ./internal/forge/catalogdata` (whose go:generate directive runs with the
// declaring file's own directory as the working directory, not the
// module's).
func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found in any parent of %s", dir)
		}
		dir = parent
	}
}

// buildPleiadesBinary builds the real cmd/pleiades binary once, mirroring
// cmd/pleiades/e2e_test.go's TestMain: every catalog/device entry then
// drives that one binary as a subprocess, the same way a user would, per
// RULE 0 (AGENTS.md) -- calling collectionscaffold/devicescaffold's
// Generate function in-process would test the library, not the CLI.
func buildPleiadesBinary(root string) (binPath string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp("", "pleiades-gencatalog")
	if err != nil {
		return "", nil, fmt.Errorf("creating temp dir for pleiades binary: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmpDir) }

	binPath = filepath.Join(tmpDir, "pleiades")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/pleiades") // #nosec G204 -- "go" is a fixed literal; binPath is this tool's own os.MkdirTemp output, not external input; "./cmd/pleiades" is a fixed literal
	build.Dir = root
	if out, buildErr := build.CombinedOutput(); buildErr != nil {
		cleanup()
		return "", nil, fmt.Errorf("building pleiades binary: %w\n%s", buildErr, out)
	}
	return binPath, cleanup, nil
}

// runPleiades runs the real built binary with args against root, exactly
// as a user invoking `pleiades <args>` from the repository root would,
// and reports how many files that invocation actually wrote.
//
// The count comes from parsing the CLI's own output rather than from
// stat-ing the filesystem here, because the CLI is the thing that
// decided: this tool passes --skip-existing to every subcommand and has
// no independent opinion about which paths a given entry produces.
func runPleiades(binPath, root string, args ...string) (written int, err error) {
	cmd := exec.Command(binPath, args...) // #nosec G204 -- binPath is our own freshly built binary; args are this tool's own fixed catalogdata-derived arguments, not external input
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("pleiades %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "wrote ") {
			written++
		}
	}
	return written, nil
}

// newCollectionArgs builds the exact `forge new-collection` argument list
// for cfg, mirroring cmd/pleiades/forge_new_collection.go's own flag
// surface.
//
// The Doc travels as JSON on --doc-json rather than as a flag per field:
// see pkg/collection.Doc for what it holds, and
// collectionscaffold.Config.Doc for why no flat flag surface can carry
// it. Passing it inline rather than through a temporary file keeps this
// tool's "drive the real CLI, hold no state of its own" shape; the
// largest Doc in the catalog is a few kilobytes against a Linux argument
// limit measured in megabytes.
func newCollectionArgs(cfg collectionscaffold.Config) ([]string, error) {
	args := []string{"forge", "new-collection", cfg.Name}
	if len(cfg.Capabilities) > 0 {
		args = append(args, "--capabilities", joinCapabilities(cfg.Capabilities))
	}
	if len(cfg.Transports) > 0 {
		args = append(args, "--transports", strings.Join(cfg.Transports, ","))
	}
	if cfg.RequiresElevation {
		args = append(args, "--requires-elevation")
	}
	if cfg.EngineVersion != "" {
		args = append(args, "--engine-version", cfg.EngineVersion)
	}
	// Every field of Doc is omitempty, so an entry that documents
	// nothing encodes to exactly "{}" and the flag is left off. Doc
	// holds slices and so cannot be compared to its zero value
	// directly.
	encoded, err := json.Marshal(cfg.Doc)
	if err != nil {
		return nil, fmt.Errorf("encoding %s's Doc as JSON: %w", cfg.Name, err)
	}
	if string(encoded) != "{}" {
		args = append(args, "--doc-json", string(encoded))
	}
	return append(args, "--skip-existing"), nil
}

// newDeviceArgs builds the exact `forge new-device` argument list for cfg,
// mirroring cmd/pleiades/forge_new_device.go's own flag surface.
func newDeviceArgs(cfg devicescaffold.Config) []string {
	args := []string{"forge", "new-device", cfg.Vendor, "--type", cfg.TypeKey}
	if len(cfg.Capabilities) > 0 {
		args = append(args, "--capabilities", joinCapabilities(cfg.Capabilities))
	}
	return append(args, "--skip-existing")
}

func joinCapabilities(names []capability.Name) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = string(n)
	}
	return strings.Join(parts, ",")
}
