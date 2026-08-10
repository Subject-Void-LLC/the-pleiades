package legacy

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// DockerOrchestrator implements ContainerOrchestrator against a real,
// local Docker daemon via testcontainers-go, the same real-container
// dependency internal/lock, internal/event, and cmd/runner's own SSH mesh
// Release Gate already use for a real ephemeral NATS/sshd container,
// rather than a mock: RULE 0 (.AGENTS/AGENTS.md) forbids a test or an
// adapter that mocks away the exact boundary it exists to exercise, and
// for this package that boundary is "a real container really runs a real
// ansible-playbook."
//
// It is the one designated place in this repository allowed to import
// testcontainers-go's real Docker client directly (see
// internal/archtest/layering_test.go's adapterAllowlist), the same
// concrete-driver-behind-a-named-adapter shape internal/event and
// internal/lock already hold for NATS.
type DockerOrchestrator struct{}

// NewDockerOrchestrator returns a DockerOrchestrator. It takes no
// arguments and holds no state: every real dependency (the Docker daemon
// itself) is discovered by testcontainers-go per call, the same way
// internal/lock's and internal/event's own container-backed tests never
// hold a client across calls either.
func NewDockerOrchestrator() *DockerOrchestrator {
	return &DockerOrchestrator{}
}

// Run implements ContainerOrchestrator. It starts spec.Image with
// spec.Argv as the container's command, copies spec.Files in before the
// entrypoint runs (testcontainers-go's own PreStart file-copy lifecycle
// hook, which is what makes this package's trust-boundary claim in
// orchestrator.go's own doc comment literally true: the bytes never touch
// this process's own disk), waits for the container to exit on its own
// (wait.ForExit, never a fixed sleep), and returns its combined output and
// exit code. The container is always terminated before Run returns,
// whether it exited cleanly or Run is returning an error.
func (o *DockerOrchestrator) Run(ctx context.Context, spec ContainerSpec) (ContainerResult, error) {
	files := make([]testcontainers.ContainerFile, 0, len(spec.Files))
	for _, f := range spec.Files {
		files = append(files, testcontainers.ContainerFile{
			Reader:            bytes.NewReader(f.Content),
			ContainerFilePath: f.ContainerPath,
			FileMode:          f.Mode,
		})
	}

	req := testcontainers.ContainerRequest{
		Image:      spec.Image,
		Cmd:        spec.Argv,
		Env:        spec.Env,
		Files:      files,
		Networks:   spec.Networks,
		WaitingFor: wait.ForExit(),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return ContainerResult{}, fmt.Errorf("failed to start container %q: %w", spec.Image, err)
	}
	defer func() { _ = container.Terminate(context.Background()) }()

	rc, err := container.Logs(ctx)
	if err != nil {
		return ContainerResult{}, fmt.Errorf("failed to fetch container logs: %w", err)
	}
	defer func() { _ = rc.Close() }()
	output, err := io.ReadAll(rc)
	if err != nil {
		return ContainerResult{}, fmt.Errorf("failed to read container logs: %w", err)
	}

	state, err := container.State(ctx)
	if err != nil {
		return ContainerResult{}, fmt.Errorf("failed to inspect container state: %w", err)
	}

	return ContainerResult{Output: output, ExitCode: state.ExitCode}, nil
}
