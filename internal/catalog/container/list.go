// container.list: the engine's containers, matched by name.
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

func init() { registerRead("container.list", List) }

// List implements container.list: every container on the engine, or the
// running ones alone when all is false, optionally those whose name
// matches a glob. Every container is the default (Phase 131's R5): a
// stopped container is still part of what the engine holds.
func List(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "container.list"
	all, err := sdk.BoolParamOr(params, paramAll, true)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	match, err := containerengine.TextParam(params, paramMatch)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
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
		out = append(out, listed(name, c))
	}
	slices.SortFunc(out, func(a, b any) int {
		return strings.Compare(a.(map[string]any)[statName].(string), b.(map[string]any)[statName].(string))
	})
	return collection.Result{}, rc.SetStat(statContainers, out)
}

// listed is one container's entry in container.list's answer, named name.
// Its command is the program and argument count only: the list holds
// containers no runbook made, and a command line's arguments may hold a
// secret (--requirepass X, --password=X).
func listed(name string, c moby.Summary) map[string]any {
	return map[string]any{
		statName: name, statID: c.ID, statState: string(c.State), statImageID: c.ImageID,
		statImage: c.Labels[containerengine.LabelManagedBy], "labels": maps.Clone(c.Labels),
		"status": c.Status, "created": c.Created, "command": containerengine.CommandLineSummary(c.Command), "ports": summaryPorts(c.Ports),
	}
}

// summaryPorts is each port a listed container exposes, as docker ps writes
// one: host_ip:host_port->container_port/proto when published (the host
// address left out when it is every address), container_port/proto when
// not, sorted so two reads compare equal.
func summaryPorts(ports []moby.PortSummary) []string {
	out := []string{}
	for _, p := range ports {
		inner := fmt.Sprintf("%d/%s", p.PrivatePort, p.Type)
		if p.PublicPort == 0 {
			out = append(out, inner)
			continue
		}
		host := fmt.Sprint(p.PublicPort)
		if p.IP.IsValid() && !p.IP.IsUnspecified() {
			addr := p.IP.String()
			if p.IP.Is6() {
				addr = "[" + addr + "]"
			}
			host = addr + ":" + host
		}
		out = append(out, host+"->"+inner)
	}
	slices.Sort(out)
	return slices.Compact(out)
}
