// The puller against a real Docker daemon: an image that does not exist is
// retried and then named, never reported as pulled.
package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestPullAll_NamesAnImageThatNeverArrived proves a failed pull is retried
// and then reported by name. The waits are shortened for the test; the
// image cannot exist, so no registry can answer it.
func TestPullAll_NamesAnImageThatNeverArrived(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("needs a Docker daemon: docker info failed")
	}
	saved := pullBackoff
	pullBackoff = []time.Duration{time.Millisecond}
	t.Cleanup(func() { pullBackoff = saved })

	image := "pleiades-test-nonexistent/never-published:0.0.0"
	err := pullAll([]string{image})
	if err == nil || !strings.Contains(err.Error(), image) {
		t.Fatalf("err = %v, want it to name %s", err, image)
	}
}
