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
	"sort"
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

// queryContainer asks docker inspect for a container's status, treating
// any non-zero exit as absent. docker inspect gives no separate exit
// code for "no such container" the way getent's 2 does
// (identity/user/user.go's queryUser); this settles for the same answer
// internal/catalog/fs's queryMount does for findmnt's identical lack of
// one.
func queryContainer(ctx context.Context, conn *remoteexec.Conn, name string) (containerState, error) {
	result, err := conn.Run(ctx, remoteexec.QuoteCommand([]string{"docker", "inspect", "--format", "{{.Id}} {{.State.Status}}", name}))
	if err != nil {
		return containerState{}, err
	}
	if result.ExitCode != 0 {
		return containerState{}, nil
	}
	id, status, _ := strings.Cut(strings.TrimSpace(result.Stdout), " ")
	return containerState{exists: true, status: status, id: id}, nil
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
		out[k] = s
	}
	return out, nil
}

// dockerRunArgs builds a fresh container's full docker run invocation.
// -d always: this platform has no interactive session to attach one to.
func dockerRunArgs(name, image string, command, ports, volumes []string, env map[string]string, restartPolicy string) []string {
	args := []string{"docker", "run", "-d", "--name", name}
	for _, p := range ports {
		args = append(args, "-p", p)
	}
	for _, v := range volumes {
		args = append(args, "-v", v)
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}
	if restartPolicy != "" {
		args = append(args, "--restart", restartPolicy)
	}
	args = append(args, image)
	return append(args, command...)
}

// runDockerCmd runs one docker invocation and treats a non-zero exit as
// a real error.
func runDockerCmd(ctx context.Context, conn *remoteexec.Conn, argv []string) error {
	command := remoteexec.QuoteCommand(argv)
	result, err := conn.Run(ctx, command)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("%s exited %d: %s", command, result.ExitCode, failureDetail(result))
	}
	return nil
}

// failureDetail picks the stream an operator should read after a
// non-zero exit. Mirrors internal/catalog/fs's own copy; docker almost
// always explains itself on stderr.
func failureDetail(result remoteexec.Result) string {
	if detail := strings.TrimSpace(result.Stderr); detail != "" {
		return detail
	}
	if detail := strings.TrimSpace(result.Stdout); detail != "" {
		return detail
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
