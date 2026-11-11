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
	"unicode"
	"unicode/utf8"
)

// ManifestDigestPrefix opens every CollectionIdentity.Manifests value: the
// digest is "sha256:" and 64 lowercase hexadecimal digits, the shape every
// other digest in the journal takes.
const ManifestDigestPrefix = "sha256:"

// MaxIdentityGroupBytes bounds CollectionIdentity.Group: the longest file
// name a program may have on Linux, where the loader runs.
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

	// Program is true for an external program's identity, whose Group is
	// the program's file name; false for a first-party group's. A file name
	// can be the same word as a first-party group (a program named "file"),
	// so a reader tells the two apart by this field, never by Group alone.
	Program bool `json:"program,omitempty"`
	// Manifests is the digest of the group's canonical manifest set
	// (collection.ManifestSetDigest for a first-party group, collection.DescriptionDigest for a program): ManifestDigestPrefix and 64 hex
	// digits. It is what tells two builds' declarations apart, since it
	// depends on the declarations alone, not on the binary or the version.
	Manifests string `json:"manifests,omitempty"`
}

// IsZero reports whether id is the zero identity, which encoding/json's
// omitzero reads to leave the key out.
func (id CollectionIdentity) IsZero() bool {
	return id == CollectionIdentity{}
}

// Valid holds an identity a build is about to make to the shape it makes:
// Shape, and a Group that holds no control or format character.
//
// The second half depends on the Unicode tables of the Go release the build
// was made with, which a later release may change, so it is a rule for the
// process that makes an identity: the Runner's engine stamping its journal
// projection and its facts, the loader naming a program, the CLI's own
// fact store. A process that receives an identity another made, whether it
// stores it (the Controller storing a Runner's facts batch and journal rows)
// or only uses it, never holds it to Valid, whose answer would then depend
// on which toolchain built the receiver: a store holds it to Deliverable,
// Shape and a frozen list of misleading characters, and notes where its own
// Valid would differ; a reader that only uses a stored or delivered identity
// (the journal's reader, a Runner comparing a dispatch's) holds it to Shape.
// Neither can move with a release, so an identity written correctly stays
// storable and readable (LESSONS 415, 422).
//
// The zero identity is not valid: a caller checks IsZero first.
func (id CollectionIdentity) Valid() error {
	if err := id.Shape(); err != nil {
		return err
	}
	if strings.ContainsFunc(id.Group, controlOrFormat) {
		// A group is printed where an operator reads it: a control or format
		// character (an escape, a newline, a direction override) could make
		// it read as something else. Fixed text, so the refusal repeats none
		// of it.
		return fmt.Errorf("%w: its group holds a control or format character", ErrInvalidIdentity)
	}
	return nil
}

// Shape holds an identity read back from a record or a payload to the part
// of Valid that depends on nothing a Go release can change: a Group that is
// non-empty UTF-8 within MaxIdentityGroupBytes and holds no NUL or slash (a
// group name or a file name) and no C0 or C1 control code or DEL, no Version
// on a program's identity, a Version of printable ASCII within
// MaxIdentityVersionBytes (empty allowed), and a Manifests digest of the
// form "sha256:" and 64 lowercase hex digits. Byte lengths, UTF-8 validity,
// fixed code point ranges and the digest's pattern read the same under
// every toolchain: Unicode never reassigns a control code, so Valid has
// refused every one of them under every release, and Shape refusing them
// turns away nothing a build wrote.
//
// The zero identity does not pass: a reader checks IsZero first.
//
// Every refusal is fixed text that repeats none of the identity: its fields
// are delivered bytes of any length, and a refusal reaches log lines and
// operators.
func (id CollectionIdentity) Shape() error {
	switch {
	case id.Group == "":
		return fmt.Errorf("%w: it names no group", ErrInvalidIdentity)
	case len(id.Group) > MaxIdentityGroupBytes:
		return fmt.Errorf("%w: its group is over %d bytes", ErrInvalidIdentity, MaxIdentityGroupBytes)
	case !utf8.ValidString(id.Group) || strings.ContainsAny(id.Group, "\x00/"):
		return fmt.Errorf("%w: its group is neither a group name nor a file name", ErrInvalidIdentity)
	case strings.ContainsFunc(id.Group, controlCode):
		// An escape or a newline acts on the terminal or the log line a group
		// is printed into, so no reader takes one.
		return fmt.Errorf("%w: its group holds a control code", ErrInvalidIdentity)
	case id.Program && id.Version != "":
		// No build writes a version for a program: the loader knows none.
		return fmt.Errorf("%w: a program's identity carries no version, and this one names one", ErrInvalidIdentity)
	case len(id.Version) > MaxIdentityVersionBytes:
		return fmt.Errorf("%w: its version is over %d bytes", ErrInvalidIdentity, MaxIdentityVersionBytes)
	case !printableASCII(id.Version):
		return fmt.Errorf("%w: its version holds a character outside printable ASCII", ErrInvalidIdentity)
	case !manifestDigestPattern.MatchString(id.Manifests):
		return fmt.Errorf("%w: its manifest digest is not sha256:<64 lowercase hex digits>", ErrInvalidIdentity)
	}
	return nil
}

// controlOrFormat reports whether r could make a group read as something
// else: a character that is not graphic (a control, a line or paragraph
// separator), a format character (category Cf, such as a direction
// override), a variation selector, or a Hangul filler, which draws as
// nothing.
func controlOrFormat(r rune) bool {
	switch r {
	case 0x115F, 0x1160, 0x3164, 0xFFA0:
		return true
	}
	return !unicode.IsGraphic(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Variation_Selector, r)
}

// controlCode reports whether r is a C0 control code, DEL or a C1 control
// code: the fixed range Unicode's category Cc has always held and, by its
// stability policy, always will.
func controlCode(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f)
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
