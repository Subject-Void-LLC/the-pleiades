// The port's typed refusals: a path refused by Resolve, and a transfer
// refused by the device's own answer about where a path physically is.
package filexfer

import "fmt"

// PathPart says which half of a path a PathError is about.
type PathPart uint8

const (
	// PartRoot is the transfer root, which comes from inventory.
	PartRoot PathPart = iota + 1
	// PartLeaf is the path below the root, which comes from a runbook.
	PartLeaf
)

// String returns the part's name as an error message uses it.
func (p PathPart) String() string {
	switch p {
	case PartRoot:
		return "transfer root"
	case PartLeaf:
		return "transfer path"
	default:
		return "path"
	}
}

// Refusal is why Resolve or ValidateRoot refused a path.
type Refusal uint8

const (
	// RefusedEmpty is an empty root, leaf or segment.
	RefusedEmpty Refusal = iota + 1
	// RefusedTooLong is a path longer than MaxPathBytes.
	RefusedTooLong
	// RefusedSegmentTooLong is one segment longer than MaxSegmentBytes.
	RefusedSegmentTooLong
	// RefusedInvalidUTF8 is a segment that is not valid UTF-8, which
	// includes overlong encodings of "." and "/".
	RefusedInvalidUTF8
	// RefusedControl is a segment holding NUL, another control
	// character, or an invisible Unicode format character.
	RefusedControl
	// RefusedBackslash is a segment holding a backslash, which some
	// servers treat as a separator.
	RefusedBackslash
	// RefusedAbsoluteLeaf is a leaf starting with "/".
	RefusedAbsoluteLeaf
	// RefusedRelativeRoot is a root not starting with "/".
	RefusedRelativeRoot
	// RefusedNotCanonical is a path path.Clean would rewrite.
	RefusedNotCanonical
	// RefusedDotSegment is a "." or ".." segment in a leaf.
	RefusedDotSegment
	// RefusedOutsideRoot is a joined path that does not lie below its
	// root. Resolve's other rules make this unreachable; it is checked
	// anyway, so a future change to them fails closed.
	RefusedOutsideRoot
)

// String explains the refusal in a clause an error message can end
// with.
func (r Refusal) String() string {
	switch r {
	case RefusedEmpty:
		return "it is empty"
	case RefusedTooLong:
		return fmt.Sprintf("it is longer than %d bytes", MaxPathBytes)
	case RefusedSegmentTooLong:
		return fmt.Sprintf("a segment is longer than %d bytes", MaxSegmentBytes)
	case RefusedInvalidUTF8:
		return "it is not valid UTF-8"
	case RefusedControl:
		return "it holds a control or invisible format character"
	case RefusedBackslash:
		return `it holds a backslash, which a Windows SSH server treats as a separator`
	case RefusedAbsoluteLeaf:
		return "it is absolute: a transfer path is relative to its root"
	case RefusedRelativeRoot:
		return "it is relative: a transfer root must be absolute"
	case RefusedNotCanonical:
		return "it is not in its simplest form"
	case RefusedDotSegment:
		return `a "." or ".." segment is never allowed`
	case RefusedOutsideRoot:
		return "it resolves outside the transfer root"
	default:
		return "it is not allowed"
	}
}

// PathError is Resolve's and ValidateRoot's refusal. It names the part
// of the path at fault and, where one segment is to blame, that
// segment, so an operator reading it knows exactly which component to
// fix. Every value is rendered with %q, which escapes NUL, control
// bytes and invalid UTF-8 rather than printing them raw into a log or a
// terminal.
type PathError struct {
	// Part says whether the root or the leaf was refused.
	Part PathPart
	// Value is the whole root or leaf as it was given.
	Value string
	// Segment is the one segment at fault, or empty when the refusal is
	// about the whole value.
	Segment string
	// Canonical is path.Clean's spelling of Value when Refusal is
	// RefusedNotCanonical, offered as the fix.
	Canonical string
	// Refusal is why the path was refused.
	Refusal Refusal
}

// Error renders the refusal as one sentence.
func (e *PathError) Error() string {
	msg := fmt.Sprintf("filexfer: %s %q refused", e.Part, e.Value)
	if e.Segment != "" {
		msg += fmt.Sprintf(" at segment %q", e.Segment)
	}
	msg += ": " + e.Refusal.String()
	if e.Refusal == RefusedNotCanonical && e.Canonical != "" {
		msg += fmt.Sprintf("; write it as %q", e.Canonical)
	}
	return msg
}

// Containment is why a physical containment check refused a transfer.
type Containment uint8

const (
	// ContainmentRootMissing is a root the device could not resolve.
	ContainmentRootMissing Containment = iota + 1
	// ContainmentParentMissing is a target whose parent directory the
	// device could not resolve.
	ContainmentParentMissing
	// ContainmentOutsideRoot is a parent directory that physically lies
	// outside the physical root, which a symlink inside the root causes.
	ContainmentOutsideRoot
	// ContainmentSymlinkLeaf is a target that is itself a symlink.
	ContainmentSymlinkLeaf
	// ContainmentDirectoryLeaf is a target that is a directory.
	ContainmentDirectoryLeaf
	// ContainmentNotRegular is a target that exists and is not a
	// regular file: a FIFO would block a Get forever, and a device node
	// is never a legitimate transfer target.
	ContainmentNotRegular
	// ContainmentUntrustedAnswer is a device answer that is not an
	// absolute, canonical path, which is refused rather than compared.
	ContainmentUntrustedAnswer
)

// String explains the containment refusal.
func (c Containment) String() string {
	switch c {
	case ContainmentRootMissing:
		return "the transfer root does not exist on the device"
	case ContainmentParentMissing:
		return "the parent directory does not exist on the device"
	case ContainmentOutsideRoot:
		return "the parent directory physically lies outside the transfer root (a symlink points out of it)"
	case ContainmentSymlinkLeaf:
		return "the target is a symlink, which is never followed"
	case ContainmentDirectoryLeaf:
		return "the target is a directory"
	case ContainmentNotRegular:
		return "the target is not a regular file"
	case ContainmentUntrustedAnswer:
		return "the device answered with a path that is not absolute and canonical"
	default:
		return "the target is not contained in the transfer root"
	}
}

// ContainmentError is a Store's refusal after asking the device where a
// path physically is. It is returned before any content byte moves.
type ContainmentError struct {
	// Path is the resolved path the caller asked for.
	Path string
	// Resolved is what the device said the relevant directory
	// physically is, when it said anything.
	Resolved string
	// Root is what the device said the transfer root physically is,
	// when it said anything.
	Root string
	// Reason is why the transfer was refused.
	Reason Containment
}

// Error renders the refusal as one sentence.
func (e *ContainmentError) Error() string {
	msg := fmt.Sprintf("filexfer: %q refused: %s", e.Path, e.Reason)
	if e.Resolved != "" || e.Root != "" {
		msg += fmt.Sprintf(" (device resolved %q against root %q)", e.Resolved, e.Root)
	}
	return msg
}
