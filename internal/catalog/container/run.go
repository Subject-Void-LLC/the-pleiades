// container.run and container.create: make a container exist as the task
// declares it, started or not, through one body their checks share.
package container

import (
	"context"
	"fmt"
	"strings"
	"time"

	moby "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const (
	fqcnRun    = "container.run"
	fqcnCreate = "container.create"
)

// convergeOps are the operations run and create send.
var convergeOps = append(append([]containerengine.Op{
	containerengine.OpContainerInspect, containerengine.OpContainerCreate,
	containerengine.OpContainerStart, containerengine.OpContainerRemove,
}, containerengine.EnsureImageOps...), containerengine.RunToExitOps...)

// cleanupGrace bounds removing a container a failed run created, on a
// context of its own so a run that ended still cleans up.
const cleanupGrace = 30 * time.Second

func init() {
	inverses := []sdk.InverseSpec{
		{FQCN: "container.remove", Record: []string{paramName, paramID, paramForce}},
		{FQCN: "container.stop", Record: []string{paramName, paramID}},
	}
	collection.MustRegister(collection.Descriptor{
		Name: fqcnRun,
		Manifest: containerengine.MethodManifest(collection.Reversibility{
			Reversible: true,
			Notes:      "A run that created the container records its removal, by ID; a run that started an existing stopped one records stopping it, by ID; a run that found it running as declared records nothing.",
			Inverses:   inverses,
		}, runDoc()),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return converge(ctx, rc, d, p, collection.ModeExecute, fqcnRun, true)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			if err := oneShotCheck(p); err != nil {
				return collection.Result{}, err
			}
			return converge(ctx, rc, d, p, collection.ModeCheck, fqcnRun, true)
		},
		CheckCall: oneShotCheck,
	})
	collection.MustRegister(collection.Descriptor{
		Name: fqcnCreate,
		Manifest: containerengine.MethodManifest(collection.Reversibility{
			Reversible: true,
			Notes:      "A run that created the container records its removal, by ID; one that found it as declared records nothing.",
			Inverses:   inverses[:1],
		}, createDoc()),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return converge(ctx, rc, d, p, collection.ModeExecute, fqcnCreate, false)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return converge(ctx, rc, d, p, collection.ModeCheck, fqcnCreate, false)
		},
	})
}

// converge is run's and create's one body: the container named exists as
// declared, started when start is set. A container that differs from the
// declaration is refused, naming container.recreate; nothing is ever
// replaced here.
func converge(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode, fqcn string, start bool) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	spec, err := containerengine.ParseSpec(params)
	if err != nil {
		return fail(err)
	}
	if spec.Image == "" {
		return fail(fmt.Errorf("image is required"))
	}
	shot, err := parseOneShot(params)
	if err != nil {
		return fail(err)
	}
	if shot.wait && !start {
		return fail(fmt.Errorf("%s runs nothing; %s with %s runs a container to its exit", fqcnCreate, fqcnRun, paramWait))
	}
	if shot.wait && spec.Restart != "no" {
		return fail(fmt.Errorf("a container run to its exit has no restart policy; %s is %s", "restart_policy", spec.Restart))
	}
	auth, err := containerengine.RegistryAuth(rc, spec.Image)
	if err != nil {
		return fail(err)
	}
	s, err := open(ctx, rc, device, params, fqcn, convergeOps...)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()

	live, exists, err := inspect(ctx, s, spec.Name)
	if err != nil {
		return fail(err)
	}
	img, err := presentImage(ctx, s, spec, auth, mode)
	if err != nil {
		return fail(err)
	}
	spec.ImageID = img.ID
	if err := recordStats(rc, map[string]any{
		statName: spec.Name, statImage: img.Pinned, statDigest: img.Digest, statImageID: img.ID,
		statEnv: spec.EnvNames(), statPulled: img.Pulled,
	}); err != nil {
		return collection.Result{}, err
	}

	if shot.wait {
		if exists {
			return fail(fmt.Errorf("a container named %s exists, and a run to its exit makes a fresh one; container.remove it first, or name another", spec.Name))
		}
		return runToExit(ctx, rc, s, spec, shot)
	}
	if exists {
		return existing(ctx, rc, s, spec, live, img, mode, start, true)
	}
	if mode == collection.ModeCheck {
		state := "created"
		if start {
			state = "running"
		}
		return collection.Result{Changed: true}, recordStats(rc, map[string]any{statState: state})
	}
	return createNew(ctx, rc, s, spec, start, true)
}

