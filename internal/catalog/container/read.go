// container.info, list and logs: what the engine says of its containers,
// read only, so each one's check is the method itself.
package container

import (
	"context"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	moby "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const (
	paramAll   = "all"
	paramMatch = "match"
	paramTail  = "tail"
	paramSince = "since"

	statContainers = "containers"
	statExists     = "exists"
	statStdout     = "stdout"
	statStderr     = "stderr"

	// maxListed bounds how many containers one list reports.
	maxListed = 1000
	// defaultTail and maxTail bound how many log lines one read returns.
	defaultTail = 100
	maxTail     = 10000
	// maxLogBytes bounds the log text one read keeps, refused past it.
	maxLogBytes = 1 << 20
)

// readOnly is the reversibility every reading method declares.
var readOnly = collection.Reversibility{ReadOnly: true, Notes: "It reads what the engine reports, and changes nothing."}

func init() {
	for name, fn := range map[string]collection.Method{
		"container.info": Info,
		"container.list": List,
		"container.logs": Logs,
	} {
		collection.MustRegister(collection.Descriptor{
			Name:     name,
			Manifest: containerengine.MethodManifest(readOnly, readDoc(name)),
			Invoke:   fn,
			// It only reads, so a check of it is the task itself.
			Check: fn,
		})
	}
}

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

// describe is what a container's info reports: never an environment
// value, which may be a credential.
func describe(c moby.InspectResponse) map[string]any {
	out := map[string]any{
		statName: strings.TrimPrefix(c.Name, "/"), statID: c.ID, statState: stateOf(c), statImageID: c.Image,
		"created": c.Created,
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
		out[statImage] = c.Config.Labels[containerengine.LabelManagedBy]
	}
	if c.HostConfig != nil {
		out["network"] = string(c.HostConfig.NetworkMode)
		out["restart_policy"] = string(c.HostConfig.RestartPolicy.Name)
		out["privileged"] = c.HostConfig.Privileged
	}
	if c.State != nil {
		out["exit_code"] = c.State.ExitCode
		out["started_at"] = c.State.StartedAt
	}
	var mounts []string
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

// List implements container.list: the engine's containers, running only
// unless all is set, optionally those whose name matches a glob.
func List(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "container.list"
	all, err := sdk.BoolParamOr(params, paramAll, false)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	match := sdk.StringParam(params, paramMatch)
	if _, err := path.Match(match, ""); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %q is not a glob: %w", fqcn, paramMatch, match, err)
	}
	s, err := open(ctx, rc, device, params, fqcn, containerengine.OpContainerList)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	res, err := s.API.ContainerList(ctx, client.ContainerListOptions{All: all})
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: listing containers: %w", fqcn, err)
	}
	if len(res.Items) > maxListed {
		return collection.Result{}, fmt.Errorf("%s: the engine has %d containers, over the %d one list reports; narrow it with %s", fqcn, len(res.Items), maxListed, paramMatch)
	}
	var out []any
	for _, c := range res.Items {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		if match != "" {
			if ok, _ := path.Match(match, name); !ok {
				continue
			}
		}
		out = append(out, map[string]any{
			statName: name, statID: c.ID, statState: string(c.State), statImageID: c.ImageID,
			statImage: c.Labels[containerengine.LabelManagedBy], "labels": maps.Clone(c.Labels),
		})
	}
	slices.SortFunc(out, func(a, b any) int {
		return strings.Compare(a.(map[string]any)[statName].(string), b.(map[string]any)[statName].(string))
	})
	return collection.Result{}, rc.SetStat(statContainers, out)
}

// Logs implements container.logs: the last lines a container wrote,
// bounded.
func Logs(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "container.logs"
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
	tail, set, err := sdk.IntParam(params, paramTail)
	if err != nil {
		return fail(err)
	}
	if !set {
		tail = defaultTail
	}
	if tail < 1 || tail > maxTail {
		return fail(fmt.Errorf("%s: %d is not a number of lines from 1 to %d", paramTail, tail, maxTail))
	}
	since := sdk.StringParam(params, paramSince)
	if since != "" && strings.ContainsAny(since, "&?#/ ") {
		return fail(fmt.Errorf("%s: %q is not a timestamp or a duration", paramSince, since))
	}
	s, err := open(ctx, rc, device, params, fqcn, append([]containerengine.Op{containerengine.OpContainerInspect}, containerengine.LogsOps...)...)
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
	tty := c.Config != nil && c.Config.Tty
	stdout, stderr, err := s.Logs(ctx, c.ID, tail, since, tty, maxLogBytes)
	if err != nil {
		return fail(err)
	}
	return collection.Result{}, recordStats(rc, map[string]any{statName: name, statID: c.ID, statStdout: stdout, statStderr: stderr})
}
