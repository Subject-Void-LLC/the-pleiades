// container.start, stop, restart and remove: a container's state changed
// by name, each through one body its check shares, each refusing a
// container whose ID is not the one the task recorded.
package container

import (
	"context"
	"fmt"

	moby "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const (
	paramTimeout = "timeout"
	paramVolumes = "remove_volumes"

	// defaultStopSeconds is how long a stop waits before killing, as
	// docker stop does.
	defaultStopSeconds = 10
	maxStopSeconds     = 3600
)

// action is one state change: whether it applies to a container in the
// state it is in, and the call that makes it.
type action struct {
	fqcn string
	ops  []containerengine.Op
	// decide says whether the container, in its state, needs the change,
	// or why it cannot have it.
	decide func(state string, params map[string]any) (bool, error)
	// do makes the change.
	do func(ctx context.Context, s *containerengine.Session, c moby.InspectResponse, params map[string]any) error
	// after is the state the container is in once changed.
	after string
	// inverse is the undo, or "" for none.
	inverse string
}

// actions are the four state changes.
var actions = map[string]action{
	"container.start": {
		fqcn: "container.start",
		ops:  []containerengine.Op{containerengine.OpContainerInspect, containerengine.OpContainerStart},
		decide: func(state string, _ map[string]any) (bool, error) {
			switch state {
			case "running", "restarting":
				return false, nil
			case "created", "exited":
				return true, nil
			case "paused":
				return false, fmt.Errorf("it is paused; unpause it rather than start it")
			}
			return false, fmt.Errorf("it is %s, which cannot be started", state)
		},
		do: func(ctx context.Context, s *containerengine.Session, c moby.InspectResponse, _ map[string]any) error {
			_, err := s.API.ContainerStart(ctx, c.ID, client.ContainerStartOptions{})
			return err
		},
		after:   "running",
		inverse: "container.stop",
	},
	"container.stop": {
		fqcn: "container.stop",
		ops:  []containerengine.Op{containerengine.OpContainerInspect, containerengine.OpContainerStop},
		decide: func(state string, _ map[string]any) (bool, error) {
			switch state {
			case "running", "restarting", "paused":
				return true, nil
			}
			return false, nil
		},
		do: func(ctx context.Context, s *containerengine.Session, c moby.InspectResponse, params map[string]any) error {
			secs, err := stopSeconds(params)
			if err != nil {
				return err
			}
			_, err = s.API.ContainerStop(ctx, c.ID, client.ContainerStopOptions{Timeout: &secs})
			return err
		},
		after:   "exited",
		inverse: "container.start",
	},
	"container.restart": {
		fqcn: "container.restart",
		ops:  []containerengine.Op{containerengine.OpContainerInspect, containerengine.OpContainerRestart},
		decide: func(state string, _ map[string]any) (bool, error) {
			switch state {
			case "running", "restarting", "created", "exited":
				return true, nil
			case "paused":
				return false, fmt.Errorf("it is paused; unpause it rather than restart it")
			}
			return false, fmt.Errorf("it is %s, which cannot be restarted", state)
		},
		do: func(ctx context.Context, s *containerengine.Session, c moby.InspectResponse, params map[string]any) error {
			secs, err := stopSeconds(params)
			if err != nil {
				return err
			}
			_, err = s.API.ContainerRestart(ctx, c.ID, client.ContainerRestartOptions{Timeout: &secs})
			return err
		},
		after: "running",
	},
}

func init() {
	for _, name := range []string{"container.start", "container.stop", "container.restart"} {
		a := actions[name]
		rev := collection.Reversibility{
			Reversible: true,
			Notes:      "A run that changed the container's state records the opposite change, by ID; one that found it already so records nothing.",
			Inverses:   []sdk.InverseSpec{{FQCN: a.inverse, Record: []string{paramName, paramID}}},
		}
		if a.inverse == "" {
			rev = collection.Reversibility{Reversible: false, Notes: "A restart has no undo: the container runs before and after it, and the processes it ended cannot be brought back."}
		}
		collection.MustRegister(collection.Descriptor{
			Name:     a.fqcn,
			Manifest: manifest(rev, lifecycleDoc(a.fqcn)),
			Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
				return change(ctx, rc, d, p, collection.ModeExecute, a)
			},
			Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
				return change(ctx, rc, d, p, collection.ModeCheck, a)
			},
		})
	}
	collection.MustRegister(collection.Descriptor{
		Name: "container.remove",
		Manifest: manifest(collection.Reversibility{
			Reversible: false,
			Notes:      "A removed container's writable layer, and any anonymous volume removed with it, is gone; container.run makes a new one from its declaration.",
		}, lifecycleDoc("container.remove")),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return remove(ctx, rc, d, p, collection.ModeExecute)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return remove(ctx, rc, d, p, collection.ModeCheck)
		},
	})
}

