// Package docker implements the three "container.docker.*" Collection
// methods: run, stop and remove.
//
// # No new pkg/ primitive
//
// Container state is read with docker inspect and changed with docker
// run/stop/rm, plain SSH commands through pkg/remoteexec, the same tier
// pkg.apt.* and identity.user.* already ship at. Nothing here needed a
// shared primitive of its own.
//
// # Scoped well below community.docker.docker_container
//
// That module's own parameter surface is enormous: networks, health
// checks, logging drivers, capabilities, ulimits, and a config-diff
// idempotency model that decides whether an existing container's actual
// configuration matches what was requested closely enough to leave it
// alone or recreate it. This namespace takes the same restraint
// identity.user.* took against full ansible.builtin.user parity: run is
// idempotent on the container NAME existing only, not on any of its
// configuration, and it does not recreate. A container already present
// under the requested name is left exactly as it is, documented plainly
// rather than silently doing a partial job.
//
// # Two of the three methods have no safe inverse
//
// run's inverse is clean when it actually created something: remove the
// container it just created. stop and remove do not have an equally
// clean one. This catalog declares no container.docker.start, so
// recording container.docker.run as stop's inverse would be dishonest:
// run's own idempotency means it would just no-op against the existing
// name rather than actually restart it. And while docker inspect does
// expose a removed container's image and some config before deletion,
// reconstructing ports, volumes, env and restart policy from that output
// well enough for a real re-run is parsing this pass does not take on; a
// partial inverse that silently drops configuration is worse than an
// honest refusal, the same call pkg.upgrade makes for a package version
// a repository may no longer offer.
package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameter names. name, image, command, restart_policy are
// community.docker.docker_container's own names. ports and volumes are
// too, each a list of "host:container" strings rather than that
// module's richer list-of-dicts form. env is that module's own name for
// a map of environment variables. force is that module's own name for
// removing a container that is still running.
const (
	paramName          = "name"
	paramImage         = "image"
	paramCommand       = "command"
	paramPorts         = "ports"
	paramVolumes       = "volumes"
	paramEnv           = "env"
	paramRestartPolicy = "restart_policy"
	paramForce         = "force"
	// paramNetwork is community.docker.docker_container's network_mode,
	// named for what it is; none when absent (networkDefault).
	paramNetwork = "network"
	// paramID is container.docker.remove's identity guard, which the undo
	// of container.docker.run sets.
	paramID = "id"
)

const (
	statName = "name"
	// statImageID is the image ID a run started its container from.
	statImageID = "image_id"
)

// imageLabel is the label a run puts on its container naming the image
// reference it was asked for, since the container itself names the ID it
// was run by.
const imageLabel = "org.pleiades.image"

// containerState is what docker inspect reports about one container
// name.
type containerState struct {
	exists bool
	status string // running, exited, created, paused, restarting, removing, dead; empty when absent
	// id is the container's full id, empty when absent. It is not part of
	// Map (and so of a diff): it names which container this is, which is
	// what container.docker.remove's id guard compares, not a state.
	id string
}

func (s containerState) Map() map[string]any {
	return map[string]any{"exists": s.exists, "status": s.status}
}

