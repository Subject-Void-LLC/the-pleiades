package legacy_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/legacy"
)

// BenchmarkContainerOrchestrator_SpawnLatency measures the real,
// per-device cost this package's design accepts: one ephemeral Docker
// container, started, run to completion, and torn down, per dispatch.
// This is the same "record the real number now, rather than assuming a
// warm-pool optimization is or is not worth building later" discipline
// internal/adapters/native's own
// BenchmarkIPCCollectionExecutor_SpawnLatency established for its own
// per-task subprocess boundary (Phase 16). Here, the number this
// benchmark produces is the one a future Phase 26 (Execution
// Environments & Container Groups) argument for or against warm pod
// pooling must start from: PLAN.md's own Section 31 already rejects warm
// pod pooling on cross-job contamination grounds, but "how much does
// rejecting it cost" is a distinct, measurable question this benchmark
// answers.
func BenchmarkContainerOrchestrator_SpawnLatency(b *testing.B) {
	if _, err := exec.LookPath("docker"); err != nil {
		b.Skip("docker not found on PATH")
	}
	orch := legacy.NewDockerOrchestrator()
	spec := legacy.ContainerSpec{Image: "alpine:3.20", Argv: []string{"true"}}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := orch.Run(ctx, spec); err != nil {
			b.Fatalf("Run: %v", err)
		}
	}
}
