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
	"regexp"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
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
	// paramID is container.docker.remove's identity guard, which the undo
	// of container.docker.run sets.
	paramID = "id"
)

const statName = "name"

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

// restartPolicyPattern is every restart policy docker run accepts: no,
// always, unless-stopped, and on-failure with an optional retry count.
var restartPolicyPattern = regexp.MustCompile(`^(no|always|unless-stopped|on-failure(:[0-9]{1,9})?)$`)

// checkName refuses a container name no engine would give a container
// (containerengine.ValidName's grammar), before anything reaches the device.
// The grammar starts with a letter or digit, so no name can be read as an
// option, and holds no whitespace or shell metacharacter.
func checkName(name string) error {
	return containerengine.ValidName("container", name)
}

// checkRunParams refuses, before anything reaches the device, a value docker
// run would read as something other than what the runbook wrote: an image
// that is not an image reference (containerengine.ValidImage), a restart
// policy docker does not have, or a port or volume that is empty, begins
// with "-" or holds a control character. Each flag also travels in its
// equals form (--publish=VALUE), which binds the value to its flag whatever
// it holds, so the check is the first of two guards rather than the only one.
func checkRunParams(image string, ports, volumes []string, restartPolicy string) error {
	if err := containerengine.ValidImage(image); err != nil {
		return fmt.Errorf("%s: %w", paramImage, err)
	}
	if restartPolicy != "" && !restartPolicyPattern.MatchString(restartPolicy) {
		return fmt.Errorf("%s %q is not one of no, always, unless-stopped or on-failure[:N]", paramRestartPolicy, restartPolicy)
	}
	for _, list := range []struct {
		param  string
		values []string
	}{{paramPorts, ports}, {paramVolumes, volumes}} {
		for _, v := range list.values {
			if err := checkFlagValue(v); err != nil {
				return fmt.Errorf("%s: %w", list.param, err)
			}
		}
	}
	return nil
}

// checkFlagValue refuses a flag's value that is empty, begins with "-", or
// holds a control character or a NUL byte.
func checkFlagValue(v string) error {
	if v == "" {
		return fmt.Errorf("an entry is empty")
	}
	if v[0] == '-' {
		return fmt.Errorf("%q begins with \"-\", which docker would read as an option", v)
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%q holds a control character", v)
		}
	}
	return nil
}

// envParam reads the env param as a map of string keys to string
// values, refusing anything else the way sdk.StringSlice refuses a
// non-string list element: a value that arrived as a number or a bool
// would render differently on the Crawl tier than across the Runner's
// JSON subprocess boundary, and a silently different docker run command
// is worse than a refusal.
func envParam(params map[string]any) (map[string]string, error) {
	raw, present := params[paramEnv]
	if !present || raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a map of string keys to string values", paramEnv)
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%s] is %T, not a string: quote it in the runbook", paramEnv, k, v)
		}
		if err := checkEnvEntry(k, s); err != nil {
			return nil, fmt.Errorf("%s: %w", paramEnv, err)
		}
		out[k] = s
	}
	return out, nil
}

// checkEnvEntry refuses a name or value docker's env-file format cannot
// carry exactly. The values travel as an env file on standard input (see
// dockerRunArgs), one NAME=value per line, where docker skips a line
// starting with "#" and refuses a name holding whitespace, so a name that
// is empty, starts with "#", or holds whitespace, "=" or a NUL byte, and a
// value holding a line break or a NUL byte, would arrive as something other
// than what the runbook wrote.
func checkEnvEntry(name, value string) error {
	if name == "" || strings.HasPrefix(name, "#") || strings.ContainsAny(name, "= \t\r\n\v\f\x00") {
		return fmt.Errorf("variable name %q is empty, starts with #, or holds whitespace, = or a NUL byte", name)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("the value of %s holds a line break or a NUL byte, which an env file cannot carry", name)
	}
	return nil
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
func dockerRunArgs(name, image string, command, ports, volumes []string, env map[string]string, restartPolicy string) (argv []string, stdin string) {
	args := []string{"docker", "run", "-d", "--name=" + name}
	for _, p := range ports {
		args = append(args, "--publish="+p)
	}
	for _, v := range volumes {
		args = append(args, "--volume="+v)
	}
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var file strings.Builder
		for _, k := range keys {
			file.WriteString(k)
			file.WriteByte('=')
			file.WriteString(env[k])
			file.WriteByte('\n')
		}
		args = append(args, "--env-file=/dev/stdin")
		stdin = file.String()
	}
	if restartPolicy != "" {
		args = append(args, "--restart="+restartPolicy)
	}
	args = append(args, "--", image)
	return append(args, command...), stdin
}

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
	if err := runDockerCmd(ctx, conn, []string{"docker", "rm", "--force", "--", left.id}); err != nil {
		return fmt.Errorf("%w; it left container %s (%s) behind, and removing it failed: %v", runErr, name, left.id, err)
	}
	return fmt.Errorf("%w; the container it created and could not start (%s) was removed", runErr, left.id)
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
