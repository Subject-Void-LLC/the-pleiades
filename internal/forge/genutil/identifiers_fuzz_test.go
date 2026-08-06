package genutil_test

import (
	"go/token"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/genutil"
)

// FuzzValidateSegment proves that no input accepted by ValidateSegment can
// ever be an unsafe filesystem path component or an unbuildable Go
// identifier: every accepted segment starts with a lowercase letter,
// contains only [a-z0-9_], contains no path separator or "..", and is not
// a Go reserved keyword. Phase 33's own Fuzz/Stress checklist item asks
// for exactly this: "a vendor name containing .. or a Go keyword must not
// produce an unsafe path or unbuildable source."
func FuzzValidateSegment(f *testing.F) {
	seeds := []string{
		"", "..", "...", "/", "\\", "../../etc/passwd", "..\\..\\windows",
		"foo/bar", "foo\\bar", "foo.bar", "3com", "0day", "9pfs",
		"Cisco", "CISCO", "foo bar", "foo\tbar", "foo\nbar",
		"func", "type", "package", "import", "for", "select", "go",
		"true", "false", "nil", "iota",
		"cisco", "junos_router", "daemon_reload", "a",
		strings.Repeat("a", 63), strings.Repeat("a", 64), strings.Repeat("a", 65), strings.Repeat("a", 10000),
		"foo\x00bar", "foo\xffbar",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		err := genutil.ValidateSegment(s)
		if err != nil {
			return
		}

		// Everything below must hold for every ACCEPTED segment.
		if s == "" {
			t.Fatalf("ValidateSegment accepted an empty segment")
		}
		if strings.ContainsAny(s, "./\\") {
			t.Fatalf("ValidateSegment accepted %q, which contains a path separator or dot", s)
		}
		if strings.Contains(s, "..") {
			t.Fatalf("ValidateSegment accepted %q, which contains a path traversal sequence", s)
		}
		for i, r := range s {
			if i == 0 {
				if r < 'a' || r > 'z' {
					t.Fatalf("ValidateSegment accepted %q, whose first rune %q is not a lowercase letter", s, r)
				}
				continue
			}
			isLower := r >= 'a' && r <= 'z'
			isDigit := r >= '0' && r <= '9'
			if !isLower && !isDigit && r != '_' {
				t.Fatalf("ValidateSegment accepted %q, which contains disallowed rune %q", s, r)
			}
		}
		if token.IsKeyword(s) {
			t.Fatalf("ValidateSegment accepted %q, which is a Go reserved keyword", s)
		}
	})
}
