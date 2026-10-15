package docker

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "container.docker.stop",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameDocker},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true, Site: collection.SiteTarget, Device: collection.DeviceRequired},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			// A check reads the container with docker inspect and changes nothing.
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A stop that stopped a running container emits a container.docker.run naming it, from the " +
					"image the container was created from, with start: true, since run starts an existing " +
					"stopped container. A stop that found nothing running emits nothing.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "container.docker.run", Record: []string{"name", "image", "start"}},
				},
			},
			Doc: stopDoc(),
		},
		Invoke: Stop,
		Check:  CheckStop,
	})
}

func stopDoc() collection.Doc {
	return collection.Doc{
		Summary: "Stops a running Docker container on the target.",
		Description: "Makes sure a container named name is not running, stopping it if it is. Container " +
			"state is read from docker inspect before anything is sent, so a container already stopped, or " +
			"absent entirely, reports no change and no command reaches the device." +
			" It acts only on the Docker engine onboarding pinned for the device, and refuses any other engine the login reaches." +
			" It stops the container by the id it inspected, and records container.docker.run, which starts it again, as its undo.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The container to stop."},
			{Name: paramID, Type: "string", Description: "When set, the container named name must be this one, by its full id, or the task is refused and nothing is stopped. The undo of container.docker.run's start sets it."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The container this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What docker inspect reported about the container before this task and after it (exists, status)."},
		},
		Examples: []collection.Example{
			{
				Name:        "Stop a container",
				RunbookYAML: "- name: Stop web\n  container.docker.stop:\n    name: web\n",
			},
		},
		SeeAlso: []string{"container.docker.run", "container.docker.remove"},
	}
}

// Stop implements "container.docker.stop".
func Stop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return stop(ctx, rc, device, params, collection.ModeExecute)
}

// CheckStop is "container.docker.stop"'s check: it reads the container and says whether
// Stop would stop it, running no docker command that changes anything.
func CheckStop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return stop(ctx, rc, device, params, collection.ModeCheck)
}

// stop is Stop's and CheckStop's one body; mode says which.
func stop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "container.docker.stop"

	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := checkName(name); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	if err := checkEngine(ctx, conn, device); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	before, err := queryContainer(ctx, conn, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	// The identity guard, as container.docker.remove's: a name can be
	// reused, an id cannot. run's undo of a start sets it.
	if want := sdk.StringParam(params, paramID); want != "" && before.exists && want != before.id {
		return collection.Result{}, fmt.Errorf("%s: container %s is %s, not %s; nothing was stopped", fqcn, name, before.id, want)
	}

	changed := before.exists && before.status == "running"
	if mode == collection.ModeCheck {
		return predictState(rc, fqcn, name, before, changed, containerState{exists: true, status: "exited"}.Map())
	}
	after := before
	if changed {
		// Stopped by the id just inspected, so the container the guard and
		// the undo name is the one stopped.
		if !containerIDPattern.MatchString(before.id) {
			return collection.Result{}, fmt.Errorf("%s: docker inspect gave no usable id to stop container %s by", fqcn, name)
		}
		if err := runDockerCmd(ctx, conn, []string{"docker", "stop", "--", before.id}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		// The engine drops a failed task's undo, so once the container is
		// stopped, a failed read back is a warning and never a failure
		// (FAILURE_PATTERNS 452).
		if after, err = queryContainer(ctx, conn, name); err != nil {
			after = containerState{exists: true, id: before.id, image: before.image}
			if err := sdk.RecordWarnings(rc, []string{fmt.Sprintf("stopped container %s but could not read it back, so its status is not reported: %v", name, err)}); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
	}

	if err := recordState(rc, name, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if changed {
		// run starts a stopped container, so it is this stop's undo: the same
		// name, from the image the container was created from, started.
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:        "container.docker.run",
			Params:      map[string]any{paramName: name, paramImage: before.image, paramStart: true},
			Description: fmt.Sprintf("Start %s again, which this task stopped.", name),
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}
