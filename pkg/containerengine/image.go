// An image as a method runs it: pinned to a digest, then present on the
// engine, pulled by that digest when it is not.
package containerengine

import (
	"context"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/distribution/reference"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// EnsureImageOps are the operations EnsureImage sends.
var EnsureImageOps = []Op{OpImageInspect, OpImagePull, OpDistributionInspect}

// PresentImage is the image a reference names, as a method runs it.
type PresentImage struct {
	// Ref is the reference the task named.
	Ref string
	// Pinned is the reference by digest, the one pulled and run: a tag
	// resolved through the registry to what it names now (a multi-platform
	// image's index), so it means the same on every machine.
	Pinned string
	// Digest is Pinned's digest.
	Digest string
	// ID is the engine's image ID, "" when the image is absent and was not
	// pulled (a check).
	ID string
	// Pulled reports that the image was absent: pulled by a run, or to be
	// pulled, by a check.
	Pulled bool
}

// EnsureImage pins ref to a digest and makes that image present, pulling
// it by digest with auth (an X-Registry-Auth value, or "" for none) when it
// is absent. With pull false (a check) nothing is pulled: an absent image
// is reported with no ID and Pulled true. Only the engine's own "no such
// image" reads as absent; any other failure is an error.
func (s *Session) EnsureImage(ctx context.Context, ref, auth string, pull bool) (PresentImage, error) {
	img := PresentImage{Ref: ref}
	if err := ValidImage(ref); err != nil {
		return img, err
	}
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return img, fmt.Errorf("image %q: %w", ref, err)
	}
	if canonical, ok := named.(reference.Canonical); ok {
		img.Digest = canonical.Digest().String()
		img.Pinned = reference.FamiliarString(canonical)
	} else {
		resolved, err := s.ResolveDigest(ctx, ref, auth)
		if err != nil {
			return img, err
		}
		pinned, err := reference.ParseNormalizedNamed(reference.TrimNamed(named).Name() + "@" + resolved.Digest)
		if err != nil {
			return img, fmt.Errorf("image %q pinned to %s: %w", ref, resolved.Digest, err)
		}
		img.Digest = resolved.Digest
		img.Pinned = reference.FamiliarString(pinned)
	}

	found, err := s.API.ImageInspect(ctx, img.Pinned)
	if err == nil {
		img.ID = found.ID
		return img, nil
	}
	if !cerrdefs.IsNotFound(err) {
		return img, fmt.Errorf("reading image %s: %w", img.Pinned, err)
	}
	img.Pulled = true
	if !pull {
		return img, nil
	}
	stream, err := s.API.ImagePull(ctx, img.Pinned, client.ImagePullOptions{RegistryAuth: auth})
	if err != nil {
		return img, fmt.Errorf("pulling %s: %w", img.Pinned, err)
	}
	defer func() { _ = stream.Close() }()
	if err := stream.Wait(ctx); err != nil {
		// The stream's error is the registry's own text, cut as every other
		// message an engine relays is.
		return img, fmt.Errorf("pulling %s: %s", img.Pinned, remoteexec.Excerpt(err.Error(), 512))
	}
	found, err = s.API.ImageInspect(ctx, img.Pinned)
	if err != nil {
		return img, fmt.Errorf("reading image %s after pulling it: %w", img.Pinned, err)
	}
	img.ID = found.ID
	return img, nil
}

// LocalImage is the image the engine already holds under ref as written,
// for a task whose pull policy is never: a loaded or air-gapped image the
// engine knows by tag alone. Its digest is the one the engine recorded for
// it from a registry, when it has one; the ID is what runs. An absent
// image is refused, never pulled.
func (s *Session) LocalImage(ctx context.Context, ref string) (PresentImage, error) {
	img := PresentImage{Ref: ref, Pinned: ref}
	if err := ValidImage(ref); err != nil {
		return img, err
	}
	found, err := s.API.ImageInspect(ctx, ref)
	if cerrdefs.IsNotFound(err) {
		return img, fmt.Errorf("image %s is not on the engine, and pull is never", ref)
	}
	if err != nil {
		return img, fmt.Errorf("reading image %s: %w", ref, err)
	}
	img.ID = found.ID
	if named, err := reference.ParseNormalizedNamed(ref); err == nil {
		for _, d := range found.RepoDigests {
			if pinned, err := reference.ParseNormalizedNamed(d); err == nil && pinned.Name() == named.Name() {
				if c, ok := pinned.(reference.Canonical); ok {
					img.Digest = c.Digest().String()
				}
				break
			}
		}
	}
	return img, nil
}
