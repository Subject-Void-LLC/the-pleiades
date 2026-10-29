// container.image.info: the images on the engine, read; its check is its
// run.
package image

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
	fqcnInfo    = "container.image.info"
	fqcnResolve = "container.image.resolve"
	fqcnSearch  = "container.image.search"

	paramTerm     = "term"
	paramLimit    = "limit"
	paramOfficial = "official_only"
	paramStars    = "min_stars"

	statResults   = "results"
	statMediaType = "media_type"

	defaultSearchLimit = 25
	maxSearchLimit     = 100
	maxTermLength      = 255
	maxDescription     = 300
)

// readOnly is the reversibility of a method that changes nothing.
var readOnly = collection.Reversibility{ReadOnly: true, Notes: "It reads and changes nothing, so there is nothing to undo."}

func init() {
	register(fqcnInfo, containerengine.MethodManifest(readOnly, infoDoc()), info)
}

// info is container.image.info's body: the images named, or every image.
func info(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, _ collection.Mode) (collection.Result, error) {
	fail := failer(fqcnInfo)
	refs, _, err := sdk.StringSlice(params, paramImages)
	if err != nil {
		return fail(err)
	}
	for _, ref := range refs {
		if err := validImageRef(paramImages, ref); err != nil {
			return fail(err)
		}
	}
	s, err := open(ctx, rc, device, params, fqcnInfo, containerengine.OpImageInspect, containerengine.OpImageList)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	images := []map[string]any{}
	missing := []string{}
	if len(refs) == 0 {
		list, err := s.API.ImageList(ctx, client.ImageListOptions{})
		if err != nil {
			return fail(fmt.Errorf("listing images: %w", err))
		}
		for _, img := range list.Items {
			images = append(images, map[string]any{
				"id": img.ID, "tags": nonNil(img.RepoTags), "digests": nonNil(img.RepoDigests),
				"created": img.Created, "size": img.Size, "labels": img.Labels, "containers": img.Containers,
			})
		}
	}
	for _, ref := range refs {
		img, exists, err := inspect(ctx, s, ref)
		if err != nil {
			return fail(err)
		}
		if !exists {
			missing = append(missing, ref)
			continue
		}
		entry := map[string]any{
			"id": img.ID, "tags": nonNil(img.RepoTags), "digests": nonNil(img.RepoDigests), "created": img.Created,
			"size": img.Size, "os": img.Os, "architecture": img.Architecture, "variant": img.Variant, "labels": map[string]string(nil),
		}
		// What a container made from it runs, as the image declares it
		// (Phase 131's R5). The environment is reported by name only, and
		// the entrypoint and command by program and argument count only: a
		// value or an argument baked into an image can be a secret.
		if c := img.Config; c != nil {
			entry["labels"] = c.Labels
			entry["entrypoint"] = containerengine.ArgvSummary(c.Entrypoint)
			entry["command"] = containerengine.ArgvSummary(c.Cmd)
			entry["user"] = c.User
			entry["working_dir"] = c.WorkingDir
			entry["exposed_ports"] = sortedKeys(c.ExposedPorts)
			entry["volumes"] = sortedKeys(c.Volumes)
			entry["env_names"] = envNames(c.Env)
		}
		images = append(images, entry)
	}
	return collection.Result{}, recordStats(rc, map[string]any{statImages: images, statMissing: missing})
}

// nonNil is list, or an empty list for nil, so a result always holds a
// list where its documentation says one.
func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// sortedKeys is a set's members, sorted, and an empty list for none.
func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// envNames is an environment's variable names, sorted, never a value.
func envNames(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
