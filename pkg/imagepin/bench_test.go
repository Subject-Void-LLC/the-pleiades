// A benchmark of a file with many references, the case a per-reference
// count from the start of the file made cost size times references.
package imagepin

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkExtract_ManyReferences extracts from a Compose file near the
// size bound holding one image per service.
func BenchmarkExtract_ManyReferences(b *testing.B) {
	var file strings.Builder
	file.WriteString("services:\n")
	for i := 0; file.Len() < MaxFileBytes-64; i++ {
		fmt.Fprintf(&file, "  s%d:\n    image: a:1\n", i)
	}
	content := []byte(file.String())
	b.ResetTimer()
	for range b.N {
		if _, err := Extract(KindCompose, content); err != nil {
			b.Fatal(err)
		}
	}
}
