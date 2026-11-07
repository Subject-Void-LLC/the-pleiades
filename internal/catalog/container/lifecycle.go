// container.start, stop, restart and remove: a container's state changed
// by name, each through one body its check shares, each refusing a
// container whose ID is not the one the task recorded.
package container

import (
	"context"
	"fmt"
	"time"

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
	// removesAutoRemove says the change removes a container the engine
	// removes when it stops (auto-remove), so it records no undo there.
	removesAutoRemove bool
}

// registerAction registers one state change. Its undo is the opposite
// change, by ID; a change with none (a restart) says why.
func registerAction(a action) {
	rev := collection.Reversibility{
		Reversible: true,
		Notes:      "A run that changed the container's state records the opposite change, by ID; one that found it already so records nothing.",
		Inverses:   []sdk.InverseSpec{{FQCN: a.inverse, Record: []string{paramName, paramID, containerengine.ParamEngine}}},
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
	// From here the change is on the device, so nothing that only reads
	// fails the task: the engine drops a failed task's undo before the
	// journal sees it, which would leave a change rollback cannot undo
	// (FAILURE_PATTERNS 563). Such a read is a warning instead.
	var warnings []string
	removed := a.removesAutoRemove && autoRemoves(c)
	switch {
	case removed:
		// An auto-remove container is gone once it stops, so starting it
		// again cannot be its undo.
		warnings = append(warnings, fmt.Sprintf("container %s removes itself when it stops (auto-remove), so this stop has no undo", name))
	case a.inverse != "":
		if err := containerengine.RecordUndo(rc, s, sdk.Inverse{
			FQCN:        a.inverse,
			Params:      map[string]any{paramName: name, paramID: c.ID},
			Description: "Put the container back in the state this task found it in.",
		}); err != nil {
			return collection.Result{Changed: true}, err
		}
	}
	gone, err := settle(ctx, s, c.ID)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("%s changed container %s, but reading it back afterwards failed, so a container list made at once may not show the change yet: %v", a.fqcn, name, err))
	}
	if err := sdk.RecordWarnings(rc, warnings); err != nil {
		return collection.Result{Changed: true}, err
	}
	if gone {
		// Nothing is left to have a state.
		return collection.Result{Changed: true}, nil
	}
	return collection.Result{Changed: true}, rc.SetStat(statState, a.after)
}

// settleTimeout bounds settle's read, which runs even once the task's own
// context has ended, so the read cannot hold a run that was stopped.
const settleTimeout = 10 * time.Second

// settle waits until the engine's container list shows the change just
// made, by inspecting the container once more, and reports whether the
// container is gone. Docker before 28.3.0 (moby issue 50133) answers a
// stop while its exit handler still holds the container's lock and has not
// yet written the new state to the copy a list reads, so a list right
// after the stop can still say "running"; inspect takes that lock, so once
// it answers the list is written too. On an engine with the fix, and on
// Podman, it is one more read. A container gone by then (removed when it
// stopped) is settled, since inspect reads the engine's 404 as no
// container and no error; the state read is not compared, since another
// client may have changed it since.
//
// The read ignores the task's cancellation, bounded by settleTimeout
// instead: a run stopped just after the change still gets a list that
// agrees with it.
func settle(ctx context.Context, s *containerengine.Session, id string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	_, found, err := inspect(ctx, s, id)
	return err == nil && !found, err
}

// autoRemoves says whether the engine removes c when it stops (docker run
// --rm).
func autoRemoves(c moby.InspectResponse) bool {
	return c.HostConfig != nil && c.HostConfig.AutoRemove
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
