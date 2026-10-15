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
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true, Site: collection.SiteTarget, Device: collection.DeviceRequired},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			// A check reads the container with docker inspect and changes nothing.
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that created an absent container emits a container.docker.remove naming it, with " +
					"force: true since a freshly started container is very likely still running. A run that " +
					"started an existing stopped container emits a container.docker.stop naming it by id. A run " +
					"that found the container already running emits nothing, the same as every other converged " +
					"run in this catalog, even though this method does not compare that existing container's " +
					"configuration against what was requested.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "container.docker.remove", Record: []string{"name", "force", "id"}},
					{FQCN: "container.docker.stop", Record: []string{"name", "id"}},
				},
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
		Description: "Makes sure a container named name exists, creating and starting one from image if no container " +
			"by that name exists. This is a narrow slice of community.docker.docker_container: idempotency " +
			"here is existence of the NAME only, not a comparison of the running container's configuration " +
			"against what this task asked for. " + "A container already present under name is started when it is stopped (exited, or created and never started) unless start is false, and is otherwise left as it is, regardless of whether its image, ports, volumes, env or restart policy match; this method never recreates. A paused, dead or removing container is refused, naming what to do instead." +
			" Always runs detached (-d), since this platform has no interactive session to " +
			"attach one to." +
			" It acts only on the Docker engine onboarding pinned for the device, and refuses any other engine the login reaches." +
			" Secure unless the task says otherwise: the container joins no network (network: none), mounts each volume read-only unless the entry's options say rw, and starts from the image's ID, pulled first when the engine has none of it, so a tag that moves cannot change what runs.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The container name to create or leave alone."},
			{Name: paramImage, Type: "string", Required: true, Description: "The image to run. It is resolved to the image's ID (pulled first when the engine holds no image by this reference) and the container starts from that ID. Ignored when a container already exists under name."},
			{Name: paramCommand, Type: "list", Description: "The command and its arguments, as separate list elements rather than one shell string. Overrides the image's own default command."},
			{Name: paramPorts, Type: "list", Description: "Port mappings, each \"host:container\" (docker run's own -p syntax). Publishing needs a network, so ports are refused unless network names one."},
			{Name: paramVolumes, Type: "list", Description: "Volume mappings, each \"host:container\" or \"host:container:options\" (docker run's own -v syntax). Each is mounted read-only unless its options say rw, as in \"/srv/data:/data:rw\"."},
			{Name: paramStart, Type: "bool", Default: "true", Description: "Start a container that exists under name but is stopped. Set false for a one-shot container whose command is meant to run once, since starting it again runs its command again."},
			{Name: paramNetwork, Type: "string", Default: networkDefault, Description: "The network the container joins: none (the default, no network at all), bridge, host (the host's own network, with no isolation), or the name of a network on the host. Required for ports."},
			{Name: paramEnv, Type: "dict", Description: "Environment variables to set in the container, as a map of name to value."},
			{Name: paramRestartPolicy, Type: "string", Description: "The restart policy (docker run's own --restart value, e.g. unless-stopped)."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The container this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What docker inspect reported about the container before this task and after it (exists, status). Recorded even on a run that changed nothing."},
			{Name: statImageID, Type: "string", Returned: "changed", Description: "The image ID the container was started from, when this task created it."},
		},
		Examples: []collection.Example{
			{
				Name:        "Run a container",
				RunbookYAML: "- name: Run nginx\n  container.docker.run:\n    name: web\n    image: nginx:1.27\n    network: bridge\n    ports:\n      - \"8080:80\"\n",
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
	network := sdk.StringParam(params, paramNetwork)
	if network == "" {
		network = networkDefault
	}
	start, err := sdk.BoolParamOr(params, paramStart, true)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, paramStart, err)
	}
	if err := checkName(name); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := checkRunParams(image, ports, volumes, restartPolicy); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := checkNetwork(network, ports); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	mounts := make([]string, 0, len(volumes))
	for _, v := range volumes {
		m, err := readOnlyVolume(v)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		mounts = append(mounts, m)
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
	startIt, err := startNeeded(name, before, start)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if mode == collection.ModeCheck {
		if startIt {
			return predictState(rc, fqcn, name, before, true, containerState{exists: true, status: "running"}.Map())
		}
		// A new container's status is left out: one whose command exits at
		// once is exited by the time a real run reads it back.
		return predictState(rc, fqcn, name, before, !before.exists, map[string]any{"exists": true})
	}

	changed := false
	after := before
	var undo *sdk.Inverse
	if startIt {
		if err := startContainer(ctx, conn, before); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		changed = true
		undo = &sdk.Inverse{
			FQCN: "container.docker.stop",
			// The id pins the undo to the container this task started.
			Params:      map[string]any{paramName: name, paramID: before.id},
			Description: fmt.Sprintf("Stop %s again, which this task found stopped and started.", name),
		}
		if after, err = queryContainer(ctx, conn, name); err != nil {
			after = containerState{exists: true, id: before.id, image: before.image}
			if err := sdk.RecordWarnings(rc, []string{fmt.Sprintf("started container %s but could not read it back, so its status is not reported: %v", name, err)}); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
	}
	if !before.exists {
		var createdID string
		imageID, _, err := resolveImage(ctx, conn, image)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		if err := rc.SetStat(statImageID, imageID); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		argv, stdin := dockerRunArgs(runSpec{name: name, imageRef: image, imageID: imageID, network: network,
			restartPolicy: restartPolicy, command: command, ports: ports, volumes: mounts, env: env})
		out, err := runDockerCmdWithStdin(ctx, conn, argv, stdin)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, cleanupFailedRun(ctx, conn, name, err))
		}
		changed = true
		// The undo is pinned to the id docker run itself printed, never to a
		// second inspect: the engine drops a failed task's undo, so reading
		// the container back is not allowed to fail the task once the
		// container exists.
		if createdID, err = createdContainerID(out); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		undo = &sdk.Inverse{
			FQCN: "container.docker.remove",
			// The id pins the undo to the container this task created: one
			// run later under the same name is refused rather than removed.
			Params: map[string]any{paramName: name, paramForce: true, paramID: createdID},
			Description: fmt.Sprintf("Remove %s, which this task created. Its volumes' contents on the host "+
				"are not touched by that removal, and are not restorable by this inverse either.", name),
		}
		if after, err = queryContainer(ctx, conn, name); err != nil {
			after = containerState{exists: true, id: createdID}
			if err := sdk.RecordWarnings(rc, []string{fmt.Sprintf("created container %s (%s) but could not read it back, so its status is not reported: %v", name, createdID, err)}); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
	}

	if err := recordState(rc, name, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if undo != nil {
		if err := sdk.RecordInverse(rc, *undo); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}
