package legacy_test

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/legacy"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
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

// orphanScript leaves fifty orphans, each a grandchild whose parent
// exits at once, then waits for them to exit and counts the zombies in
// the container. It runs as Python because ansible-playbook is Python,
// and Python as PID 1 reaps only the children it started.
const orphanScript = `import glob, os, time
for _ in range(50):
    pid = os.fork()
    if pid == 0:
        if os.fork() == 0:
            time.sleep(0.2)
            os._exit(0)
        os._exit(0)
    os.waitpid(pid, 0)
time.sleep(1.5)
zombies = 0
for status in glob.glob("/proc/[0-9]*/status"):
    try:
        with open(status) as f:
            if any(line.startswith("State:") and line.split()[1] == "Z" for line in f):
                zombies += 1
    except OSError:
        pass
print(f"pid={os.getpid()} zombies={zombies}", flush=True)
`

// TestDockerOrchestrator_ReapsOrphans proves the command runs under an
// init rather than as PID 1, in the image the Ansible adapter runs: the
// command's own pid is not 1, and the fifty orphans it leaves are reaped
// rather than left as zombies. Without an init the command is PID 1 and
// all fifty stay zombies until the container exits (FAILURE_PATTERNS
// 343).
func TestDockerOrchestrator_ReapsOrphans(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-Docker container test in short mode")
	}
	image := testsupport.BuildAnsibleRunnerImage(t)
	result, err := legacy.NewDockerOrchestrator().Run(context.Background(), legacy.ContainerSpec{
		Image: image,
		Argv:  []string{"python3", "-c", orphanScript},
	})
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0: output=%s", result.ExitCode, result.Output)
	}
	var pid, zombies int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(result.Output)), "pid=%d zombies=%d", &pid, &zombies); err != nil {
		t.Fatalf("unreadable output %q: %v", result.Output, err)
	}
	if pid == 1 {
		t.Errorf("the command ran as PID 1: nothing but itself can reap its orphans")
	}
	if zombies != 0 {
		t.Errorf("%d zombies left in the container, want 0", zombies)
	}
}

// TestDockerOrchestrator_ACanceledRunLeavesNoContainerRunning proves a run
// whose context ends while its container is still working takes the
// container down with it. A canceled or timed-out job reaches Run exactly
// this way, and the container is an ansible-playbook changing devices: one
// left running would keep changing them after the job had been reported
// stopped. testcontainers-go returns the container alongside the start
// error when the wait for its exit is cut short, so Run has to terminate
// it on that path too (FAILURE_PATTERNS 423).
func TestDockerOrchestrator_ACanceledRunLeavesNoContainerRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-Docker container test in short mode")
	}
	_, lookErr := exec.LookPath("docker")
	testsupport.Require(t, "docker", lookErr == nil, "the docker CLI is not installed")

	// A marker in the command, so this test finds its own container and no
	// other test's.
	marker := fmt.Sprintf("pleiades-canceled-run-%d", time.Now().UnixNano())
	running := func() []string {
		out, err := exec.Command("docker", "ps", "--no-trunc", "--format", "{{.ID}} {{.Command}}").Output()
		if err != nil {
			t.Fatalf("docker ps: %v", err)
		}
		var ids []string
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, marker) {
				ids = append(ids, strings.Fields(line)[0])
			}
		}
		return ids
	}
	t.Cleanup(func() {
		for _, id := range running() {
			_ = exec.Command("docker", "rm", "-f", id).Run()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := legacy.NewDockerOrchestrator().Run(ctx, legacy.ContainerSpec{
		Image: "alpine:3.20",
		Argv:  []string{"sh", "-c", "sleep 300 # " + marker},
	})
	if err == nil {
		t.Fatal("Run returned no error for a run whose context ended while its container was still running")
	}
	if ids := running(); len(ids) != 0 {
		t.Errorf("the container %v is still running after its run ended with %v", ids, err)
	}
}
