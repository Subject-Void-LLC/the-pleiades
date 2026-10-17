// container.start, stop, restart and remove: a container's state changed
// by name, each through one body its check shares, each refusing a
// container whose ID is not the one the task recorded.
package container

import (
	"context"
	"fmt"

	moby "github.com/moby/moby/api/types/container"

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

// registerAction registers one state change. Its undo is the opposite
// change, by ID; a change with none (a restart) says why.
func registerAction(a action) {
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
		Manifest: containerengine.MethodManifest(rev, lifecycleDoc(a.fqcn)),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return change(ctx, rc, d, p, collection.ModeExecute, a)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return change(ctx, rc, d, p, collection.ModeCheck, a)
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
