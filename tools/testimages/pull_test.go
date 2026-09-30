// The puller against a real Docker daemon: an image that does not exist is
// retried and then named, never reported as pulled.
package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// TestPullAll_NamesAnImageThatNeverArrived proves a failed pull is retried
// and then reported by name. The waits are shortened for the test; the
// image cannot exist, so no registry can answer it.
func TestPullAll_NamesAnImageThatNeverArrived(t *testing.T) {
	daemon := exec.Command("docker", "info").Run()
	testsupport.Require(t, "docker", daemon == nil, "no Docker daemon answered docker info")
	saved := pullBackoff
	pullBackoff = []time.Duration{time.Millisecond}
	t.Cleanup(func() { pullBackoff = saved })

	image := "pleiades-test-nonexistent/never-published:0.0.0"
	err := pullAll([]string{image})
	if err == nil || !strings.Contains(err.Error(), image) {
		t.Fatalf("err = %v, want it to name %s", err, image)
	}
}
