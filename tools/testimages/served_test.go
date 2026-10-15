// The registry check against a real Docker daemon: a reference no registry
// serves is named, and an image the engine already holds is never looked
// up.
package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// needDocker skips a test when no daemon answers, and shortens the retry
// waits for it.
func needDocker(t *testing.T) {
	t.Helper()
	daemon := exec.Command("docker", "info").Run()
	testsupport.Require(t, "docker", daemon == nil, "no Docker daemon answered docker info")
	saved := pullBackoff
	pullBackoff = []time.Duration{time.Millisecond}
	t.Cleanup(func() { pullBackoff = saved })
}

// TestServedAll_NamesWhatNoRegistryServes: an unpublished name, and the
// fake image ID CI's pull step once asked Docker Hub for, each fail by
// name without anything being pulled.
func TestServedAll_NamesWhatNoRegistryServes(t *testing.T) {
	needDocker(t)
	for _, image := range []string{
		"pleiades-test-nonexistent/never-published:0.0.0",
		"sha256:9f2c0e6a9b2d3c5e7f8a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e",
	} {
		if err := servedAll([]string{image}); err == nil || !strings.Contains(err.Error(), image) {
			t.Errorf("servedAll(%s) = %v, want it named", image, err)
		}
	}
}

// TestServedAll_AnImageTheEngineHoldsIsNotLookedUp: an image imported here,
// under a name no registry serves, passes, so the engine's copy is what
// answered, and a run with every image already pulled asks no registry.
func TestServedAll_AnImageTheEngineHoldsIsNotLookedUp(t *testing.T) {
	needDocker(t)
	image := "pleiades-test-served/local-only:1"
	// An empty tar archive: two zero blocks.
	imp := exec.Command("docker", "import", "-", image)
	imp.Stdin = bytes.NewReader(make([]byte, 1024))
	if out, err := imp.CombinedOutput(); err != nil {
		t.Fatalf("docker import: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "--force", image).Run() })
	if err := servedAll([]string{image}); err != nil {
		t.Fatalf("an image the engine holds was looked up: %v", err)
	}
}
