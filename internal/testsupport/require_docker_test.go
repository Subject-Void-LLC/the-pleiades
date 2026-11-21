// Tests for DockerMissing: the reason a TestMain hands facttest when the
// tests that start a container skip, and "" when the docker command is on
// PATH. Each sets PATH, so none runs in parallel.
package testsupport

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
)

func TestDockerMissing_NamesTheNeedWithNoDockerOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	why := DockerMissing()
	if !strings.HasPrefix(why, "needs "+need.Docker+": ") {
		t.Errorf("DockerMissing() with an empty PATH = %q, want the need named as Require names it", why)
	}
}

func TestDockerMissing_IsEmptyWithDockerOnPath(t *testing.T) {
	dir := t.TempDir()
	name := "docker"
	if runtime.GOOS == "windows" {
		name += ".exe" // what exec.LookPath looks for there
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- an executable the lookup must find
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if got := DockerMissing(); got != "" {
		t.Errorf("DockerMissing() with docker on PATH = %q, want \"\"", got)
	}
}
