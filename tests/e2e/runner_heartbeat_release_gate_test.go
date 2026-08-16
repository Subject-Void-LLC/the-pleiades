//go:build integration

// The assertion FAILURE_PATTERNS.md #119 has been waiting for.
//
// #119, recorded FOUND and NOT FIXED for its whole life: "A Runner whose
// NATS connection closes for good stays alive, stays healthy-looking, and
// silently stops doing any work." Every earlier statement of it was a
// description. This file is the proof, and it is also the proof that the
// fix works, because it asserts both halves of the same run:
//
//  1. A runner attached to a live broker answers `runner healthcheck`
//     with exit 0.
//  2. The broker is DESTROYED under it. The process stays up, which is
//     #119 itself and is asserted rather than assumed, and the same
//     `runner healthcheck` starts answering non-zero.
//
// NOTHING HERE IS A STAND-IN, which is what makes it evidence under RULE
// 0 rather than a description of one. The runner is the real binary built
// by this package's TestMain, launched as a real operating system process
// with the environment a deployment gives it. The broker is a real NATS
// server in a container, and it is really terminated: not paused, not
// firewalled, not a fake client returning errors. The probe is the same
// `runner healthcheck` argument vector helm/the-pleiades and
// docker-compose.yml run, invoked as a separate process exactly as a
// kubelet or Docker would invoke it, and judged by its exit code alone.
//
// WHY IT DOES NOT USE startHarness. That builds the whole mesh: a
// PostgreSQL container, a controller, seeded inventory. None of it is on
// the path this asserts, and one of the two claims here is destructive to
// the broker every other assertion in a harness would need.
package e2e

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// heartbeatGateMaxAge is the staleness limit this gate probes with.
//
// It is not a number invented for the test: it is the readiness threshold
// helm/the-pleiades ships (runner.heartbeat.readinessStaleAfterSeconds),
// so what runs here is a value a deployment really uses. The liveness
// threshold is the same rule with a longer duration
// (runner.DefaultHeartbeatMaxAge, 60s), and asserting the mechanism at
// the shorter of the two proves the mechanism at both while keeping this
// gate to about half a minute of waiting rather than a minute and a half.
const heartbeatGateMaxAge = 30 * time.Second

// heartbeatGateStaleBudget bounds how long the gate waits for the probe
// to turn unhealthy after the broker is destroyed.
//
// The heartbeat stops on the first withheld beat, so the file's age
// crosses heartbeatGateMaxAge roughly that long after the broker dies.
// The budget is generous on top of that because it also covers a probe
// round trip and a loaded CI machine, and because a gate that fails on
// timing teaches people to ignore it.
const heartbeatGateStaleBudget = heartbeatGateMaxAge + 60*time.Second

// TestRunnerHeartbeatReleaseGate_SeveredBrokerIsDetected is Phase 20's
// runner-liveness Release Gate.
func TestRunnerHeartbeatReleaseGate_SeveredBrokerIsDetected(t *testing.T) {
	ctx := context.Background()

	natsC, natsURL := startDisposableNATS(t, ctx)

	runbookDir := t.TempDir()
	writeRunbookFixture(t, runbookDir)

	// An explicit path under the test's own temporary directory rather
	// than the built-in /tmp default, for one reason: several runs of
	// this suite on one machine must not share a heartbeat file. The
	// binary reads the same RUNNER_HEARTBEAT_FILE a chart and a compose
	// file set, so the mechanism under test is unchanged.
	heartbeat := runner.HeartbeatPath(filepath.Join(t.TempDir(), "runner", "heartbeat"))

	proc := startProcess(t, "runner", runnerBinPath, []string{
		"NATS_URL=" + natsURL,
		"RUNBOOK_DIR=" + runbookDir,
		"RUNNER_HEARTBEAT_FILE=" + heartbeat.String(),
		"OTEL_TRACES_EXPORTER=none",
	})

	// PART ONE: a runner with a live broker reports healthy.
	//
	// Waiting for the probe to pass, rather than for the file to exist,
	// is deliberate: the thing this gate claims is about the exit code of
	// the shipped command, so the shipped command is what is polled.
	waitForHealthcheck(t, heartbeat, 0, 60*time.Second,
		"a runner attached to a live broker never reported healthy")
	t.Logf("`runner healthcheck` exits 0 against a live broker")

	before, err := runner.HeartbeatAge(heartbeat, time.Now())
	if err != nil {
		t.Fatalf("reading the heartbeat age while healthy: %v", err)
	}
	if before > heartbeatGateMaxAge {
		t.Fatalf("the heartbeat was already %s old while the probe reported healthy, which means the probe is not reading what the runner writes", before)
	}

	// PART TWO: destroy the broker.
	//
	// Terminate, not stop, and not a network pause. The container and its
	// JetStream store go away, so nothing the client is holding can
	// resume, and the mapped port stops answering entirely. That is the
	// permanent loss #119 is about rather than the transient blip the
	// fetch loop is supposed to survive.
	if err := testcontainers.TerminateContainer(natsC); err != nil {
		t.Fatalf("terminating the nats container: %v", err)
	}
	t.Logf("the broker is gone; the runner is on its own")

	// #119 ITSELF, asserted rather than assumed. If this process had
	// exited when its broker died, there would be no defect to fix and no
	// probe to need: the orchestrator's restart policy would already
	// cover it. The whole reason a liveness probe is required here is
	// that the process stays up.
	if exited, out := processHasExited(proc, 5*time.Second); exited {
		t.Fatalf("the runner exited when its broker was destroyed, which is not the failure #119 describes and would make this probe unnecessary\n%s", out)
	}

	// PART THREE: the probe notices.
	waitForHealthcheck(t, heartbeat, 1, heartbeatGateStaleBudget,
		"the heartbeat never went stale after the broker was destroyed, so this probe cannot detect the failure it exists for")

	after, err := runner.HeartbeatAge(heartbeat, time.Now())
	if err != nil {
		t.Fatalf("reading the heartbeat age after the cut: %v", err)
	}
	t.Logf("`runner healthcheck` exits 1: the heartbeat is %s old (limit %s) while the runner process is still alive",
		after.Round(time.Second), heartbeatGateMaxAge)

	// Still alive at the END of the window too, not merely at the start
	// of it. This is what makes the exit code the only thing that changed.
	if exited, out := processHasExited(proc, 0); exited {
		t.Fatalf("the runner exited during the staleness window, so the probe's non-zero answer cannot be attributed to the heartbeat\n%s", out)
	}
}

