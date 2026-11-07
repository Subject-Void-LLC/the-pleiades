// Package wire: the identity of the Collection a method came from (Phase
// 136b).
//
// Once Collections are versioned apart from the core, a recorded undo or a
// stored fact has to say which Collection, at which declarations, wrote it,
// so a later build can tell "the program changed" from "the device changed".
// CollectionIdentity is that record. The core writes it from what it linked
// or what its loader hashed; a program never supplies one, since anything a
// program says about itself is text it could make up until it is signed.
package wire

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ManifestDigestPrefix opens every CollectionIdentity.Manifests value: the
// digest is "sha256:" and 64 lowercase hexadecimal digits, the shape every
// other digest in the journal takes.
const ManifestDigestPrefix = "sha256:"

// MaxIdentityGroupBytes bounds CollectionIdentity.Group: the longest file
// name a program may have on the platforms the loader runs on.
const MaxIdentityGroupBytes = 255

// MaxIdentityVersionBytes bounds CollectionIdentity.Version. A release
// version with a prerelease and build metadata fits in well under it.
const MaxIdentityVersionBytes = 128

// manifestDigestPattern is the one shape a Manifests value may take.
var manifestDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ErrInvalidIdentity marks an identity whose shape no build writes.
var ErrInvalidIdentity = errors.New("invalid collection identity")

// CollectionIdentity names the Collection a method came from and the
// declarations it had: a first-party group linked into this build, or an
// external program the loader hashed and approved.
//
// Its zero value means "unknown": a record written before the field existed,
// or one with nothing to attribute (an engine action, an unregistered name).
// A carrier writes it with omitzero, so a zero identity adds no key and an
// older record's bytes are unchanged.
type CollectionIdentity struct {
	// Group is the first-party group's name ("file", "virt.clab";
	// collection.FirstPartyGroup.Name) for a built-in method, or the file
	// name of the external program the loader approved for one it loaded.
	Group string `json:"group,omitempty"`

	// Version is the core build a built-in group was linked into, as the
	// build reports it ("0.4.0", or "0.0.0-dev+<rev>" for a development
	// build). It is printed for a person and never parsed for order: two
	// development builds of different trees can share one. It is empty for
	// an external program, whose own version would be text the program sent.
	Version string `json:"version,omitempty"`

	// Manifests is the digest of the group's canonical manifest set
	// (collection.ManifestSetDigest): ManifestDigestPrefix and 64 hex
	// digits. It is what tells two builds' declarations apart, since it
	// depends on the declarations alone, not on the binary or the version.
	Manifests string `json:"manifests,omitempty"`
}

// IsZero reports whether id is the zero identity, which encoding/json's
// omitzero reads to leave the key out.
func (id CollectionIdentity) IsZero() bool {
	return id == CollectionIdentity{}
}

// Valid holds an identity read back from a record or a payload to the shape
// a build writes: a Group that is non-empty UTF-8 within
// MaxIdentityGroupBytes and holds no NUL or slash (a group name or a file
// name), a Version of printable ASCII within MaxIdentityVersionBytes (empty
// allowed), and a Manifests digest of the form "sha256:" and 64 lowercase
// hex digits. The zero identity is not valid: a reader checks IsZero first.
func (id CollectionIdentity) Valid() error {
	switch {
	case id.Group == "":
		return fmt.Errorf("%w: it names no group", ErrInvalidIdentity)
	case len(id.Group) > MaxIdentityGroupBytes:
		return fmt.Errorf("%w: its group is over %d bytes", ErrInvalidIdentity, MaxIdentityGroupBytes)
	case !utf8.ValidString(id.Group) || strings.ContainsAny(id.Group, "\x00/"):
		return fmt.Errorf("%w: its group %q is neither a group name nor a file name", ErrInvalidIdentity, id.Group)
	case len(id.Version) > MaxIdentityVersionBytes:
		return fmt.Errorf("%w: its version is over %d bytes", ErrInvalidIdentity, MaxIdentityVersionBytes)
	case !printableASCII(id.Version):
		return fmt.Errorf("%w: its version %q holds a character outside printable ASCII", ErrInvalidIdentity, id.Version)
	case !manifestDigestPattern.MatchString(id.Manifests):
		return fmt.Errorf("%w: its manifest digest %q is not sha256:<64 lowercase hex digits>", ErrInvalidIdentity, id.Manifests)
	}
	return nil
}

// printableASCII reports whether every byte of s is a visible ASCII
// character, space excluded.
func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}
