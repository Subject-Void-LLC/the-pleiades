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
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			// A check reads the container with docker inspect and changes nothing.
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "This catalog declares no container.docker.start, and recording container.docker.run " +
					"as this method's inverse would be dishonest: run's own idempotency means it would just " +
					"no-op against the existing name rather than actually restart the container this stopped. " +
					"The container's own state is still recorded under diff, so a rollback reaching this task " +
					"can see it was running before and decide for itself, but this method emits no instruction " +
					"because none would be true.",
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
			"absent entirely, reports no change and no command reaches the device.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The container to stop."},
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

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	before, err := queryContainer(ctx, conn, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	changed := before.exists && before.status == "running"
	if mode == collection.ModeCheck {
		return predictState(rc, fqcn, name, before, changed, containerState{exists: true, status: "exited"}.Map())
	}
	after := before
	if changed {
		if err := runDockerCmd(ctx, conn, []string{"docker", "stop", name}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		if after, err = queryContainer(ctx, conn, name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	if err := recordState(rc, name, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	return collection.Result{Changed: changed}, nil
}
