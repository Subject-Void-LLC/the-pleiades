// container.image.info: the images on the engine, read; its check is its
// run.
package image

import (
	"context"
	"fmt"

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
		var labels map[string]string
		if img.Config != nil {
			labels = img.Config.Labels
		}
		// The image's environment is left out on purpose: a value baked
		// into an image can be a secret, and an image's labels and names
		// say what it is.
		images = append(images, map[string]any{
			"id": img.ID, "tags": nonNil(img.RepoTags), "digests": nonNil(img.RepoDigests), "created": img.Created,
			"size": img.Size, "os": img.Os, "architecture": img.Architecture, "labels": labels,
		})
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
