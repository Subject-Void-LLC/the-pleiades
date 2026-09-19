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
		Name: "container.docker.remove",
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
				Notes: "docker inspect exposes a removed container's image and some of its configuration " +
					"before deletion, but reconstructing ports, volumes, env and restart policy from that " +
					"output well enough for a real container.docker.run to recreate it is parsing this pass " +
					"does not take on. A partial inverse that silently dropped that configuration would be " +
					"worse than refusing to record one, the same call pkg.upgrade makes for a package version a " +
					"repository may no longer offer.",
			},
			Doc: removeDoc(),
		},
		Invoke: Remove,
		Check:  CheckRemove,
	})
}

func removeDoc() collection.Doc {
	return collection.Doc{
		Summary: "Removes a Docker container from the target.",
		Description: "Makes sure a container named name does not exist, removing it if present. Container " +
			"state is read from docker inspect before anything is sent, so a container already absent reports " +
			"no change and no command reaches the device. " +
			"A check reads the same state and removes nothing; a container that is not stopped, with force unset, " +
			"makes the call unchecked rather than failed, since docker rm refuses one unless an earlier task in " +
			"the same run stops it.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The container to remove."},
			{Name: paramForce, Type: "bool", Default: "false", Description: "Remove the container even if it is still running (docker rm -f). Left false, removing a running container fails rather than stopping it first."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The container this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What docker inspect reported about the container before this task and after it. After always reports exists: false on a successful run."},
		},
		Examples: []collection.Example{
			{
				Name:        "Remove a stopped container",
				RunbookYAML: "- name: Remove web\n  container.docker.remove:\n    name: web\n",
			},
			{
				Name:        "Force-remove a running container",
				RunbookYAML: "- name: Remove web even if it is still running\n  container.docker.remove:\n    name: web\n    force: true\n",
			},
		},
		SeeAlso: []string{"container.docker.run", "container.docker.stop"},
	}
}

// Remove implements "container.docker.remove".
func Remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return remove(ctx, rc, device, params, collection.ModeExecute)
}

// CheckRemove is "container.docker.remove"'s check: it reads the container and says whether
// Remove would remove it, running no docker command that changes anything.
func CheckRemove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return remove(ctx, rc, device, params, collection.ModeCheck)
}

// remove is Remove's and CheckRemove's one body; mode says which.
func remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "container.docker.remove"

	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	force, err := sdk.BoolParamOr(params, paramForce, false)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, paramForce, err)
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

	changed := before.exists
	if mode == collection.ModeCheck {
		if changed && !force && before.status != "exited" && before.status != "created" && before.status != "dead" {
			return collection.Result{}, collection.CannotCheck(fmt.Sprintf("container %s is %s, and docker rm without force "+
				"refuses one that is not stopped unless an earlier task stops it, which a check cannot tell", name, before.status))
		}
		return predictState(rc, fqcn, name, before, changed, containerState{}.Map())
	}
	after := before
	if changed {
		args := []string{"docker", "rm"}
		if force {
			args = append(args, "-f")
		}
		if err := runDockerCmd(ctx, conn, append(args, name)); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		after = containerState{}
	}

	if err := recordState(rc, name, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	return collection.Result{Changed: changed}, nil
}
