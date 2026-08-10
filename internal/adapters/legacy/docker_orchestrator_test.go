package legacy_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/legacy"
)

// TestDockerOrchestrator_RunsRealContainerAndCapturesOutput proves
// DockerOrchestrator against a real Docker daemon (RULE 0: this is the
// exact boundary this type exists to make real, so a fake here would
// prove nothing): a real container starts, a real file crosses in via
// ContainerSpec.Files before the entrypoint runs (never a host bind
// mount: orchestrator.go's own trust-boundary doc comment), the
// entrypoint really reads it, and the real exit code and combined output
// both come back correctly.
func TestDockerOrchestrator_RunsRealContainerAndCapturesOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-Docker container test in short mode")
	}
	orch := legacy.NewDockerOrchestrator()

	spec := legacy.ContainerSpec{
		Image: "alpine:3.20",
		Argv:  []string{"sh", "-c", "cat /run/pleiades/greeting.txt && echo done-marker"},
		Files: []legacy.ContainerFile{
			{Content: []byte("hello from the trust boundary"), ContainerPath: "/run/pleiades/greeting.txt", Mode: 0o600},
		},
	}

	result, err := orch.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0: output=%s", result.ExitCode, result.Output)
	}
	if !strings.Contains(string(result.Output), "hello from the trust boundary") {
		t.Errorf("output does not contain the file content copied in via ContainerSpec.Files: %s", result.Output)
	}
	if !strings.Contains(string(result.Output), "done-marker") {
		t.Errorf("output does not contain the command's own marker: %s", result.Output)
	}
}

// TestDockerOrchestrator_CapturesNonZeroExitCode proves a container that
// runs to completion but exits non-zero is reported through
// ContainerResult.ExitCode, not returned as a Go error -- the same split
// os/exec.Cmd.Run itself draws between "failed to start" and "ran,
// exited non-zero" (orchestrator.go's own doc comment on Run states this
// contract explicitly; this test is what proves it holds).
func TestDockerOrchestrator_CapturesNonZeroExitCode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-Docker container test in short mode")
	}
	orch := legacy.NewDockerOrchestrator()

	result, err := orch.Run(context.Background(), legacy.ContainerSpec{
		Image: "alpine:3.20",
		Argv:  []string{"sh", "-c", "exit 7"},
	})
	if err != nil {
		t.Fatalf("Run returned unexpected error for a container that ran to completion: %v", err)
	}
	if result.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", result.ExitCode)
	}
}
