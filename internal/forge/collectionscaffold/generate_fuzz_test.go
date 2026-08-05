package collectionscaffold_test

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

// FuzzGenerate proves that Generate either rejects a malformed Name
// outright, or, for anything it accepts, produces two files whose paths
// never escape internal/catalog/ and whose content always parses as valid
// Go. This is Phase 33's own Fuzz/Stress checklist item: "fuzz names and
// capability lists for path traversal and invalid Go identifiers."
func FuzzGenerate(f *testing.F) {
	seeds := []string{
		"pkg.apt.install", "exec.command", "install", "",
		"pkg..install", "../../etc/passwd", "pkg.apt.install..",
		"pkg.type.install", "pkg.func.install", "3com.install",
		"pkg.apt.0day", "Pkg.Apt.Install", "pkg apt.install",
		"pkg/apt.install", "pkg.apt/install", "a.b.c.d.e.f.g",
		".", "..", "...", "pkg.",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, name string) {
		files, err := collectionscaffold.Generate(collectionscaffold.Config{
			Name:         name,
			Capabilities: []capability.Name{capability.NameSSHTransport},
		})
		if err != nil {
			return
		}

		if len(files) != 2 {
			t.Fatalf("Generate(%q) returned %d files, want 2", name, len(files))
		}

		for _, file := range files {
			if !strings.HasPrefix(file.Path, "internal/catalog/") {
				t.Fatalf("Generate(%q) produced an escaping path: %q", name, file.Path)
			}
			if strings.Contains(file.Path, "..") {
				t.Fatalf("Generate(%q) produced a path traversal segment: %q", name, file.Path)
			}

			fset := token.NewFileSet()
			if _, parseErr := parser.ParseFile(fset, file.Path, file.Content, parser.AllErrors); parseErr != nil {
				t.Fatalf("Generate(%q) produced unparseable Go source for %s: %v\n---\n%s",
					name, file.Path, parseErr, file.Content)
			}
		}
	})
}
