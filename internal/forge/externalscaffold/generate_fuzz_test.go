// Package externalscaffold_test: a fuzz of Generate over method names.
package externalscaffold_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/externalscaffold"
)

// FuzzGenerate proves that Generate either rejects a name outright or,
// for anything it accepts, produces four files whose paths are single,
// distinct, relative file names that cannot leave the program directory,
// whose Go sources parse, and among which exactly one is a test. The
// same holds for the default program directory the CLI derives from the
// name, since that is joined into a real path too.
//
// It mirrors collectionscaffold's own FuzzGenerate, with one difference
// that matters: here every path lands directly in a directory the user
// named, with no fixed prefix like internal/catalog/ to anchor it, so a
// separator of any kind in a path would be an escape rather than a
// subdirectory.
func FuzzGenerate(f *testing.F) {
	seeds := []string{
		"acme.motd.read", "acme.probe", "read", "", ".", "..", "...",
		"acme..read", "../../etc/passwd", "acme.motd.read..", "acme.",
		"acme.type.read", "acme.branch.main", "acme.motd.read_test",
		"acme.motd.read_linux", "acme.svc.daemon_reload", "Acme.Motd.Read",
		"acme motd.read", "acme/motd.read", "acme.motd/read", "acme\\motd.read",
		"acme.3com.read", "a.b.c.d.e.f.g", "acme.motd.a_", "acme.motd.a__b",
		"acme.motd.read\x00", "acme.motd.re\nad",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, name string) {
		cfg := externalscaffold.Config{Name: name}
		files, err := externalscaffold.Generate(cfg)
		if err != nil {
			return
		}

		if len(files) != 5 {
			t.Fatalf("Generate(%q) returned %d files, want 5", name, len(files))
		}

		seen := make(map[string]bool, len(files))
		tests := 0
		for _, file := range files {
			if seen[file.Path] {
				t.Fatalf("Generate(%q) returned %q twice", name, file.Path)
			}
			seen[file.Path] = true

			if file.Path == "" || filepath.Base(file.Path) != file.Path || strings.ContainsAny(file.Path, `/\`) || strings.Contains(file.Path, "..") {
				t.Fatalf("Generate(%q) produced a path that is not a plain file name: %q", name, file.Path)
			}
			if strings.HasSuffix(file.Path, "_test.go") {
				tests++
			}
			if !strings.HasSuffix(file.Path, ".go") {
				continue
			}
			if _, parseErr := parser.ParseFile(token.NewFileSet(), file.Path, file.Content, parser.AllErrors); parseErr != nil {
				t.Fatalf("Generate(%q) produced unparseable Go for %s: %v\n---\n%s", name, file.Path, parseErr, file.Content)
			}
		}
		if tests != 1 {
			t.Fatalf("Generate(%q) produced %d test files, want exactly 1", name, tests)
		}

		dir := cfg.DefaultDir()
		if dir == "" || filepath.Base(dir) != dir || strings.ContainsAny(dir, `/\`) || strings.Contains(dir, "..") {
			t.Fatalf("DefaultDir() for %q is not a plain directory name: %q", name, dir)
		}
	})
}
