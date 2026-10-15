// What container.docker.run does with a container that already exists but
// is not running.
package docker

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// paramStart is container.docker.run's choice to start a container that
// exists but is stopped. True by default, as community.docker's
// docker_container (state: started) does.
const paramStart = "start"

// startNeeded reports whether run starts the existing container before, or
// refuses a state that is not stopped and not running.
//
// Exited and created (never started, or a start that failed) are stopped,
// and are started. Running and restarting are the engine's already. Paused
// is not stopped, and unpausing is a different act from starting, so it is
// refused rather than chosen silently; dead and removing cannot be started.
// With start false the container is left as it is, which is what a one-shot
// container (a migration, a backup) whose command is meant to run once
// needs, since starting it again runs its command again.
func startNeeded(name string, before containerState, start bool) (bool, error) {
	if !before.exists || !start {
		return false, nil
	}
	switch before.status {
	case "running", "restarting":
		return false, nil
	case "exited", "created":
		return true, nil
	case "paused":
		return false, fmt.Errorf("container %s is paused, which is not stopped: unpause it (docker unpause), or set %s: false to leave it", name, paramStart)
	}
	return false, fmt.Errorf("container %s is %s, and cannot be started: remove it (container.docker.remove) and run again, or set %s: false to leave it", name, before.status, paramStart)
}

// startContainer starts the container before names, by its id, which
// docker inspect reported and which is refused unless it is a full
// container id, since it is text from the device that becomes an operand.
func startContainer(ctx context.Context, conn *remoteexec.Conn, before containerState) error {
	if !containerIDPattern.MatchString(before.id) {
		return fmt.Errorf("docker inspect gave no usable id to start the container by")
	}
	return runDockerCmd(ctx, conn, []string{"docker", "start", "--", before.id}, startCallTimeout)
}
