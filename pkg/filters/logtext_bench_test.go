package filters_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// BenchmarkSyslogParse measures the RFC 5424 path, including its
// STRUCTURED-DATA bracket/quote/escape scanner -- the most involved
// parsing this phase does.
func BenchmarkSyslogParse(b *testing.B) {
	line := `<165>1 2003-10-11T22:14:15.003Z mymachine.example.com su 1234 ID47 [exampleSDID@32473 iut="3" eventSource="Application" eventID="1011"] an application event log entry`
	for i := 0; i < b.N; i++ {
		filters.SyslogParse(line)
	}
}

func BenchmarkLineEndingConvert(b *testing.B) {
	content := strings.Repeat("line of log content\r\n", 200)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filters.LineEndingConvert(content, "lf")
	}
}

func BenchmarkTrimNormalizeWhitespace(b *testing.B) {
	s := strings.Repeat("word   \t  word\n\n", 200)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filters.TrimNormalizeWhitespace(s)
	}
}

func BenchmarkPayloadChunker(b *testing.B) {
	items := make([]any, 1000)
	for i := range items {
		items[i] = i
	}
	for i := 0; i < b.N; i++ {
		filters.PayloadChunker(items, 25)
	}
}

func BenchmarkPathJoin(b *testing.B) {
	parts := []string{"var", "log", "..", "log", "app", "current.log"}
	for i := 0; i < b.N; i++ {
		filters.PathJoin(parts)
	}
}

func BenchmarkPathExtractExtension(b *testing.B) {
	for i := 0; i < b.N; i++ {
		filters.PathExtractExtension("/var/log/app/archive.tar.gz")
	}
}

// BenchmarkGzipCompress and BenchmarkGzipDecompress measure the round
// trip against a realistic multi-kilobyte log excerpt, establishing that
// this phase's decompression-bomb guard (maxGzipDecompressedBytes) costs
// nothing on the legitimate path.
func BenchmarkGzipCompress(b *testing.B) {
	content := strings.Repeat("2024-01-01T00:00:00Z app[123]: request completed in 42ms\n", 200)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filters.GzipCompress(content)
	}
}

func BenchmarkGzipDecompress(b *testing.B) {
	content := strings.Repeat("2024-01-01T00:00:00Z app[123]: request completed in 42ms\n", 200)
	compressed := filters.GzipCompress(content)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filters.GzipDecompress(compressed)
	}
}