// startDisposableNATS starts a broker this test is allowed to destroy,
// and returns the container alongside its URL.
//
// Separate from the harness's own startNATS because that one keeps the
// container private and registers a cleanup that owns its lifetime. This
// gate has to terminate it mid-test, so it needs the handle, and its
// cleanup tolerates a container that is already gone.
func startDisposableNATS(t *testing.T, ctx context.Context) (testcontainers.Container, string) {
	t.Helper()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        testsupport.NATSImage,
			ExposedPorts: []string{"4222/tcp"},
			Cmd:          []string{"-js"},
			WaitingFor:   wait.ForLog("Server is ready").WithStartupTimeout(testsupport.ContainerStartupTimeout),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("starting the nats container: %v", err)
	}
	// Terminating an already-terminated container is not an error worth
	// failing a passing test over: this gate destroys it on purpose.
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })

	endpoint, err := container.Endpoint(ctx, "")
	if err != nil {
		t.Fatalf("reading the nats endpoint: %v", err)
	}
	return container, "nats://" + endpoint
}

// waitForHealthcheck polls the real `runner healthcheck` subcommand until
// it exits with want, or fails the test with why.
//
// It runs the BINARY, as a separate process, with the same argument
// vector the chart's probe uses. Calling the Go function directly would
// be faster and would prove less: it would skip the argument routing that
// makes `healthcheck` mean something other than "start a second agent",
// which is the half of this feature a chart depends on and the half no
// in-process call can exercise.
func waitForHealthcheck(t *testing.T, path runner.HeartbeatPath, want int, budget time.Duration, why string) {
	t.Helper()

	deadline := time.Now().Add(budget * raceTimeScale)
	var lastCode int
	var lastOutput string
	for {
		lastCode, lastOutput = runHealthcheckBinary(t, path)
		if lastCode == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: `runner healthcheck` still exits %d after %s, want %d\n%s",
				why, lastCode, budget, want, lastOutput)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// runHealthcheckBinary runs one probe and returns its exit code and its
// combined output.
//
// A non-zero exit is an ordinary answer here rather than a failure to
// report, which is why the ExitError is unwrapped instead of raised.
func runHealthcheckBinary(t *testing.T, path runner.HeartbeatPath) (int, string) {
	t.Helper()

	// #nosec G204 -- runnerBinPath is this package's own TestMain build
	// output and the arguments are literals plus a path this test made in
	// its own temporary directory. Nothing here comes from outside the
	// test binary.
	cmd := exec.Command(runnerBinPath, "healthcheck",
		"-path", path.String(),
		"-max-age", heartbeatGateMaxAge.String())
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), string(out)
	}
	// Anything that is not an exit status (the binary is missing, the
	// fork failed) is this gate being broken rather than an answer about
	// the runner.
	t.Fatalf("running `runner healthcheck`: %v\n%s", err, out)
	return -1, string(out)
}

// processHasExited reports whether proc has finished within grace,
// returning its retained output so a failure can say what it printed on
// the way out.
func processHasExited(proc *managedProc, grace time.Duration) (bool, string) {
	if grace <= 0 {
		select {
		case <-proc.done:
			return true, proc.output()
		default:
			return false, ""
		}
	}
	select {
	case <-proc.done:
		return true, proc.output()
	case <-time.After(grace):
		return false, ""
	}
}
