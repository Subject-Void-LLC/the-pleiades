package testsupport

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// AnsibleRunnerImageTag is the fixed local tag BuildAnsibleRunnerImage
// builds Dockerfile.legacy-ansible-runner under, once per test run; the
// docker layer cache makes repeat builds a no-op.
const AnsibleRunnerImageTag = "pleiades/legacy-ansible-runner:release-gate"

// BuildAnsibleRunnerImage builds the real, repository-committed
// Dockerfile.legacy-ansible-runner via a real `docker build`, the same
// image a real Runner deployment would run, rather than a synthetic
// stand-in. Skips the calling test if docker is not on PATH.
//
// It lives here rather than in cmd/runner's release gate, where it first
// shipped, because two suites now need the identical image: the release
// gate proves legacy.Adapter executes a real playbook when composed by
// hand, and tests/e2e proves the production binaries compose it at all.
// The second suite exists because the first one passing was mistaken for
// the wiring existing (FAILURE_PATTERNS.md #112).
func BuildAnsibleRunnerImage(tb testing.TB) string {
	tb.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		tb.Skip("docker not found on PATH")
	}

	root := RepoRoot(tb)
	dockerfile := filepath.Join(root, "Dockerfile.legacy-ansible-runner")
	if _, err := os.Stat(dockerfile); err != nil {
		tb.Fatalf("Dockerfile.legacy-ansible-runner not found at %s: %v", dockerfile, err)
	}

	cmd := exec.Command("docker", "build", "-f", dockerfile, "-t", AnsibleRunnerImageTag, root)
	if out, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("docker build failed: %v\n%s", err, out)
	}
	return AnsibleRunnerImageTag
}
