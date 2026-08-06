package devicescaffold_test

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/devicescaffold"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

// FuzzGenerate proves that Generate either rejects a malformed Vendor or
// TypeKey outright, or, for anything it accepts, produces two files whose
// paths never escape internal/inventory/devices/ and whose content always
// parses as valid Go. This is Phase 33's own Fuzz/Stress checklist item:
// "fuzz names and capability lists for path traversal and invalid Go
// identifiers. A vendor name containing .. or a Go keyword must not
// produce an unsafe path or unbuildable source."
func FuzzGenerate(f *testing.F) {
	seeds := []string{
		"juniper", "..", "../../etc/passwd", "..\\windows", "3com", "0day",
		"Cisco", "foo/bar", "foo.bar", "foo bar", "func", "type", "package",
		"", "a", "acme_box", "junos_router", "acme_", "_acme",
	}
	for _, vendor := range seeds {
		for _, typeKey := range seeds {
			f.Add(vendor, typeKey)
		}
	}

	f.Fuzz(func(t *testing.T, vendor, typeKey string) {
		files, err := devicescaffold.Generate(devicescaffold.Config{
			Vendor:       vendor,
			TypeKey:      typeKey,
			Capabilities: []capability.Name{capability.NameSSHTransport},
		})
		if err != nil {
			return
		}

		if len(files) != 2 {
			t.Fatalf("Generate(%q, %q) returned %d files, want 2", vendor, typeKey, len(files))
		}

		for _, file := range files {
			if !strings.HasPrefix(file.Path, "internal/inventory/devices/") {
				t.Fatalf("Generate(%q, %q) produced an escaping path: %q", vendor, typeKey, file.Path)
			}
			if strings.Contains(file.Path, "..") {
				t.Fatalf("Generate(%q, %q) produced a path traversal segment: %q", vendor, typeKey, file.Path)
			}

			fset := token.NewFileSet()
			if _, parseErr := parser.ParseFile(fset, file.Path, file.Content, parser.AllErrors); parseErr != nil {
				t.Fatalf("Generate(%q, %q) produced unparseable Go source for %s: %v\n---\n%s",
					vendor, typeKey, file.Path, parseErr, file.Content)
			}
		}
	})
}
