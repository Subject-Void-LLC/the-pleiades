// Building docker run's command line, and what a run that created a
// container leaves behind.
package docker

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// runSpec is the one container a docker run creates.
type runSpec struct {
	name, imageRef, imageID, network, restartPolicy string
	command, ports, volumes                         []string
	env                                             map[string]string
}

// dockerRunArgs builds a fresh container's full docker run invocation and
// the standard input it reads. -d always: this platform has no interactive
// session to attach one to.
//
// Environment values never go on the command line. Until Phase 119a they
// went as -e NAME=value, where any user on the host could read them with
// ps while the command ran, and where a failed run quoted them back in its
// error (finding S2). They go as an env file on standard input instead
// (--env-file=/dev/stdin), which the docker CLI reads before it creates the
// container; entries checkEnvEntry refused never reach here.
//
// Every flag takes its value in the equals form (--name=web), which binds the
// value to its flag whatever it holds, and "--" ends the options before the
// image, so neither an image nor anything after it is ever read as an option
// (LESSONS_LEARNED 137: quoting stops a shell, not option parsing). The
// command after the image is the container's own, where a leading "-" is
// ordinary ("nginx -g ...").
//
// The container joins the network its task names (none by default), mounts
// each volume read-only unless the entry says rw (readOnlyVolume), and starts
// from the image ID resolveImage found, labeled with the reference asked for.
func dockerRunArgs(s runSpec) (argv []string, stdin string) {
	args := []string{"docker", "run", "-d", "--name=" + s.name, "--network=" + s.network, "--label=" + imageLabel + "=" + s.imageRef}
	for _, p := range s.ports {
		args = append(args, "--publish="+p)
	}
	for _, v := range s.volumes {
		args = append(args, "--volume="+v)
	}
	if len(s.env) > 0 {
		keys := make([]string, 0, len(s.env))
		for k := range s.env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var file strings.Builder
		for _, k := range keys {
			file.WriteString(k)
			file.WriteByte('=')
			file.WriteString(s.env[k])
			file.WriteByte('\n')
		}
		args = append(args, "--env-file=/dev/stdin")
		stdin = file.String()
	}
	if s.restartPolicy != "" {
		args = append(args, "--restart="+s.restartPolicy)
	}
	args = append(args, "--", s.imageID)
	return append(args, s.command...), stdin
}

// containerIDPattern is a full container id as docker prints and inspects
// it: 64 lowercase hexadecimal digits.
var containerIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// createdContainerID reads the id docker run -d prints on its last line of
// standard output. It is refused unless it is a full container id: it is
// text from the device, and it becomes the undo's identity guard and a later
// command's operand.
func createdContainerID(stdout string) (string, error) {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	id := strings.TrimSpace(lines[len(lines)-1])
	if !containerIDPattern.MatchString(id) {
		return "", fmt.Errorf("docker run succeeded but printed no container id")
	}
	return id, nil
}

// cleanupFailedRun is what a docker run that exited non-zero leaves behind.
// docker creates a container before it starts one, so a start that fails (a
// host port already taken) leaves the container "created" under the name,
// and the engine drops a failed task's undo; the next run would find the
// name, report no change, and never start it. So the container is removed
// here, by the id docker inspect reports, when it is still "created", and
// never when docker said the name was already in use, which means the
// container is someone else's. The returned error is runErr, with what
// happened to the container appended.
func cleanupFailedRun(ctx context.Context, conn *remoteexec.Conn, name string, runErr error) error {
	if strings.Contains(runErr.Error(), "is already in use") {
		return runErr
	}
	left, err := queryContainer(ctx, conn, name)
	switch {
	case err != nil:
		return fmt.Errorf("%w; whether it left container %s behind is unknown: %v", runErr, name, err)
	case !left.exists:
		return runErr
	case left.status != "created":
		return fmt.Errorf("%w; container %s exists and is %s, so it is not this task's to remove", runErr, name, left.status)
	case !containerIDPattern.MatchString(left.id):
		return fmt.Errorf("%w; it left container %s behind, and docker inspect gave no usable id to remove it by", runErr, name)
	}
	if err := runDockerCmd(ctx, conn, []string{"docker", "rm", "--force", "--", left.id}, removeCallTimeout); err != nil {
		return fmt.Errorf("%w; it left container %s (%s) behind, and removing it failed: %v", runErr, name, left.id, err)
	}
	return fmt.Errorf("%w; the container it created and could not start (%s) was removed", runErr, left.id)
}
