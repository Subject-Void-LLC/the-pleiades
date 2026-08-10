package legacy_test

import (
	"os"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/legacy"
)

// BenchmarkParseStdout measures ParseStdout's own pure throughput, no
// Docker, no subprocess: the real captured two-host, three-task fixture
// (testdata/real_playbook_run.txt) parsed repeatedly.
func BenchmarkParseStdout(b *testing.B) {
	output, err := os.ReadFile("testdata/real_playbook_run.txt")
	if err != nil {
		b.Fatalf("failed to read fixture: %v", err)
	}
	now := time.Now()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		legacy.ParseStdout(output, now)
	}
}
