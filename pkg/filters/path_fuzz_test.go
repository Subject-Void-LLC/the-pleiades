package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// This file fuzzes PathJoin and PathExtractExtension against malformed
// and adversarial input, per this phase's own Fuzz/Stress Test checklist
// item naming both explicitly ("a path with embedded .. segments").
// PathJoin's own signature takes a []string, a shape Go's native fuzzer
// has no corpus support for (only scalar types and []byte/string); three
// fixed string arguments assembled into a slice inside the fuzz function
// itself is the same accommodation this package already makes for
// BuildARN's map[string]any parameter in cloudid_fuzz_test.go.

func FuzzPathJoin(f *testing.F) {
	f.Add("a", "b", "c")
	f.Add("a", "..", "b")
	f.Add("..", "..", "etc")
	f.Add("/etc", "..", "passwd")
	f.Add("", "", "")
	f.Fuzz(func(t *testing.T, a, b, c string) {
		filters.PathJoin([]string{a, b, c})
	})
}

func FuzzPathExtractExtension(f *testing.F) {
	seeds := []string{
		"", "archive.tar.gz", ".bashrc", "README", "a.b/c",
		"../../etc/passwd", `C:\Users\a\file.txt`, "trailing.dot.",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, path string) {
		filters.PathExtractExtension(path)
	})
}
