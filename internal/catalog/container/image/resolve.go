// container.image.resolve: what a tag names now in its registry, read;
// its check is its run.
package image

import (
	"context"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/distribution/reference"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	register(fqcnResolve, containerengine.MethodManifest(readOnly, resolveDoc()), resolve)
}

// statExists is whether the registry holds the tag resolve was asked for.
const statExists = "exists"

// resolve is container.image.resolve's body: what a reference names now,
// asked of its registry through the engine, with the registry credential
// bound for its host.
func resolve(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, _ collection.Mode) (collection.Result, error) {
	fail := failer(fqcnResolve)
	ref, err := sdk.RequiredStringParam(params, paramImage)
	if err != nil {
		return fail(err)
	}
	if err := containerengine.ValidImage(ref); err != nil {
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
	s, err := open(ctx, rc, device, params, fqcnResolve, containerengine.DistributionOps...)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	got, err := s.ResolveDigest(ctx, ref, auth)
	// A tag the registry answers "not found" for is an answer, exists
	// false, so a plan can branch on whether a release is published. Every
	// other refusal (no route, a credential refused, a registry that hides
	// a private repository behind "unauthorized") stays a failure, since it
	// does not say the tag is absent.
	if cerrdefs.IsNotFound(err) {
		return collection.Result{}, recordStats(rc, map[string]any{statExists: false, statImage: ref})
	}
	if err != nil {
		return fail(err)
	}
	pinned, err := reference.ParseNormalizedNamed(reference.TrimNamed(named).Name() + "@" + got.Digest)
	if err != nil {
		return fail(fmt.Errorf("pinning %s to %s: %w", ref, got.Digest, err))
	}
	return collection.Result{}, recordStats(rc, map[string]any{
		statExists: true, statImage: reference.FamiliarString(pinned), statDigest: got.Digest,
		statMediaType: got.MediaType, statPlatform: nonNil(got.Platforms),
	})
}
