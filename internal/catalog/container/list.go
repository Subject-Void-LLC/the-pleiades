// container.list: the engine's containers, matched by name.
package container

import (
	"context"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() { registerRead("container.list", List) }

// List implements container.list: the engine's containers, running only
// unless all is set, optionally those whose name matches a glob.
func List(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "container.list"
	all, err := sdk.BoolParamOr(params, paramAll, false)
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
