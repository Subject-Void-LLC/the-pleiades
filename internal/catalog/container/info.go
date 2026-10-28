// container.info: one container as the engine reports it.
package container

import (
	"context"
	"fmt"
	"slices"
	"strings"

	moby "github.com/moby/moby/api/types/container"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() { registerRead("container.info", Info) }

// Info implements container.info: one container as the engine reports it,
// environment values withheld.
func Info(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "container.info"
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := containerengine.ValidName("container", name); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	s, err := open(ctx, rc, device, params, fqcn, containerengine.OpContainerInspect)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	c, exists, err := inspect(ctx, s, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statExists, exists); err != nil {
		return collection.Result{}, err
	}
	if !exists {
		return collection.Result{}, nil
	}
	return collection.Result{}, recordStats(rc, describe(c))
}

// describe is what a container's info reports: everything the engine's
// inspect answer says of it that a runbook decides on (Phase 131's R5),
// and never an environment value, which may be a credential.
func describe(c moby.InspectResponse) map[string]any {
	out := map[string]any{
		statName: strings.TrimPrefix(c.Name, "/"), statID: c.ID, statState: stateOf(c), statImageID: c.Image,
		"created": c.Created, "restart_count": c.RestartCount, "platform": c.Platform,
	}
	if c.Config != nil {
		env := make([]string, 0, len(c.Config.Env))
		for _, kv := range c.Config.Env {
			k, _, _ := strings.Cut(kv, "=")
			env = append(env, k)
		}
		slices.Sort(env)
		out[statEnv] = env
		out["labels"] = c.Config.Labels
		out["command"] = c.Config.Cmd
		out["entrypoint"] = c.Config.Entrypoint
		out["user"] = c.Config.User
		out["working_dir"] = c.Config.WorkingDir
		out["hostname"] = c.Config.Hostname
		out[statImage] = c.Config.Labels[containerengine.LabelManagedBy]
	}
	if c.HostConfig != nil {
		out["network"] = string(c.HostConfig.NetworkMode)
		out["restart_policy"] = string(c.HostConfig.RestartPolicy.Name)
		out["privileged"] = c.HostConfig.Privileged
		out["read_only"] = c.HostConfig.ReadonlyRootfs
		out["cap_add"] = nonNilStrings(c.HostConfig.CapAdd)
		out["memory"] = c.HostConfig.Memory
		out["ports"] = publishedPorts(c.HostConfig.PortBindings)
	}
	if c.State != nil {
		out["exit_code"] = c.State.ExitCode
		out["started_at"] = c.State.StartedAt
		out["finished_at"] = c.State.FinishedAt
		out["oom_killed"] = c.State.OOMKilled
		out["pid"] = c.State.Pid
		// The engine's own words for why the container last failed to
		// start, bounded, and only when it has some.
		if e := c.State.Error; e != "" {
			out["state_error"] = boundedText(e, maxStateError)
		}
		if c.State.Health != nil {
			out["health"] = string(c.State.Health.Status)
		}
	}
	out["networks"] = attachedNetworks(c)
	mounts := []string{}
	for _, m := range c.Mounts {
		src := m.Source
		if m.Name != "" {
			src = m.Name
		}
		mode := "ro"
		if m.RW {
			mode = "rw"
		}
		mounts = append(mounts, src+":"+m.Destination+":"+mode)
	}
	out["volumes"] = mounts
	return out
}
