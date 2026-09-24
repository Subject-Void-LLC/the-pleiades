// The resolved remote path, the resolver that alone can build one, and the
// containment comparisons every Store shares.
package filexfer

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxPathBytes caps a root, a leaf and their join. It is Linux's
// PATH_MAX, and it also bounds the one line of SCP's wire header a path
// ends up in.
const MaxPathBytes = 4096

// MaxSegmentBytes caps one path segment. It is NAME_MAX on every common
// POSIX filesystem, so a longer segment could not name a real file
// anyway.
const MaxSegmentBytes = 255

// Path is a remote path proven, by Resolve, to lie below a transfer
// root. Its fields are unexported, so the only way to build one is
// Resolve, and every Store refuses the zero value: an unresolved string
// cannot reach a protocol adapter at all.
type Path struct {
	root string // the validated, canonical, absolute root
	rel  string // the validated, canonical, relative leaf
}

// IsZero reports whether p is the zero Path, one Resolve did not build.
func (p Path) IsZero() bool { return p.root == "" }

// Root returns the transfer root p was resolved against.
func (p Path) Root() string { return p.root }

// Rel returns p relative to its root.
func (p Path) Rel() string { return p.rel }

// String returns p as one absolute remote path.
func (p Path) String() string { return path.Join(p.root, p.rel) }

// Dir returns the absolute path of p's parent directory, which always
// lies within the root (and is the root itself for a one-segment leaf).
func (p Path) Dir() string { return path.Dir(p.String()) }

// Base returns p's final segment, the file's own name.
func (p Path) Base() string { return path.Base(p.rel) }

// Resolve joins a transfer root and a leaf into a Path, refusing
// anything that could name a file outside the root.
//
// The rules are the codebase's established path guard
// (internal/playbook's ValidateReference, internal/engine's
// resolveImportPath) tightened for a path both an inventory author and a
// runbook author contribute to:
//
//   - The root must pass ValidateRoot.
//   - The leaf must be non-empty, relative, at most MaxPathBytes long,
//     and in its simplest form (so what a reviewer reads is exactly what
//     is opened; the error offers the canonical spelling).
//   - No leaf segment may be "." or "..", be longer than
//     MaxSegmentBytes, be invalid UTF-8 (which catches overlong
//     encodings of "." and "/"), hold NUL or any other control or
//     invisible format character (a newline would forge a line of SCP's
//     wire header), or hold a backslash (a Windows OpenSSH server treats
//     it as a separator).
//
// It uses the path package and never path/filepath, so its answer is
// the same on every controller operating system. It performs no I/O.
func Resolve(root, leaf string) (Path, error) {
	if err := ValidateRoot(root); err != nil {
		return Path{}, err
	}

	refuse := func(segment string, why Refusal) (Path, error) {
		return Path{}, &PathError{Part: PartLeaf, Value: leaf, Segment: segment, Refusal: why}
	}
	switch {
	case leaf == "":
		return refuse("", RefusedEmpty)
	case len(leaf) > MaxPathBytes:
		return refuse("", RefusedTooLong)
	case strings.HasPrefix(leaf, "/"):
		return refuse("", RefusedAbsoluteLeaf)
	}

	// Segment by segment, so the error names the one that is at fault.
	for segment := range strings.SplitSeq(leaf, "/") {
		if segment == "." || segment == ".." {
			return refuse(segment, RefusedDotSegment)
		}
		if why, bad := refuseSegment(segment); bad {
			return refuse(segment, why)
		}
	}

	// An empty segment ("a//b", "a/") survived the loop above only as
	// the empty string, which refuseSegment does not judge; path.Clean
	// is what catches it, and a trailing slash in particular, which
	// canonicalizing would silently turn from "a directory" into "a
	// file".
	if clean := path.Clean(leaf); clean != leaf {
		return Path{}, &PathError{Part: PartLeaf, Value: leaf, Canonical: clean, Refusal: RefusedNotCanonical}
	}

	p := Path{root: root, rel: leaf}
	full := p.String()
	if len(full) > MaxPathBytes {
		return refuse("", RefusedTooLong)
	}
	// Unreachable while the rules above hold, and checked anyway: if a
	// later edit loosens one of them, this fails closed instead of open.
	if !Within(root, full) || full == root {
		return refuse("", RefusedOutsideRoot)
	}
	return p, nil
}

