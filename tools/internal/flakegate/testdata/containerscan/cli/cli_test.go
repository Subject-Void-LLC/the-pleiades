// Package cli runs a container with the podman command.
package cli

import (
	"context"
	"os/exec"
	"testing"
)

func TestCLI(t *testing.T) {
	_ = exec.CommandContext(context.Background(), "podman", "run", "--rm", "alpine").Run()
}
