// Package digests implements build.digests.check and
// build.digests.refresh: every container image reference in a build's
// files pinned by tag and digest, checked and moved forward on the device
// that holds the project. Finding and rewriting references is
// pkg/imagepin's, and resolving a tag is the device's engine's
// (containerengine.ResolveDigest).
package digests

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/imagepin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The methods' names.
const (
	fqcnCheck   = "build.digests.check"
	fqcnRefresh = "build.digests.refresh"
)

// Parameter names.
const (
	paramFiles   = "files"
	paramResolve = "resolve"
	paramSet     = "set"
)

// Return names.
const (
	statReferences = "references"
	statOffenders  = "offenders"
	statChanges    = "changes"
)

// maxFiles bounds how many files one task reads.
const maxFiles = 200

// maxListed bounds how many offenders or changes a failed task's error
// names; the rest are counted.
const maxListed = 50

// pathList reads a required or optional list of paths, each checked.
func pathList(params map[string]any, key string, required bool) ([]string, error) {
	list, set, err := sdk.StringSlice(params, key)
	if err != nil {
		return nil, err
	}
	if required && (!set || len(list) == 0) {
		return nil, fmt.Errorf("%s names no file", key)
	}
	if len(list) > maxFiles {
		return nil, fmt.Errorf("%s names %d files, over the %d one task reads", key, len(list), maxFiles)
	}
	seen := map[string]bool{}
	for _, p := range list {
		if err := checkPath(key, p); err != nil {
			return nil, err
		}
		// A file named twice would be read twice, and its second write
		// would refuse it as changed since it was read.
		clean := path.Clean(p)
		if seen[clean] {
			return nil, fmt.Errorf("%s names %s twice", key, p)
		}
		seen[clean] = true
	}
	return list, nil
}

// checkPath refuses a path no file could have.
func checkPath(key, p string) error {
	if p == "" || len(p) > 4096 || strings.ContainsAny(p, "\x00\n\r") {
		return fmt.Errorf("%s: %q is not a path", key, p)
	}
	return nil
}

// listed is lines for a failed task's error: at most maxListed of them,
// the rest counted, each cut to maxShown bytes and escaped, since a line
// carries text read from the device (a reference, a package name).
func listed(lines []string) string {
	shown := make([]string, 0, min(len(lines), maxListed)+1)
	for _, line := range lines[:min(len(lines), maxListed)] {
		shown = append(shown, remoteexec.Excerpt(line, maxShown))
	}
	if len(lines) > maxListed {
		shown = append(shown, fmt.Sprintf("... and %d more", len(lines)-maxListed))
	}
	return "\n  " + strings.Join(shown, "\n  ")
}

// maxShown bounds one item of device text in an error or a stat. No image
// reference is near it: a name is at most 255 bytes, a tag 128.
const maxShown = 512

// file is one file read for its references.
type file struct {
	path    string
	content []byte
	info    remotefile.Info
	refs    []imagepin.Ref
}

// scan reads each file and finds its references.
func scan(ctx context.Context, conn *remoteexec.Conn, paths []string) ([]*file, error) {
	var out []*file
	for _, p := range paths {
		content, info, err := remotefile.ReadText(ctx, conn, p, imagepin.MaxFileBytes)
		if err != nil {
			return nil, err
		}
		kind, err := imagepin.Detect(p, content)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		refs, err := imagepin.Extract(kind, content)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		out = append(out, &file{path: p, content: content, info: info, refs: refs})
	}
	return out, nil
}

// resolver asks the registry what a tag names now, through the device's
// engine, with the run's credential for that registry if it has one.
type resolver struct {
	rc      sdk.RunbookContext
	session *containerengine.Session
}

// openResolver opens the device's engine for resolving, naming why a
// device without one cannot.
func openResolver(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (*resolver, error) {
	if !device.HasCapability(capability.NameContainerEngine) {
		return nil, fmt.Errorf("%s: resolving a tag goes through the device's container engine, and none was onboarded on %q (run pleiades onboard)", fqcn, device.Name())
	}
	s, err := containerengine.Open(ctx, rc, device, params, fqcn, containerengine.DistributionOps...)
	if err != nil {
		return nil, err
	}
	return &resolver{rc: rc, session: s}, nil
}

// digest is what pin's name and tag name now.
func (r *resolver) digest(ctx context.Context, pin imagepin.Pin) (string, error) {
	ref := imagepin.Pin{Name: pin.Name, Tag: pin.Tag}.Format()
	auth, err := containerengine.RegistryAuth(r.rc, ref)
	if err != nil {
		return "", err
	}
	res, err := r.session.ResolveDigest(ctx, ref, auth)
	if err != nil {
		return "", err
	}
	return res.Digest, nil
}
