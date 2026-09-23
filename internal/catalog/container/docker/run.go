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
		Name: "container.docker.run",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameDocker},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			// A check reads the container with docker inspect and changes nothing.
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that created an absent container emits a container.docker.remove naming it, with " +
					"force: true since a freshly started container is very likely still running. A run that " +
					"found a container already present under that name emits nothing, the same as every other " +
					"converged run in this catalog, even though this method does not compare that existing " +
					"container's configuration against what was requested.",
			},
			Doc: runDoc(),
		},
		Invoke: Run,
		Check:  CheckRun,
	})
}

func runDoc() collection.Doc {
	return collection.Doc{
		Summary: "Runs a Docker container on the target.",
		Description: "Makes sure a container named name is running, starting one from image if no container " +
			"by that name exists. This is a narrow slice of community.docker.docker_container: idempotency " +
			"here is existence of the NAME only, not a comparison of the running container's configuration " +
			"against what this task asked for. A container already present under name is left exactly as it " +
			"is, regardless of whether its image, ports, volumes, env or restart policy match; this method " +
			"never recreates. Always runs detached (-d), since this platform has no interactive session to " +
			"attach one to.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The container name to create or leave alone."},
			{Name: paramImage, Type: "string", Required: true, Description: "The image to run. Ignored when a container already exists under name."},
			{Name: paramCommand, Type: "list", Description: "The command and its arguments, as separate list elements rather than one shell string. Overrides the image's own default command."},
			{Name: paramPorts, Type: "list", Description: "Port mappings, each \"host:container\" (docker run's own -p syntax)."},
			{Name: paramVolumes, Type: "list", Description: "Volume mappings, each \"host:container\" (docker run's own -v syntax)."},
			{Name: paramEnv, Type: "dict", Description: "Environment variables to set in the container, as a map of name to value."},
			{Name: paramRestartPolicy, Type: "string", Description: "The restart policy (docker run's own --restart value, e.g. unless-stopped)."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The container this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What docker inspect reported about the container before this task and after it (exists, status). Recorded even on a run that changed nothing."},
		},
		Examples: []collection.Example{
			{
				Name:        "Run a container",
				RunbookYAML: "- name: Run nginx\n  container.docker.run:\n    name: web\n    image: nginx:1.27\n    ports:\n      - \"8080:80\"\n",
			},
		},
		SeeAlso: []string{"container.docker.stop", "container.docker.remove"},
	}
}

// Run implements "container.docker.run".
func Run(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return run(ctx, rc, device, params, collection.ModeExecute)
}

// CheckRun is "container.docker.run"'s check: it reads the container and says whether
// Run would create and start it, running no docker command that changes anything.
func CheckRun(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return run(ctx, rc, device, params, collection.ModeCheck)
}

// run is Run's and CheckRun's one body; mode says which.
func run(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "container.docker.run"

	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	image, err := sdk.RequiredStringParam(params, paramImage)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	command, _, err := sdk.StringSlice(params, paramCommand)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, paramCommand, err)
	}
	ports, _, err := sdk.StringSlice(params, paramPorts)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, paramPorts, err)
	}
	volumes, _, err := sdk.StringSlice(params, paramVolumes)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, paramVolumes, err)
	}
	env, err := envParam(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	restartPolicy := sdk.StringParam(params, paramRestartPolicy)

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	before, err := queryContainer(ctx, conn, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if mode == collection.ModeCheck {
		// A new container's status is left out: one whose command exits at
		// once is exited by the time a real run reads it back.
		return predictState(rc, fqcn, name, before, !before.exists, map[string]any{"exists": true})
	}

	changed := false
	after := before
	if !before.exists {
		if err := runDockerCmd(ctx, conn, dockerRunArgs(name, image, command, ports, volumes, env, restartPolicy)); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		changed = true
		if after, err = queryContainer(ctx, conn, name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	if err := recordState(rc, name, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if changed {
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:   "container.docker.remove",
			Params: map[string]any{paramName: name, paramForce: true},
			Description: fmt.Sprintf("Remove %s, which this task created. Its volumes' contents on the host "+
				"are not touched by that removal, and are not restorable by this inverse either.", name),
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}
