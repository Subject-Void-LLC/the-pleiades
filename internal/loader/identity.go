//go:build unix

// Package loader: an external program's Collection identity (Phase 136b),
// computed here from what the loader itself decoded and inspected.
package loader

import (
	"fmt"
	"path/filepath"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// programIdentity is the Collection identity of the program at path whose
// describe gave methods: the program's file name (the name its approval
// names), marked as a program's, no version, and collection.ManifestSetDigest
// over the manifests
// as this build decoded them. Every value comes from the loader: the path it
// opened and the description it parsed, never a field the program filled in
// to name itself. A method named twice is refused, since a set cannot hold
// it and keeping either would digest something the program did not
// describe.
func programIdentity(path string, methods []external.DescribedMethod) (wire.CollectionIdentity, error) {
	name := filepath.Base(path)
	manifests := make(map[string]collection.Manifest, len(methods))
	for _, m := range methods {
		if _, dup := manifests[m.Name]; dup {
			return wire.CollectionIdentity{}, fmt.Errorf("program %s describes method %q twice", path, m.Name)
		}
		manifests[m.Name] = m.Manifest
	}
	// Program marks the identity as a program's, so its Group (a file name,
	// which may be the same word as a first-party group's) is never read as
	// that group.
	id := wire.CollectionIdentity{Group: name, Program: true, Manifests: collection.ManifestSetDigest(manifests)}
	if err := id.Valid(); err != nil {
		return wire.CollectionIdentity{}, fmt.Errorf("program %s: %w", path, err)
	}
	return id, nil
}
