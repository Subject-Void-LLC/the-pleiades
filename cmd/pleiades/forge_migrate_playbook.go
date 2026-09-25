// Command pleiades's `forge migrate-playbook` subcommand lives here: flag
// parsing and file writing around internal/forge/playbook, which does the
// conversion and knows nothing of the command line, so an editor's
// language server can call it the same way.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// runForgeMigratePlaybook converts an Ansible playbook into native
// runbooks, writes them into --out, and prints the migration report: text
// by default, the same model as JSON with --json. It exits 3 when the
// report asks a person to act, whether to read a review or to resolve a
// blocked task, so a script cannot mistake an incomplete conversion for a
// finished one.
func runForgeMigratePlaybook(args []string) error {
	usage := "usage: pleiades forge migrate-playbook <playbook.yml> [--out runbooks] [--json] [--force]"

	path, rest, err := splitPositional(args, map[string]bool{"json": true, "force": true})
	if err != nil {
		return fmt.Errorf("%s: %w", usage, err)
	}
	flags := flag.NewFlagSet("forge migrate-playbook", flag.ContinueOnError)
	out := flags.String("out", "runbooks", "directory to write the converted runbooks into, created when missing")
	asJSON := flags.Bool("json", false, "print the migration report as JSON, the same model the text view renders")
	force := flags.Bool("force", false, "replace runbooks an earlier conversion wrote")
	if err := flags.Parse(rest); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("%s: unexpected argument %s", usage, termsafe.EscapeLine(flags.Arg(0)))
	}

	src, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("opening the playbook's directory: %w", err)
	}
	defer src.Close()
	result, err := playbook.Translate(src.FS(), filepath.Base(path), playbook.Options{})
	if err != nil {
		return err
	}

	dst, err := openOutDir(*out)
	if err != nil {
		return err
	}
	defer dst.Close()
	if err := writeRunbooks(dst, *out, result.Runbooks, *force); err != nil {
		return err
	}

	if *asJSON {
		data, err := json.MarshalIndent(result.Report, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	} else {
		fmt.Print(result.Report.String())
	}
	if result.Report.NeedsHuman() {
		return &incompleteError{msg: "the conversion needs a person: resolve every blocked finding, and read every review, before running the runbooks"}
	}
	return nil
}

// openOutDir opens dir for the converted runbooks, creating it when
// missing. A symbolic link is refused, and the directory opened must be
// the one looked at, so the output cannot be sent somewhere the person did
// not name.
func openOutDir(dir string) (*os.Root, error) {
	shown := termsafe.EscapeLine(dir)
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- a directory of runbooks, not secret material
			return nil, err
		}
		if info, err = os.Lstat(dir); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("--out %s is a symbolic link; name the directory itself", shown)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("--out %s is not a directory", shown)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	if opened, err := root.Stat("."); err != nil || !os.SameFile(info, opened) {
		_ = root.Close()
		return nil, fmt.Errorf("--out %s changed while it was being opened", shown)
	}
	return root, nil
}

// writeRunbooks writes each runbook into dst. Without force, a file
// already there refuses the whole write before anything is written; with
// it, the file is replaced. A symbolic link where a runbook goes is
// refused either way. A runbook an earlier conversion left under the other
// name (x.yaml beside a new x.incomplete.yaml) is named on stderr, since
// running the stale one is the mistake the new name exists to prevent.
func writeRunbooks(dst *os.Root, outName string, runbooks []playbook.Runbook, force bool) error {
	for _, rb := range runbooks {
		info, err := dst.Lstat(rb.File)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return err
		case info.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symbolic link; remove it first", termsafe.EscapeLine(filepath.Join(outName, rb.File)))
		case !force:
			return fmt.Errorf("%s already exists; rerun with --force to replace it", termsafe.EscapeLine(filepath.Join(outName, rb.File)))
		}
	}
	mode := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		mode = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	for _, rb := range runbooks {
		f, err := dst.OpenFile(rb.File, mode, 0o644) // #nosec G302 -- a runbook, which holds no secret: the converter never copies one into it
		if err != nil {
			return err
		}
		if _, err := f.Write(rb.YAML); err != nil {
			_ = f.Close() // the write's error is the one to report
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if stale := otherName(rb.File); stale != "" {
			if _, err := dst.Lstat(stale); err == nil {
				fmt.Fprintf(os.Stderr, "note: %s is left from an earlier conversion of this playbook; remove it\n", termsafe.EscapeLine(filepath.Join(outName, stale)))
			}
		}
	}
	return nil
}

// otherName is the name a runbook has in the other state: x.yaml and
// x.incomplete.yaml name the same conversion, complete or not.
func otherName(file string) string {
	if stem, ok := strings.CutSuffix(file, ".incomplete.yaml"); ok {
		return stem + ".yaml"
	}
	if stem, ok := strings.CutSuffix(file, ".yaml"); ok {
		return stem + ".incomplete.yaml"
	}
	return ""
}
