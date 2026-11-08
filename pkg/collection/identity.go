// Package collection: the identity of the Collection a built-in method came
// from, the canonical digest of a set of manifests, and the digest of one
// undo's declaration (Phase 136b).
//
// A group's identity (wire.CollectionIdentity) is its first-party name, the
// core build it was linked into, and a digest of its manifests. The digest
// depends on the declarations alone: two builds of one source agree on it, a
// development build marked dirty with unchanged declarations matches a clean
// one, and the binary and the version string play no part. An external
// program's identity takes DescriptionDigest over what its describe gave (its
// manifests and the capabilities it defines), which the loader computes
// (internal/loader).
package collection

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
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
// manifest was decoded. A new field of a basic, slice or map type left empty
// under omitempty writes nothing, so it moves no digest until a method sets
// it; a new struct field is written even when zero (encoding/json does not
// omit a struct), so it moves every digest. Every part of a manifest is
// digested, its documentation included: a corrected sentence in a method's
// doc moves its group's digest.
func ManifestSetDigest(manifests map[string]Manifest) string {
	if manifests == nil {
		// An empty set and a nil one are the same set.
		manifests = map[string]Manifest{}
	}
	// A Manifest holds only strings, booleans, integers, structs, slices and
	// string-keyed maps of those (TestManifestEncodingCannotFail holds it to
	// that), which encoding/json always encodes, and its own output always
	// decodes, so neither step below can fail.
	return canonicalDigest(manifests)
}

// canonicalDigest is the SHA-256, with ManifestDigestPrefix, of v's
// canonical JSON: encoded, decoded with numbers kept as written, and encoded
// again, so every object's keys are sorted.
func canonicalDigest(v any) string {
	// A failure here would digest "null" for every failing set, a silent
	// collision, so it panics rather than return a digest.
	raw, err := marshalJSON(v)
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

// DescriptionDigest is an external program's description digest: the
// canonical form ManifestSetDigest uses, over the program's method manifests
// and the capabilities it defines as data, sorted by name. A program that
// defines no capability digests the same as ManifestSetDigest over its
// manifests would only by chance, never by design: the two are different
// subjects, told apart by the identity's Program field.
func DescriptionDigest(manifests map[string]Manifest, capabilities []capability.DataDefinition) string {
	if manifests == nil {
		manifests = map[string]Manifest{}
	}
	defs := append([]capability.DataDefinition(nil), capabilities...)
	// By name, and two definitions of one name (which the loader refuses) by
	// their encoding, so the order the program listed them in never matters.
	sort.SliceStable(defs, func(i, j int) bool {
		if defs[i].Name != defs[j].Name {
			return defs[i].Name < defs[j].Name
		}
		a, _ := marshalJSON(defs[i])
		b, _ := marshalJSON(defs[j])
		return string(a) < string(b)
	})
	if defs == nil {
		defs = []capability.DataDefinition{}
	}
	return canonicalDigest(struct {
		Methods      map[string]Manifest         `json:"methods"`
		Capabilities []capability.DataDefinition `json:"capabilities"`
	}{manifests, defs})
}

// UndoDigest is the digest of one undo's declaration: the emitting method's
// sdk.InverseSpec for it and the parameters the undo's own method declares
// (params, by name, as fragment.ByName gives them: its Doc.Params and its
// fragments'), in the canonical form ManifestSetDigest uses.
//
// It covers what decides whether a recorded undo still replays as it was
// recorded, and nothing else: the spec's FQCN, the Record and Withhold sets
// and its two flags; and each parameter's name, type, whether it is
// required, its default, the parameter it is an alias of, its closed set of
// choices, its format (a recorded path read as a template_file is a
// different replay), whether a run setting may fill it, and whether it may
// hold secret references. A description and a risk mark move nothing, so a
// corrected sentence in a method's documentation never reads as skew.
// TestUndoDigest_DecidesEveryField holds every field of sdk.InverseSpec and
// Param to that list or to the list of what is left out. The sets (Record,
// Withhold, Choices) are sorted, since their order means nothing to a
// replay.
func UndoDigest(spec sdk.InverseSpec, params map[string]Param) string {
	type undoParam struct {
		Name              string   `json:"name"`
		Type              string   `json:"type"`
		Required          bool     `json:"required"`
		Default           string   `json:"default"`
		AliasOf           string   `json:"aliasOf"`
		Choices           []string `json:"choices"`
		ChoicesIgnoreCase bool     `json:"choicesIgnoreCase"`
		Format            string   `json:"format"`
		RunSetting        bool     `json:"runSetting"`
		SecretRefs        bool     `json:"secretRefs"`
	}
	sorted := func(s []string) []string {
		out := append([]string{}, s...)
		sort.Strings(out)
		return out
	}
	declared := make(map[string]undoParam, len(params))
	for name, p := range params {
		declared[name] = undoParam{
			Name: p.Name, Type: p.Type, Required: p.Required, Default: p.Default,
			AliasOf: p.AliasOf, Choices: sorted(p.Choices), ChoicesIgnoreCase: p.ChoicesIgnoreCase,
			Format: string(p.Format), RunSetting: p.RunSetting, SecretRefs: p.SecretRefs,
		}
	}
	return canonicalDigest(struct {
		FQCN             string               `json:"fqcn"`
		Record           []string             `json:"record"`
		Withhold         []string             `json:"withhold"`
		MayBePartial     bool                 `json:"mayBePartial"`
		MayKeepOnFailure bool                 `json:"mayKeepOnFailure"`
		Params           map[string]undoParam `json:"params"`
	}{spec.FQCN, sorted(spec.Record), sorted(spec.Withhold), spec.MayBePartial, spec.MayKeepOnFailure, declared})
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
