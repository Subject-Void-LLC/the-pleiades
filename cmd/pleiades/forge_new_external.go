// Command pleiades's `forge new-external` subcommand lives here: thin
// flag parsing that delegates all generation to
// internal/forge/externalscaffold, per the forge convention that a
// subcommand file owns nothing beyond its own flags.
package main

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/externalscaffold"
)

// runForgeNewExternal scaffolds a complete, buildable external Collection
// program providing one method: a main.go that calls external.Main, the
// method with a working read-only body, its test, and a README saying how
// to build and install it.
//
// It writes into a fresh directory named after the method unless --dir
// names another. Unlike new-collection, nothing is wired into this
// repository afterward: the program is built on its own and installed by
// dropping its binary into PLEIADES_COLLECTIONS_DIR.
//
// Every file is checked for a collision before any is written, so a
// refusal never leaves half a program behind, and an existing go.mod is
// never replaced. A program is five files that only make sense together, the same reason firstExistingFile decides for
// a whole catalog entry rather than per file.
func runForgeNewExternal(args []string) error {
	name, rest, err := splitPositional(args, map[string]bool{"no-go-mod": true})
	if err != nil {
		return fmt.Errorf("usage: pleiades forge new-external <namespace.method> [--dir DIR] [--no-go-mod]: %w", err)
	}

	fs := flag.NewFlagSet("forge new-external", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory to write the program into (default: the method name with its dots as hyphens)")
	noGoMod := fs.Bool("no-go-mod", false, "write no go.mod, so the program joins the Go module around it instead of being a module of its own")
	if err := fs.Parse(rest); err != nil {
		return err
	}

	cfg := externalscaffold.Config{Name: name, Engine: version, NoGoMod: *noGoMod}
	files, err := externalscaffold.Generate(cfg)
	if err != nil {
		return err
	}

	target := *dir
	if target == "" {
		target = cfg.DefaultDir()
	}

	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	existing, err := firstExistingFile(target, paths)
	if err != nil {
		return err
	}
	if existing != "" {
		return fmt.Errorf("refusing to overwrite existing file: %s (nothing was written)", existing)
	}

	for _, f := range files {
		written, err := writeGeneratedFile(target, f.Path, f.Content)
		if err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", written)
	}

	fmt.Printf("\nNext: read %s for how to build the program and install it into PLEIADES_COLLECTIONS_DIR.\n",
		filepath.Join(target, "README.md"))
	return nil
}
