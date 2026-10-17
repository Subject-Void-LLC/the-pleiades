// Package image implements the container.image.* methods (Phase 119e): an
// image's life on a device's engine, Docker or Podman, through the engine's
// own API (pkg/containerengine), never its command line.
//
// Every method opens one guarded session declaring only the operations it
// sends, validates every name, reference and path before any of them
// reaches the engine, and, when it changes something, reads, compares and
// writes through one body its check shares (the Phase 46 pattern).
package image

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/distribution/reference"
	mobyimage "github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameters and returns shared by the namespace's methods.
const (
	paramImage  = "image"
	paramImages = "images"
	paramID     = "id"
	paramForce  = "force"
	paramSource = "source"
	paramTarget = "tag"
	paramPath   = "path"
	paramDigest = "digest"

	statImage    = "image"
	statImageID  = "image_id"
	statDigest   = "digest"
	statPulled   = "pulled"
	statTagged   = "tagged"
	statRemoved  = "removed"
	statUsedBy   = "used_by"
	statImages   = "images"
	statPath     = "path"
	statSize     = "size"
	statMissing  = "missing"
	statPlatform = "platforms"
)

// imageIDPattern is an engine's image ID, with or without its algorithm.
var imageIDPattern = regexp.MustCompile(`^(sha256:)?[0-9a-f]{64}$`)

// isImageID reports whether s is an image ID rather than a reference.
func isImageID(s string) bool { return imageIDPattern.MatchString(s) }

// sameImage reports whether two image IDs name one image, with or
// without the sha256: prefix.
func sameImage(a, b string) bool {
	return strings.TrimPrefix(a, "sha256:") == strings.TrimPrefix(b, "sha256:")
}

// validImageRef refuses what is neither an image ID nor a reference the
// engines' own grammar accepts (containerengine.ValidImage).
func validImageRef(what, s string) error {
	if isImageID(s) {
		return nil
	}
	if err := containerengine.ValidImage(s); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// taggedRef reads s as a reference naming a tag, never a digest, and
// returns it in its familiar form with the default tag filled in
// ("nginx" is "nginx:latest").
func taggedRef(what, s string) (string, error) {
	if err := containerengine.ValidImage(s); err != nil {
		return "", fmt.Errorf("%s: %w", what, err)
	}
	named, err := reference.ParseNormalizedNamed(s)
	if err != nil {
		return "", fmt.Errorf("%s: %w", what, err)
	}
	if _, ok := named.(reference.Digested); ok {
		return "", fmt.Errorf("%s: %q names a digest, and this needs a tag", what, s)
	}
	return reference.FamiliarString(reference.TagNameOnly(named)), nil
}

// body is a method's one body, run with the mode it is called in.
type body func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any, mode collection.Mode) (collection.Result, error)

// register registers fqcn with one body serving its run and its check. A
// read-only method's check is its run.
func register(fqcn string, m collection.Manifest, b body) {
	collection.MustRegister(collection.Descriptor{
		Name:     fqcn,
		Manifest: m,
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return b(ctx, rc, d, p, collection.ModeExecute)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return b(ctx, rc, d, p, collection.ModeCheck)
		},
	})
}

// open opens the device's engine for fqcn, allowing ops.
func open(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, ops ...containerengine.Op) (*containerengine.Session, error) {
	return containerengine.Open(ctx, rc, device, params, fqcn, ops...)
}

// inspect reads the image ref names, reporting false when the engine has
// none by that name; any other failure is an error.
func inspect(ctx context.Context, s *containerengine.Session, ref string) (mobyimage.InspectResponse, bool, error) {
	res, err := s.API.ImageInspect(ctx, ref)
	if cerrdefs.IsNotFound(err) {
		return mobyimage.InspectResponse{}, false, nil
	}
	if err != nil {
		return mobyimage.InspectResponse{}, false, fmt.Errorf("reading image %s: %w", ref, err)
	}
	if !isImageID(res.ID) {
		return mobyimage.InspectResponse{}, false, fmt.Errorf("the engine answered an unusable ID for image %s", ref)
	}
	return res.InspectResponse, true, nil
}

// containersUsing maps each image ID a container on the engine runs, in
// any state, to those containers' names.
func containersUsing(ctx context.Context, s *containerengine.Session) (map[string][]string, error) {
	res, err := s.API.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	out := map[string][]string{}
	for _, c := range res.Items {
		name := c.ID
		if len(c.Names) > 0 {
			name = c.Names[0]
		}
		if len(name) > 0 && name[0] == '/' {
			name = name[1:]
		}
		out[c.ImageID] = append(out[c.ImageID], name)
	}
	for id := range out {
		slices.Sort(out[id])
	}
	return out, nil
}

// recordStats records each stat, stopping at the first failure.
func recordStats(rc sdk.RunbookContext, stats map[string]any) error {
	for k, v := range stats {
		if err := rc.SetStat(k, v); err != nil {
			return err
		}
	}
	return nil
}

// failer prefixes a method's errors with its name.
func failer(fqcn string) func(error) (collection.Result, error) {
	return func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
}