// queryContainer asks docker inspect for a container's id and status.
// docker inspect gives no separate exit code for "no such container" the
// way getent's 2 does (identity/user/user.go's queryUser), so absence is
// read from its message instead (noSuchContainer).
//
// Only docker's own "No such container" answer reads as absent. Any other
// non-zero exit (a daemon that is down, a socket the login cannot open) is
// "could not look", which is an error and never an absence: read as one, it
// had run create a container on an engine it had not reached, and stop and
// remove report a container they could not see as already gone. Measured
// against Docker 29.8 on 2026-10-14: a missing container exits 1 with
// "No such container: NAME", an unreachable daemon exits 1 with "failed to
// connect to the docker API".
//
// The name follows "--", so a value that begins with "-" can never be read
// as an option; checkName refuses one before this runs anyway.
func queryContainer(ctx context.Context, conn *remoteexec.Conn, name string) (containerState, error) {
	result, err := conn.RunWithStdinBounded(ctx, remoteexec.QuoteCommand([]string{"docker", "inspect", "--type=container", "--format={{.Id}} {{.State.Status}}", "--", name}), nil, dockerOutputLimit)
	if err != nil {
		return containerState{}, err
	}
	if result.ExitCode != 0 {
		if noSuchContainer(result) {
			return containerState{}, nil
		}
		return containerState{}, fmt.Errorf("docker inspect could not read container %s (exit %d): %s", name, result.ExitCode, failureDetail(result))
	}
	id, status, _ := strings.Cut(strings.TrimSpace(result.Stdout), " ")
	return containerState{exists: true, status: status, id: id}, nil
}

// noSuchContainer reports whether a failed docker inspect said the container
// does not exist, which the daemon words as "No such container" (and older
// CLIs, inspecting with no --type, as "No such object").
func noSuchContainer(result remoteexec.Result) bool {
	text := strings.ToLower(result.Stderr)
	return strings.Contains(text, "no such container") || strings.Contains(text, "no such object")
}

// dockerOutputLimit bounds what one docker command may write to each of
// standard output and standard error. None of the commands here prints more
// than a container id, an inspect line or a pull's progress, so a command
// that writes more is cut off and fails rather than growing the memory of
// the process running the task.
const dockerOutputLimit = 1 << 20

// maxFailureDetail bounds how much of a failed command's own text an error
// quotes.
const maxFailureDetail = 512

// runDockerCmd runs one docker invocation and treats a non-zero exit as
// a real error.
func runDockerCmd(ctx context.Context, conn *remoteexec.Conn, argv []string) error {
	_, err := runDockerCmdWithStdin(ctx, conn, argv, "")
	return err
}

// runDockerCmdWithStdin is runDockerCmd with stdin piped to the command,
// returning what it printed on standard output. The error quotes argv,
// which is why nothing secret may be in it.
func runDockerCmdWithStdin(ctx context.Context, conn *remoteexec.Conn, argv []string, stdin string) (string, error) {
	command := remoteexec.QuoteCommand(argv)
	result, err := conn.RunWithStdinBounded(ctx, command, strings.NewReader(stdin), dockerOutputLimit)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("%s exited %d: %s", command, result.ExitCode, failureDetail(result))
	}
	return result.Stdout, nil
}

// failureDetail picks the stream an operator should read after a
// non-zero exit, cut to maxFailureDetail bytes with anything a terminal acts
// on escaped (remoteexec.Excerpt), since it is the device's own text and it
// reaches an error, a log and a terminal. docker almost always explains
// itself on stderr.
func failureDetail(result remoteexec.Result) string {
	if detail := strings.TrimSpace(result.Stderr); detail != "" {
		return remoteexec.Excerpt(detail, maxFailureDetail)
	}
	if detail := strings.TrimSpace(result.Stdout); detail != "" {
		return remoteexec.Excerpt(detail, maxFailureDetail)
	}
	return "no output"
}

// recordState writes the container name and the before/after diff, the
// three methods in this namespace's own common report.
func recordState(rc sdk.RunbookContext, name string, before, after containerState) error {
	if err := rc.SetStat(statName, name); err != nil {
		return err
	}
	return sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after.Map()})
}

// predictState is a check's report for a container found as before: the
// same name stat and diff a real run records, with predicted as the after
// half when a real run would change something, and nothing undone, since
// nothing was done.
func predictState(rc sdk.RunbookContext, fqcn, name string, before containerState, changed bool, predicted map[string]any) (collection.Result, error) {
	after := before.Map()
	if changed {
		after = predicted
	}
	if err := rc.SetStat(statName, name); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: changed}, nil
}
