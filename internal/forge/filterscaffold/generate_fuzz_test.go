package filterscaffold_test

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/filterscaffold"
)

// FuzzGenerate proves that Generate either rejects a malformed GoName/
// CELName/Category outright, or, for anything it accepts, produces two
// files whose paths never escape pkg/filters/ and whose content always
// parses as valid Go. Mirrors collectionscaffold's own FuzzGenerate,
// adapted for filterscaffold's two-name Config.
func FuzzGenerate(f *testing.F) {
	seeds := []string{
		"CIDRToNetmask", "IPToInt", "", "cidrToNetmask", "CIDR.ToNetmask",
		"../../etc/passwd", "Cidr Netmask", "3Com", "Type", "Switch",
		"CIDR_To_Netmask", "C", "cidrToNetmask..",
	}
	for _, s := range seeds {
		f.Add(s, s, "network")
	}

	f.Fuzz(func(t *testing.T, goName, celName, category string) {
		files, err := filterscaffold.Generate(filterscaffold.Config{
			GoName:   goName,
			CELName:  celName,
			Category: category,
			Summary:  "fuzz summary.",
			Params:   []filterscaffold.Param{{Name: "arg", GoType: "string"}},
			Return:   filterscaffold.Return{GoType: "string"},
		})
		if err != nil {
			return
		}

		if len(files) != 2 {
			t.Fatalf("Generate(%q,%q,%q) returned %d files, want 2", goName, celName, category, len(files))
		}

		for _, file := range files {
			if !strings.HasPrefix(file.Path, "pkg/filters/") {
				t.Fatalf("Generate(%q,%q,%q) produced an escaping path: %q", goName, celName, category, file.Path)
			}
			if strings.Contains(file.Path, "..") {
				t.Fatalf("Generate(%q,%q,%q) produced a path traversal segment: %q", goName, celName, category, file.Path)
			}

			fset := token.NewFileSet()
			if _, parseErr := parser.ParseFile(fset, file.Path, file.Content, parser.AllErrors); parseErr != nil {
				t.Fatalf("Generate(%q,%q,%q) produced unparseable Go source for %s: %v\n---\n%s",
					goName, celName, category, file.Path, parseErr, file.Content)
			}
		}
	})
}

// FuzzReminder proves Reminder either rejects the same malformed input
// Generate would, or never panics producing its paste-ready text. It
// does not parse the output as Go (Reminder's output is a fragment
// meant to be pasted into an existing file, not a standalone one), only
// that it terminates and returns non-empty text on success.
func FuzzReminder(f *testing.F) {
	seeds := []string{"CIDRToNetmask", "IPToInt", "", "cidrToNetmask", "3Com"}
	for _, s := range seeds {
		f.Add(s, s)
	}

	f.Fuzz(func(t *testing.T, goName, celName string) {
		out, err := filterscaffold.Reminder(filterscaffold.Config{
			GoName:   goName,
			CELName:  celName,
			Category: "network",
			Summary:  "fuzz summary.",
			Params:   []filterscaffold.Param{{Name: "arg", GoType: "string"}},
			Return:   filterscaffold.Return{GoType: "string"},
		})
		if err != nil {
			return
		}
		if out == "" {
			t.Fatalf("Reminder(%q,%q) succeeded but returned empty text", goName, celName)
		}
	})
}
