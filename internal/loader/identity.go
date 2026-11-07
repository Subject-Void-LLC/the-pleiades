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
// describe gave desc: the program's file name (the name its approval names),
// marked as a program's, no version, and a digest of its whole description as
// this build decoded it: its method manifests and the capabilities it defines
// as data (owner, 2026-11-07), so a program that changes what one of its
// capabilities applies to changes its identity, as one that changes a method
// does. Every value comes from the loader: the path it opened and the
// description it parsed, never a field the program filled in to name itself.
// A method named twice is refused, since a set cannot hold it and keeping
// either would digest something the program did not describe.
func programIdentity(path string, desc external.Description) (wire.CollectionIdentity, error) {
	methods := desc.Methods
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
	id := wire.CollectionIdentity{Group: name, Program: true, Manifests: collection.DescriptionDigest(manifests, desc.Capabilities)}
	if err := id.Valid(); err != nil {
		return wire.CollectionIdentity{}, fmt.Errorf("program %s: %w", path, err)
	}
	return id, nil
}
