// container.image.push: a local image sent to its registry, unless the
// registry already holds it under that tag.
package image

import (
	"context"
	"fmt"
	"slices"

	"github.com/distribution/reference"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnPush = "container.image.push"

func init() {
	register(fqcnPush, containerengine.MethodManifest(collection.Reversibility{
		Reversible: false,
		Notes:      "A push changes the registry, which the engine has no operation to undo: a tag it moved cannot be moved back, nor a pushed image deleted, through the engine.",
	}, pushDoc()), push)
}

// push is container.image.push's one body. The registry is asked what the
// tag names now; when that is the local image (one of its repository
// digests, or its ID on an engine that names images by their index), there
// is nothing to send.
func push(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := failer(fqcnPush)
	raw, err := sdk.RequiredStringParam(params, paramImage)
	if err != nil {
		return fail(err)
	}
	ref, err := taggedRef(paramImage, raw)
	if err != nil {
		return fail(err)
	}
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return fail(err)
	}
	auth, err := containerengine.RegistryAuth(rc, ref)
	if err != nil {
		return fail(err)
	}
	s, err := open(ctx, rc, device, params, fqcnPush,
		containerengine.OpImageInspect, containerengine.OpDistributionInspect, containerengine.OpImagePush)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	local, exists, err := inspect(ctx, s, ref)
	if err != nil {
		return fail(err)
	}
	if !exists {
		return fail(fmt.Errorf("there is no image %s on the engine to push", ref))
	}
	if err := recordStats(rc, map[string]any{statImage: ref, statImageID: local.ID}); err != nil {
		return collection.Result{}, err
	}
	// A registry that cannot answer (the repository does not exist yet,
	// or refuses to say) is one the push is needed for; the push itself
	// then succeeds or says why not.
	if remote, err := s.ResolveDigest(ctx, ref, auth); err == nil && holds(local.ID, local.RepoDigests, reference.FamiliarName(named), remote.Digest) {
		return collection.Result{}, rc.SetStat(statDigest, remote.Digest)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	stream, err := s.API.ImagePush(ctx, ref, client.ImagePushOptions{RegistryAuth: auth})
	if err != nil {
		return fail(fmt.Errorf("pushing %s: %w", ref, err))
	}
	defer func() { _ = stream.Close() }()
	if err := stream.Wait(ctx); err != nil {
		return fail(fmt.Errorf("pushing %s: %w", ref, err))
	}
	remote, err := s.ResolveDigest(ctx, ref, auth)
	if err != nil {
		return fail(fmt.Errorf("reading what %s names after pushing it: %w", ref, err))
	}
	return collection.Result{Changed: true}, rc.SetStat(statDigest, remote.Digest)
}

// holds reports whether the registry's digest for repo is the local
// image's: one of its repository digests for that repository, or its ID,
// which an engine storing images by their index (containerd's store,
// Podman's manifest lists) gives as that index's digest.
func holds(id string, repoDigests []string, repo, digest string) bool {
	if id == digest {
		return true
	}
	return slices.ContainsFunc(repoDigests, func(d string) bool {
		named, err := reference.ParseNormalizedNamed(d)
		if err != nil {
			return false
		}
		canonical, ok := named.(reference.Canonical)
		return ok && reference.FamiliarName(named) == repo && canonical.Digest().String() == digest
	})
}
