// container.prune: what a task names as unused, removed by the IDs its own
// listing found, so a run removes only what it listed.
package container

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const (
	paramWhat   = "what"
	paramLabels = "labels"

	statPruned = "pruned"

	// maxPruned bounds how many objects one prune removes; past it the task
	// is refused, naming labels to narrow it.
	maxPruned = 500
)

// pruneKinds are what a prune may name, each with the ops it sends.
var pruneKinds = map[string][]containerengine.Op{
	"containers": {containerengine.OpContainerList, containerengine.OpContainerRemove},
	"images":     {containerengine.OpImageList, containerengine.OpImageRemove},
	"volumes":    {containerengine.OpVolumeList, containerengine.OpVolumeRemove},
	"networks":   {containerengine.OpNetworkList, containerengine.OpNetworkRemove},
}

// predefinedNetworks are the engine's own, never pruned.
var predefinedNetworks = []string{"bridge", "host", "none", "podman"}

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "container.prune",
		Manifest: containerengine.MethodManifest(collection.Reversibility{
			Reversible: false,
			Notes:      "A pruned container's writable layer, image or volume's data, or network is gone.",
		}, pruneDoc()),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return prune(ctx, rc, d, p, collection.ModeExecute)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return prune(ctx, rc, d, p, collection.ModeCheck)
		},
	})
}

// candidate is one object a prune would remove.
type candidate struct{ id, name string }

// prune is container.prune's body: it lists the unused objects of the kind
// named (stopped containers, dangling images, anonymous volumes no
// container uses, networks no container uses, each narrowed by labels),
// then removes those, by ID.
func prune(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "container.prune"
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	what, err := sdk.RequiredStringParam(params, paramWhat)
	if err != nil {
		return fail(err)
	}
	ops, ok := pruneKinds[what]
	if !ok {
		return fail(fmt.Errorf("%s: %q is not containers, images, volumes or networks", paramWhat, what))
	}
	labels, _, err := sdk.StringSlice(params, paramLabels)
	if err != nil {
		return fail(err)
	}
	for _, l := range labels {
		if strings.TrimSpace(l) == "" || strings.ContainsAny(l, "\x00\n") {
			return fail(fmt.Errorf("%s: %q is not key or key=value", paramLabels, l))
		}
	}
	s, err := open(ctx, rc, device, params, fqcn, ops...)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	found, err := candidates(ctx, s, what, labels)
	if err != nil {
		return fail(err)
	}
	if len(found) > maxPruned {
		return fail(fmt.Errorf("%d %s would be pruned, over the %d one task removes; narrow it with %s", len(found), what, maxPruned, paramLabels))
	}
	names := make([]string, 0, len(found))
	for _, c := range found {
		names = append(names, c.name)
	}
	slices.Sort(names)
	if err := rc.SetStat(statPruned, names); err != nil {
		return collection.Result{}, err
	}
	if len(found) == 0 || mode == collection.ModeCheck {
		return collection.Result{Changed: len(found) > 0}, nil
	}
	var failed []string
	for _, c := range found {
		if err := removeOne(ctx, s, what, c.id); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", c.name, err))
		}
	}
	if len(failed) > 0 {
		return collection.Result{Changed: len(failed) < len(found)}, fmt.Errorf("%s: %d of %d %s were not removed:\n  %s", fqcn, len(failed), len(found), what, strings.Join(failed, "\n  "))
	}
	return collection.Result{Changed: true}, nil
}

// candidates lists the unused objects of kind what, narrowed by labels.
func candidates(ctx context.Context, s *containerengine.Session, what string, labels []string) ([]candidate, error) {
	f := client.Filters{}
	if len(labels) > 0 {
		f = f.Add("label", labels...)
	}
	var out []candidate
	switch what {
	case "containers":
		res, err := s.API.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: f.Add("status", "created", "exited", "dead")})
		if err != nil {
			return nil, fmt.Errorf("listing containers: %w", err)
		}
		for _, c := range res.Items {
			name := c.ID
			if len(c.Names) > 0 {
				name = strings.TrimPrefix(c.Names[0], "/")
			}
			out = append(out, candidate{id: c.ID, name: name})
		}
	case "images":
		res, err := s.API.ImageList(ctx, client.ImageListOptions{Filters: f.Add("dangling", "true")})
		if err != nil {
			return nil, fmt.Errorf("listing images: %w", err)
		}
		for _, img := range res.Items {
			out = append(out, candidate{id: img.ID, name: img.ID})
		}
	case "volumes":
		res, err := s.API.VolumeList(ctx, client.VolumeListOptions{Filters: f.Add("dangling", "true")})
		if err != nil {
			return nil, fmt.Errorf("listing volumes: %w", err)
		}
		for _, v := range res.Items {
			// Only anonymous volumes: a named one holds data someone named.
			if anonymousVolume(v.Name) {
				out = append(out, candidate{id: v.Name, name: v.Name})
			}
		}
	case "networks":
		res, err := s.API.NetworkList(ctx, client.NetworkListOptions{Filters: f.Add("dangling", "true")})
		if err != nil {
			return nil, fmt.Errorf("listing networks: %w", err)
		}
		for _, n := range res.Items {
			if !slices.Contains(predefinedNetworks, n.Name) {
				out = append(out, candidate{id: n.ID, name: n.Name})
			}
		}
	}
	return out, nil
}

// anonymousVolume reports whether name is an engine-made volume name: 64
// hexadecimal characters.
func anonymousVolume(name string) bool {
	return len(name) == 64 && strings.Trim(name, "0123456789abcdef") == ""
}

// removeOne removes one candidate, never forced: one that came into use
// since it was listed is refused by the engine and reported.
func removeOne(ctx context.Context, s *containerengine.Session, what, id string) error {
	var err error
	switch what {
	case "containers":
		_, err = s.API.ContainerRemove(ctx, id, client.ContainerRemoveOptions{})
	case "images":
		_, err = s.API.ImageRemove(ctx, id, client.ImageRemoveOptions{})
	case "volumes":
		_, err = s.API.VolumeRemove(ctx, id, client.VolumeRemoveOptions{})
	case "networks":
		_, err = s.API.NetworkRemove(ctx, id, client.NetworkRemoveOptions{})
	}
	return err
}
