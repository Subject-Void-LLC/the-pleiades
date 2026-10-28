// container.run with wait: a container run to its exit, as a task runs a
// command, its exit code and output returned and the container removed.
package container

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const (
	paramWait        = "wait"
	paramRemove      = "remove"
	paramWaitTimeout = "wait_timeout"
	paramAcceptExit  = "accept_exit_codes"

	statTimedOut = "timed_out"

	defaultWaitSeconds = 3600
	maxWaitSeconds     = 86400
	// maxOneShotOutput bounds what a one-shot container's output may be,
	// together, refused past it, as exec.command's is.
	maxOneShotOutput = 1 << 20
)

// oneShot is how container.run runs a container to its exit.
type oneShot struct {
	wait    bool
	remove  bool
	timeout time.Duration
	accept  []int
}

// parseOneShot reads wait, remove, wait_timeout and accept_exit_codes.
func parseOneShot(params map[string]any) (oneShot, error) {
	var o oneShot
	var err error
	if o.wait, err = sdk.BoolParamOr(params, paramWait, false); err != nil {
		return o, err
	}
	if o.remove, err = sdk.BoolParamOr(params, paramRemove, true); err != nil {
		return o, err
	}
	secs, set, err := sdk.IntParam(params, paramWaitTimeout)
	if err != nil {
		return o, err
	}
	if !set {
		secs = defaultWaitSeconds
	}
	if secs < 1 || secs > maxWaitSeconds {
		return o, fmt.Errorf("%s: %d is not a number of seconds from 1 to %d", paramWaitTimeout, secs, maxWaitSeconds)
	}
	o.timeout = time.Duration(secs) * time.Second
	o.accept = []int{0}
	if raw, ok := params[paramAcceptExit]; ok && raw != nil {
		items, ok := raw.([]any)
		if !ok {
			return o, fmt.Errorf("%s is a list of exit codes", paramAcceptExit)
		}
		o.accept = nil
		for _, item := range items {
			// IntParam reads the one value the way every tier delivers it.
			n, _, err := sdk.IntParam(map[string]any{paramAcceptExit: item}, paramAcceptExit)
			if err != nil || n < 0 || n > 255 {
				return o, fmt.Errorf("%s: %v is not an exit code from 0 to 255", paramAcceptExit, item)
			}
			o.accept = append(o.accept, n)
		}
	}
	if !o.wait && (params[paramWaitTimeout] != nil || params[paramAcceptExit] != nil) {
		return o, fmt.Errorf("%s and %s apply only with %s", paramWaitTimeout, paramAcceptExit, paramWait)
	}
	return o, nil
}

// oneShotCheck says which container.run calls can be checked: one that
// runs a container to its exit runs its command, whose effect is known
// only once it has run.
func oneShotCheck(params map[string]any) error {
	if wait, _ := sdk.BoolParamOr(params, paramWait, false); wait {
		return collection.CannotCheck("a container run to its exit runs its command, and what that command changes is known only once it has run")
	}
	return nil
}

// runToExit creates the container, runs it to its exit and returns how it
// ended. It is removed afterwards when remove is set, and whenever the
// task fails, remove or not: the engine drops a failed task's undo
// (FAILURE_PATTERNS 452), so a container kept then would be named by no
// undo and refuse every rerun of the task by its name. An exit code the
// task did not accept fails it, after its output is recorded.
func runToExit(ctx context.Context, rc sdk.RunbookContext, s *containerengine.Session, spec containerengine.Spec, shot oneShot) (collection.Result, error) {
	cfg, host, net, err := spec.Create()
	if err != nil {
		return collection.Result{}, err
	}
	cfg.AttachStdout, cfg.AttachStderr = true, true
	created, err := s.API.ContainerCreate(ctx, client.ContainerCreateOptions{Name: spec.Name, Config: cfg, HostConfig: host, NetworkingConfig: net})
	if err != nil {
		return collection.Result{}, fmt.Errorf("creating container %s: %w", spec.Name, err)
	}
	if !containerID.MatchString(created.ID) {
		return collection.Result{}, fmt.Errorf("the engine answered an unusable ID for the container it created")
	}
	failed := true
	defer func() {
		if shot.remove || failed {
			removeQuietly(s, created.ID)
		}
	}()
	exited, err := s.RunToExit(ctx, created.ID, cfg.Tty, shot.timeout, maxOneShotOutput)
	if err != nil {
		return collection.Result{}, fmt.Errorf("container %s: %w; it was removed", spec.Name, err)
	}
	accepted := !exited.TimedOut && slices.Contains(shot.accept, exited.ExitCode)
	state := "removed"
	if !shot.remove && accepted {
		state = "exited"
		if err := containerengine.RecordUndo(rc, s, sdk.Inverse{
			FQCN:        "container.remove",
			Params:      map[string]any{paramName: spec.Name, paramID: created.ID, paramForce: true},
			Description: "Remove the container this task ran.",
		}); err != nil {
			return collection.Result{}, err
		}
	}
	if err := recordStats(rc, map[string]any{
		statID: created.ID, statState: state, statExitCode: exited.ExitCode, statTimedOut: exited.TimedOut,
		statStdout: exited.Stdout, statStderr: exited.Stderr,
	}); err != nil {
		return collection.Result{}, err
	}
	if exited.TimedOut {
		return collection.Result{Changed: true}, fmt.Errorf("container %s ran past %s and was killed and removed", spec.Name, shot.timeout)
	}
	if !accepted {
		return collection.Result{Changed: true}, fmt.Errorf("container %s exited %d and was removed: %s", spec.Name, exited.ExitCode, farside.Output(firstNonEmpty(exited.Stderr, exited.Stdout)))
	}
	failed = false
	return collection.Result{Changed: true}, nil
}
