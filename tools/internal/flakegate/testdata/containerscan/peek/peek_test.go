// Package peek only looks: it asks the daemon its state, names a wait
// strategy and calls a method that starts nothing.
package peek

import (
	"os/exec"
	"testing"

	"example.com/scan/helper"
)

func TestPeek(t *testing.T) {
	helper.Require(t)
	helper.Info()
	_ = helper.Strategy()
	var f helper.Fixture
	f.Peek()
	_ = exec.Command("docker", "volume", "ls").Run()
	_ = exec.Command("podman").Run()
	_ = exec.Command("make", "run").Run()
}
