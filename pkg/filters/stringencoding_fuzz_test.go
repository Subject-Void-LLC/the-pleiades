package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// FuzzRegexExtract proves RegexExtract never panics regardless of how
// malformed s, pattern, or groupName are (an invalid regex, a pattern
// with no named groups, a group name that never appears), per this
// phase's own Fuzz/Stress Test checklist item.
func FuzzRegexExtract(f *testing.F) {
	seeds := []struct{ s, pattern, groupName string }{
		{"host1.example.com", `^(?P<host>[^.]+)\.`, "host"},
		{"x", "(", "host"},
		{"", "", ""},
		{"a", `(?P<a>a)(?P<b>b)?`, "b"},
		{"aaa", `(a+)+$`, "a"}, // RE2-safe: no catastrophic backtracking either way.
	}
	for _, s := range seeds {
		f.Add(s.s, s.pattern, s.groupName)
	}
	f.Fuzz(func(t *testing.T, s, pattern, groupName string) {
		filters.RegexExtract(s, pattern, groupName)
	})
}

// FuzzWindowsPathToPOSIX exercises both path-conversion functions
// together, since both are simple, symmetric separator swaps over
// arbitrary input.
func FuzzWindowsPathToPOSIX(f *testing.F) {
	seeds := []string{"", `C:\Users\foo`, "/already/posix", `\\server\share`, "mixed/and\\slashes"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, path string) {
		filters.WindowsPathToPOSIX(path)
		filters.POSIXPathToWindows(path)
		filters.IsAbsolutePath(path)
	})
}

// FuzzOctalToSymbolicPerms proves both permission-conversion functions
// never panic on malformed input (too short, too long, non-octal or
// non-symbolic characters).
func FuzzOctalToSymbolicPerms(f *testing.F) {
	seeds := []string{"755", "0755", "4755", "", "abc", "12345", "7777", "999"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, octal string) {
		filters.OctalToSymbolicPerms(octal)
	})
}

// FuzzSymbolicToOctalPerms mirrors FuzzOctalToSymbolicPerms for the
// reverse direction.
func FuzzSymbolicToOctalPerms(f *testing.F) {
	seeds := []string{"rwxr-xr-x", "rwsr-xr-x", "rwSr-xr-x", "", "bogus", "rwxrwxrwq", "---------"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, symbolic string) {
		filters.SymbolicToOctalPerms(symbolic)
	})
}