// change is start's, stop's and restart's one body.
func change(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode, a action) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", a.fqcn, err)
	}
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return fail(err)
	}
	if err := containerengine.ValidName("container", name); err != nil {
		return fail(err)
	}
	if _, err := stopSeconds(params); err != nil {
		return fail(err)
	}
	s, err := open(ctx, rc, device, params, a.fqcn, a.ops...)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	c, exists, err := inspect(ctx, s, name)
	if err != nil {
		return fail(err)
	}
	if !exists {
		return fail(fmt.Errorf("there is no container named %s", name))
	}
	if err := guardID(params, c); err != nil {
		return fail(err)
	}
	if err := recordStats(rc, map[string]any{statName: name, statID: c.ID, statState: stateOf(c)}); err != nil {
		return collection.Result{}, err
	}
	needed, err := a.decide(stateOf(c), params)
	if err != nil {
		return fail(fmt.Errorf("container %s: %w", name, err))
	}
	if !needed {
		return collection.Result{}, nil
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, rc.SetStat(statState, a.after)
	}
	if err := a.do(ctx, s, c, params); err != nil {
		return fail(fmt.Errorf("container %s: %w", name, err))
	}
	if a.inverse != "" {
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:        a.inverse,
			Params:      map[string]any{paramName: name, paramID: c.ID},
			Description: "Put the container back in the state this task found it in.",
		}); err != nil {
			return collection.Result{}, err
		}
	}
	return collection.Result{Changed: true}, rc.SetStat(statState, a.after)
}

// remove is container.remove's body: an absent container is no change; a
// running one is refused unless force is set.
func remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "container.remove"
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return fail(err)
	}
	if err := containerengine.ValidName("container", name); err != nil {
		return fail(err)
	}
	force, err := sdk.BoolParamOr(params, paramForce, false)
	if err != nil {
		return fail(err)
	}
	volumes, err := sdk.BoolParamOr(params, paramVolumes, false)
	if err != nil {
		return fail(err)
	}
	s, err := open(ctx, rc, device, params, fqcn, containerengine.OpContainerInspect, containerengine.OpContainerRemove)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	c, exists, err := inspect(ctx, s, name)
	if err != nil {
		return fail(err)
	}
	if err := rc.SetStat(statName, name); err != nil {
		return collection.Result{}, err
	}
	if !exists {
		return collection.Result{}, nil
	}
	if err := guardID(params, c); err != nil {
		return fail(err)
	}
	if err := recordStats(rc, map[string]any{statID: c.ID, statState: stateOf(c)}); err != nil {
		return collection.Result{}, err
	}
	if state := stateOf(c); (state == "running" || state == "restarting" || state == "paused") && !force {
		return fail(fmt.Errorf("container %s is %s; stop it first, or set %s", name, state, paramForce))
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, rc.SetStat(statState, "removed")
	}
	if _, err := s.API.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: force, RemoveVolumes: volumes}); err != nil {
		return fail(fmt.Errorf("container %s: %w", name, err))
	}
	return collection.Result{Changed: true}, rc.SetStat(statState, "removed")
}

// stopSeconds reads the stop timeout, in whole seconds.
func stopSeconds(params map[string]any) (int, error) {
	n, set, err := sdk.IntParam(params, paramTimeout)
	if err != nil {
		return 0, err
	}
	if !set {
		return defaultStopSeconds, nil
	}
	if n < 0 || n > maxStopSeconds {
		return 0, fmt.Errorf("%s: %d is not a number of seconds from 0 to %d", paramTimeout, n, maxStopSeconds)
	}
	return n, nil
}