// ValidateRoot checks a transfer root on its own: non-empty, absolute,
// at most MaxPathBytes long, in its simplest form, and with no segment
// that refuseSegment rejects. "/" is a valid root, and an operator may
// choose it; it confines nothing, which is theirs to decide.
//
// It is exported so a device type can refuse a bad file_transfer_root
// property when inventory loads, rather than the first transfer
// discovering it.
func ValidateRoot(root string) error {
	refuse := func(segment string, why Refusal) error {
		return &PathError{Part: PartRoot, Value: root, Segment: segment, Refusal: why}
	}
	switch {
	case root == "":
		return refuse("", RefusedEmpty)
	case len(root) > MaxPathBytes:
		return refuse("", RefusedTooLong)
	case !strings.HasPrefix(root, "/"):
		return refuse("", RefusedRelativeRoot)
	}
	for segment := range strings.SplitSeq(root[1:], "/") {
		if why, bad := refuseSegment(segment); bad {
			return refuse(segment, why)
		}
	}
	if clean := path.Clean(root); clean != root {
		return &PathError{Part: PartRoot, Value: root, Canonical: clean, Refusal: RefusedNotCanonical}
	}
	return nil
}

// refuseSegment judges one non-empty path segment's content, reporting
// why it is refused. The empty segment is left to the caller's
// canonical-form check.
func refuseSegment(segment string) (Refusal, bool) {
	if segment == "" {
		return 0, false
	}
	if len(segment) > MaxSegmentBytes {
		return RefusedSegmentTooLong, true
	}
	if !utf8.ValidString(segment) {
		return RefusedInvalidUTF8, true
	}
	for _, r := range segment {
		switch {
		case r == '\\':
			return RefusedBackslash, true
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			// IsControl covers NUL, every C0 control including newline,
			// DEL and the C1 range; Cf covers the invisible format
			// characters (right-to-left override, zero-width joiners)
			// that cannot inject anything but can make a log lie about
			// which file was written.
			return RefusedControl, true
		}
	}
	return 0, false
}

// Within reports whether the absolute path p is root itself or lies
// below it. It compares whole segments, so "/srv/xferX" is NOT within
// "/srv/xfer", and it treats "/" as containing every absolute path,
// where a naive prefix check against root+"/" would refuse them all.
func Within(root, p string) bool {
	if !strings.HasPrefix(p, "/") {
		return false
	}
	if root == "/" {
		return true
	}
	return p == root || strings.HasPrefix(p, root+"/")
}

// Contained is the physical half of containment: given what the device
// itself said the root and the target's parent directory physically
// are (after following every symlink), it reports whether the parent
// lies within the root. Both answers must be absolute and canonical,
// because an answer that is not cannot be compared honestly and is
// refused rather than trusted.
//
// Every Store calls it with its protocol's own answers (the SFTP
// adapter's client-side LSTAT and READLINK walk, the SCP adapter's
// "pwd -P" on the device), so the comparison itself exists exactly
// once.
func Contained(p Path, physicalRoot, physicalParent string) error {
	refuse := func(why Containment) error {
		return &ContainmentError{Path: p.String(), Resolved: physicalParent, Root: physicalRoot, Reason: why}
	}
	if !trustworthyAnswer(physicalRoot) || !trustworthyAnswer(physicalParent) {
		return refuse(ContainmentUntrustedAnswer)
	}
	if !Within(physicalRoot, physicalParent) {
		return refuse(ContainmentOutsideRoot)
	}
	return nil
}

// trustworthyAnswer reports whether a device's answer about a physical
// path is one Contained can compare: absolute, canonical, and free of
// NUL. It does not apply Resolve's stricter content rules, because the
// device's own directory names are its truth, not a runbook's input.
func trustworthyAnswer(p string) bool {
	return strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.ContainsRune(p, 0) && len(p) <= MaxPathBytes
}

// LeafKind turns a device's description of the final path component
// into a refusal, or nil when the transfer may proceed. forGet is true
// for a read, which needs an existing regular file; a write accepts a
// missing target too, since it creates one.
func LeafKind(p Path, kind Kind, exists, forGet bool) error {
	refuse := func(why Containment) error {
		return &ContainmentError{Path: p.String(), Reason: why}
	}
	switch {
	case !exists && forGet:
		// Wrapped rather than a ContainmentError: a missing file is not
		// a containment question, and callers test for it by identity.
		return fmt.Errorf("filexfer: %q: %w", p.String(), fs.ErrNotExist)
	case !exists:
		return nil
	case kind == KindSymlink:
		return refuse(ContainmentSymlinkLeaf)
	case kind == KindDirectory:
		return refuse(ContainmentDirectoryLeaf)
	case kind != KindRegular:
		return refuse(ContainmentNotRegular)
	}
	return nil
}
