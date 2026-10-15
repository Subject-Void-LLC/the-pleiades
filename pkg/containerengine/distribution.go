// What a reference names now, asked of its registry through the engine.
package containerengine

import (
	"context"
	"fmt"
	"regexp"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// sha256Digest is a digest as a registry gives one for a descriptor.
var sha256Digest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// DistributionOps are the operations ResolveDigest sends.
var DistributionOps = []Op{OpDistributionInspect}

// Resolved is what a registry says a reference names now.
type Resolved struct {
	// Digest is the digest of what the reference's tag points to: a
	// multi-platform image's index, or a single image's manifest.
	Digest string
	// MediaType says which of the two it is.
	MediaType string
	// Platforms are the platforms the image is built for, os/arch or
	// os/arch/variant.
	Platforms []string
}

// ResolveDigest asks the registry ref names, through the engine (so through
// the device's own network, proxy, mirrors and logins), what ref names now.
// auth is an X-Registry-Auth value (RegistryAuth), or "" for none. The
// answer's digest is refused unless it is sha256 and well formed, since it
// is text from the network that a caller writes into a file.
func (s *Session) ResolveDigest(ctx context.Context, ref, auth string) (Resolved, error) {
	if err := ValidImage(ref); err != nil {
		return Resolved{}, err
	}
	res, err := s.API.DistributionInspect(ctx, ref, client.DistributionInspectOptions{EncodedRegistryAuth: auth})
	if err != nil {
		return Resolved{}, fmt.Errorf("containerengine: asking the registry for %s: %w", ref, err)
	}
	digest := res.Descriptor.Digest.String()
	if !sha256Digest.MatchString(digest) {
		return Resolved{}, fmt.Errorf("containerengine: the registry answered %s with a digest that is not sha256: %q", ref, remoteexec.Excerpt(digest, 128))
	}
	out := Resolved{Digest: digest, MediaType: res.Descriptor.MediaType}
	for _, p := range res.Platforms {
		platform := p.OS + "/" + p.Architecture
		if p.Variant != "" {
			platform += "/" + p.Variant
		}
		out.Platforms = append(out.Platforms, platform)
	}
	return out, nil
}
