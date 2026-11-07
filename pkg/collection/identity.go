// Package collection: the identity of the Collection a built-in method came
// from, and the canonical digest of a set of manifests (Phase 136b).
//
// A group's identity (wire.CollectionIdentity) is its first-party name, the
// core build it was linked into, and a digest of its manifests. The digest
// depends on the declarations alone: two builds of one source agree on it, a
// development build marked dirty with unchanged declarations matches a clean
// one, and the binary and the version string play no part. An external
// program's identity takes the same digest over the manifests its describe
// gave, which the loader computes (internal/loader).
package collection

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// ErrNotBuiltin marks a method name BuiltinIdentity has no identity for: one
// this build neither links nor holds in its installed catalog, one an
// external program provides, or one in no first-party group.
var ErrNotBuiltin = errors.New("collection: not a built-in method of a first-party group")

// ErrGroupNotLinked marks a first-party group this build holds no built-in
// method of, so it has no manifest set to digest (kubernetes before its
// methods arrive, or a group a build leaves out).
var ErrGroupNotLinked = errors.New("collection: the first-party group holds no built-in method in this build")

// marshalJSON and groupDigest are json.Marshal and GroupDigest, as variables
// so a test can prove what ManifestSetDigest and BuiltinIdentity do on a
// failure no real input reaches.
var (
	marshalJSON = json.Marshal
	groupDigest = GroupDigest
)

// ManifestSetDigest is the digest of a set of manifests keyed by method
// name: wire.ManifestDigestPrefix and the hex SHA-256 of the set's canonical
// JSON.
//
// The canonical JSON is the object mapping each name to its Manifest as this
// build's JSON tags write it, put through JSON once more with numbers kept as
// written so every object's keys come out sorted, with no white space and
// with encoding/json's escaping. So the digest depends on neither the order
// the manifests arrived in nor the order a struct declares its fields. The
// order of a list inside a manifest (its transports, its parameters) is part
// of what is digested: two declarations listing one set in another order
// read as different. A field this build does not
// know (an external program's describe can carry one) was dropped when the
// manifest was decoded, and a field left at its zero value under omitempty
// writes nothing, so adding such a field to Manifest moves no digest until a
// method sets it.
func ManifestSetDigest(manifests map[string]Manifest) string {
	if manifests == nil {
		// An empty set and a nil one are the same set.
		manifests = map[string]Manifest{}
	}
	// A Manifest holds only strings, booleans, integers, structs, slices and
	// string-keyed maps of those (TestManifestEncodingCannotFail holds it to
	// that), which encoding/json always encodes, and its own output always
	// decodes, so neither step below can fail.
	// A failure here would digest "null" for every failing set, a silent
	// collision, so it panics rather than return a digest.
	raw, err := marshalJSON(manifests)
	if err != nil {
		panic("collection: a manifest set did not encode, which TestManifestEncodingCannotFail rules out: " + err.Error())
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var normalized any
	if err := dec.Decode(&normalized); err != nil {
		panic("collection: a manifest set's own encoding did not decode: " + err.Error())
	}
	canonical, err := marshalJSON(normalized)
	if err != nil {
		panic("collection: a decoded manifest set did not encode again: " + err.Error())
	}
	sum := sha256.Sum256(canonical)
	return wire.ManifestDigestPrefix + hex.EncodeToString(sum[:])
}

// GroupManifests returns the manifests of every built-in method of the
// first-party group named group, by method name: each method registered
// with no Provider whose group (FirstPartyGroupOf) is group, and, for a
// method not registered at all, each one in the installed catalog. It is the
// set BuiltinManifest answers from, so a Controller (which registers no
// Collection and installs the catalog) and a Runner (which registers every
// built-in) hold the same set when the catalog was generated from the same
// registry. It is empty for a group this build holds no method of.
func GroupManifests(group string) map[string]Manifest {
	out := map[string]Manifest{}
	for name, d := range collections.All() {
		if d.Provider == nil && groupNameOf(name) == group {
			out[name] = d.Manifest
		}
	}
	builtinCatalog.RLock()
	catalog := maps.Clone(builtinCatalog.manifests)
	builtinCatalog.RUnlock()
	for name, m := range catalog {
		if _, registered := Lookup(name); !registered && groupNameOf(name) == group {
			out[name] = m
		}
	}
	return out
}

// groupNameOf is the name of the first-party group fqcn belongs to, or ""
// when it belongs to none.
func groupNameOf(fqcn string) string {
	g, _ := FirstPartyGroupOf(fqcn)
	return g.Name
}

// GroupDigest is ManifestSetDigest over GroupManifests(group). It refuses a
// name that is not a first-party group, and a group this build holds no
// built-in method of (ErrGroupNotLinked), rather than digest an empty set no
// build of that group ever had. It is computed on every call; a caller that
// asks once per task caches it after the registry is complete.
func GroupDigest(group string) (string, error) {
	known := false
	for _, g := range firstPartyGroups {
		known = known || g.Name == group
	}
	if !known {
		return "", fmt.Errorf("collection: %q is not a first-party group", group)
	}
	manifests := GroupManifests(group)
	if len(manifests) == 0 {
		return "", fmt.Errorf("%w: %s", ErrGroupNotLinked, group)
	}
	return ManifestSetDigest(manifests), nil
}

// BuiltinIdentity is the identity of the first-party group the built-in
// method fqcn belongs to: the group's name, version, and the group's
// GroupDigest. It refuses (ErrNotBuiltin) a name BuiltinManifest does not
// call built-in or one in no first-party group.
//
// version is the core build's version, which the caller passes because
// pkg/ may not import internal/buildinfo: a composition root, or the engine
// on its behalf, passes buildinfo.Version(). It is recorded as given and
// takes no part in the digest.
func BuiltinIdentity(fqcn, version string) (wire.CollectionIdentity, error) {
	if _, builtin := BuiltinManifest(fqcn); !builtin {
		return wire.CollectionIdentity{}, fmt.Errorf("%w: %s", ErrNotBuiltin, fqcn)
	}
	g, ok := FirstPartyGroupOf(fqcn)
	if !ok {
		return wire.CollectionIdentity{}, fmt.Errorf("%w: %s belongs to no first-party group", ErrNotBuiltin, fqcn)
	}
	digest, err := groupDigest(g.Name)
	if err != nil {
		return wire.CollectionIdentity{}, err
	}
	return wire.CollectionIdentity{Group: g.Name, Version: version, Manifests: digest}, nil
}