// existing settles a container that is already there: one that differs is
// refused, a stopped one is started when start is set (recording stopping
// it as the undo when record is set), and anything else is left as it is.
func existing(ctx context.Context, rc sdk.RunbookContext, s *containerengine.Session, spec containerengine.Spec, live moby.InspectResponse, img containerengine.PresentImage, mode collection.Mode, start, record bool) (collection.Result, error) {
	if err := recordStats(rc, map[string]any{statID: live.ID, statState: stateOf(live)}); err != nil {
		return collection.Result{}, err
	}
	var diffs []string
	if img.ID == "" {
		diffs = []string{fmt.Sprintf("image: %s is not on the engine, so the container cannot be running it", img.Pinned)}
	} else {
		diffs = spec.Differences(live)
	}
	if len(diffs) > 0 {
		return collection.Result{}, fmt.Errorf("container %s differs from the task, and is not replaced here; container.recreate replaces it:\n  %s", spec.Name, strings.Join(diffs, "\n  "))
	}
	state := stateOf(live)
	switch state {
	case "running", "restarting":
		return collection.Result{}, nil
	case "created", "exited":
		if !start {
			return collection.Result{}, nil
		}
	case "paused":
		return collection.Result{}, fmt.Errorf("container %s is paused; unpause it before running it", spec.Name)
	default:
		return collection.Result{}, fmt.Errorf("container %s is %s, which cannot be started; container.remove it", spec.Name, state)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, rc.SetStat(statState, "running")
	}
	if _, err := s.API.ContainerStart(ctx, live.ID, client.ContainerStartOptions{}); err != nil {
		return collection.Result{}, fmt.Errorf("starting container %s: %w", spec.Name, err)
	}
	if record {
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:        "container.stop",
			Params:      map[string]any{paramName: spec.Name, paramID: live.ID},
			Description: "Stop the container this task started.",
		}); err != nil {
			return collection.Result{}, err
		}
	}
	return collection.Result{Changed: true}, rc.SetStat(statState, "running")
}

// createNew creates the container, and starts it when start is set,
// recording its removal as the undo when record is set. A
// create whose answer was lost is found by name rather than sent twice; a
// start that fails removes what the create made, since the engine drops a
// failed task's undo (FAILURE_PATTERNS 452).
func createNew(ctx context.Context, rc sdk.RunbookContext, s *containerengine.Session, spec containerengine.Spec, start, record bool) (collection.Result, error) {
	cfg, host, net, err := spec.Create()
	if err != nil {
		return collection.Result{}, err
	}
	created, err := s.API.ContainerCreate(ctx, client.ContainerCreateOptions{Name: spec.Name, Config: cfg, HostConfig: host, NetworkingConfig: net})
	if err != nil {
		if live, exists, inspectErr := inspect(context.WithoutCancel(ctx), s, spec.Name); inspectErr == nil && exists && live.Config != nil && live.Config.Labels[containerengine.LabelManagedBy] == spec.Image {
			removeQuietly(s, live.ID)
			return collection.Result{}, fmt.Errorf("creating container %s: %w; the engine had made it anyway, and it was removed", spec.Name, err)
		}
		return collection.Result{}, fmt.Errorf("creating container %s: %w", spec.Name, err)
	}
	if !containerID.MatchString(created.ID) {
		return collection.Result{}, fmt.Errorf("the engine answered an unusable ID for the container it created")
	}
	state := "created"
	if start {
		if _, err := s.API.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
			removeQuietly(s, created.ID)
			return collection.Result{}, fmt.Errorf("starting container %s: %w; the container it created was removed", spec.Name, err)
		}
		state = "running"
	}
	if record {
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:        "container.remove",
			Params:      map[string]any{paramName: spec.Name, paramID: created.ID, paramForce: true},
			Description: "Remove the container this task created.",
		}); err != nil {
			return collection.Result{}, err
		}
	}
	return collection.Result{Changed: true}, recordStats(rc, map[string]any{statID: created.ID, statState: state})
}

// removeQuietly removes a container a failed run made, forced, on a
// context of its own.
func removeQuietly(s *containerengine.Session, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupGrace)
	defer cancel()
	_, _ = s.API.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true})
}
