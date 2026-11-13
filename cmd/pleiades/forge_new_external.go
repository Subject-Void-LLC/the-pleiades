// Command pleiades's `forge new-external` subcommand lives here: thin
// flag parsing that delegates all generation to
// internal/forge/externalscaffold, per the forge convention that a
// subcommand file owns nothing beyond its own flags.
package main

import (
	"flag"
	"fmt"
	"os"
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
// With --script, the program wraps that script instead: it embeds the file
// (read here, since Generate does no I/O) and runs it with --interpreter,
// which must be an absolute path.
//
// Every file is checked for a collision before any is written, so a
// refusal never leaves half a program behind, and an existing go.mod is
// never replaced. A program is five to six files that only make sense
// together, the same reason firstExistingFile decides for a whole catalog
// entry rather than per file.
func runForgeNewExternal(args []string) error {
	fs := flag.NewFlagSet("forge new-external", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory to write the program into (default: the method name with its dots as hyphens)")
	noGoMod := fs.Bool("no-go-mod", false, "write no go.mod, so the program joins the Go module around it instead of being a module of its own")
	capabilityName := fs.String("capability", "", "a capability the program defines as data, <namespace>.<name> in the method's namespace: its definition and a probe that grants it are written, and the method requires it")
	scriptPath := fs.String("script", "", "a script for the method to run, embedded in the program so its approved digest covers it; the method acts on no device and is handed no credential")
	interpreter := fs.String("interpreter", "", "the absolute path of the program that runs --script, for example /usr/bin/python3; it is outside the approved digest")
	name, err := parseOneArg(fs, args)
	if err != nil {
		return fmt.Errorf("usage: pleiades forge new-external <namespace.method> [--dir DIR] [--no-go-mod] [--capability <namespace>.<name>] [--script FILE --interpreter PATH]: %w", err)
	}

	cfg := externalscaffold.Config{Name: name, Engine: version, NoGoMod: *noGoMod, Capability: *capabilityName, Interpreter: *interpreter}
	if *scriptPath != "" {
		if cfg.Script, err = readScript(*scriptPath); err != nil {
			return err
		}
		cfg.ScriptName = filepath.Base(*scriptPath)
	}
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

// readScript reads the script --script names, refusing anything but a
// regular file, and a file larger than externalscaffold.MaxScriptSize
// before reading it.
func readScript(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("--script: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("--script: %s is not a regular file", path)
	}
	if info.Size() > externalscaffold.MaxScriptSize {
		return nil, fmt.Errorf("--script: %s is %d bytes, more than the %d a program embeds", path, info.Size(), externalscaffold.MaxScriptSize)
	}
	script, err := os.ReadFile(path) // #nosec G304 -- the file the person running forge named
	if err != nil {
		return nil, fmt.Errorf("--script: %w", err)
	}
	return script, nil
}
