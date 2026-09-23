// Package filexfer is the shared port through which this platform moves
// a file's bytes to and from a device, independent of which protocol
// carries them: SFTP (pkg/sftpxfer) and legacy SCP (pkg/scpxfer).
//
// # Why a port of its own, and not transport.Transport
//
// transport.Transport's Exec(ctx, target, cred, command string)
// (Result, error) returns standard output and standard error as Go
// strings. That is the right shape for a command and the wrong one for
// a file in both directions: a firmware image is binary, it can be
// gigabytes long, and a string return means holding every byte of it in
// memory before the caller sees the first one. Widening Exec with Put
// and Get would be worse still, because every existing implementation
// (SSH, serial, serial-over-TCP, Telnet) would then have to answer a
// question about file transfer it was never asked.
// pkg/catalystcenter/client.go recorded the same reasoning for a REST
// API, and pkg/datastore for NETCONF: a protocol whose shape is not a
// command string gets a narrow port of its own. A future reviewer facing
// a third protocol that fits neither shape should reach the same
// answer: another narrow port, never a widened Exec.
//
// # What the port carries, and what it deliberately does not
//
// Store carries exactly what SFTP and SCP genuinely share: put a file's
// bytes at a path, and get them back. Put takes the byte count up front
// because SCP's wire protocol announces it before the first content
// byte, and discovering it would mean spooling a multi-gigabyte stream
// to disk first. Every Store then enforces it both ways: a source that
// yields fewer or more bytes than declared fails the transfer, and
// nothing replaces the target.
//
// Stat is NOT on Store. Legacy SCP has no stat operation, and
// pkg/tftpxfer, the likeliest next protocol to join, has none either. A
// method on an interface that some implementations cannot honor
// type-checks and then fails per device at run time, which is worse
// than an absent method. So Stat lives on the separate Stater
// interface, reached by a type assertion at the call site, the same
// idiom pkg/datastore uses for NETCONF's Commit and
// internal/engine/collection_action.go uses for FactCollector.
//
// # Paths are resolved before anything is dialed
//
// A remote path is built from two parties' input: the root comes from
// inventory (capability.FileTransferCapable's FileTransferRoot) and the
// leaf from a runbook. Resolve is the only way to build a Path, it needs
// no connection, and every Store refuses the zero Path. So a leaf that
// would climb out of its root is refused before any network call, by
// construction rather than by each caller remembering to check. The
// resolver uses the path package, never path/filepath: the remote
// separator is always "/", whatever operating system the controller
// runs on, and a guard written with filepath would pass on a Linux
// controller and fail open on a Windows one.
//
// A lexical check confines a name, not a file: a symlink inside the
// root can still point outside it. Each Store therefore also asks the
// device itself where the root and the target's parent directory
// physically are, and Contained compares those answers, before any
// content byte moves.
//
// This package is under pkg/, not internal/, because a Collection
// method may import only pkg/ (internal/archtest's
// TestPkgNeverImportsInternal). It imports nothing outside the standard
// library and nothing that opens a file or a socket, which
// internal/archtest's TestFileTransferPortImportsNoIO asserts.
package filexfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"time"
)

// Store is the file-transfer port: put a file's bytes at a resolved
// path on a device, or get them back.
type Store interface {
	// Put writes exactly size bytes read from src to dst, replacing any
	// existing file there atomically: a reader of dst sees either the
	// old file or the complete new one, never a partial one. mode sets
	// the new file's permission bits exactly, regardless of the remote
	// account's umask. A src that yields fewer or more than size bytes
	// fails with ErrSizeMismatch and leaves dst untouched.
	Put(ctx context.Context, dst Path, src io.Reader, size int64, mode Mode) error

	// Get copies the regular file at src to dst and returns how many
	// bytes it wrote. A file larger than limit fails with
	// ErrLimitExceeded, before any byte is written when the size is
	// known up front.
	Get(ctx context.Context, src Path, dst io.Writer, limit int64) (int64, error)
}

// Stater is the optional interface a Store implements when its protocol
// can describe a remote file without transferring it. See the package
// doc for why it is not part of Store.
type Stater interface {
	// Stat describes the file at p without following a symlink there,
	// so a symlink is reported as KindSymlink rather than as whatever it
	// points to.
	Stat(ctx context.Context, p Path) (Info, error)
}

// Mode is a remote file's permission bits. Only the nine rwx bits are
// valid: setuid, setgid and the sticky bit are refused rather than
// passed through, because a transfer that could create a setuid file is
// a privilege escalation waiting for a typo. A decimal typo is caught
// the same way, since Mode(644) is 0o1204, which has the sticky bit set.
type Mode uint32

// Valid reports whether m holds only the nine permission bits.
func (m Mode) Valid() bool { return m&^0o777 == 0 }

// Perm returns m as an fs.FileMode, for a protocol library that wants
// one. Callers should check Valid first.
func (m Mode) Perm() fs.FileMode { return fs.FileMode(m) & fs.ModePerm }

// String renders m as a four-digit octal number, the form both chmod
// and SCP's wire header use.
func (m Mode) String() string { return fmt.Sprintf("%04o", uint32(m)) }

// Kind is what sort of directory entry a remote path names.
type Kind uint8

const (
	// KindRegular is a regular file, the only kind Get will read and
	// the only kind Put will replace.
	KindRegular Kind = iota + 1
	// KindDirectory is a directory.
	KindDirectory
	// KindSymlink is a symbolic link, which no Store follows at the
	// final path component.
	KindSymlink
	// KindOther is anything else: a FIFO, a socket, a device node.
	KindOther
)

// String returns the kind's lowercase name.
func (k Kind) String() string {
	switch k {
	case KindRegular:
		return "regular file"
	case KindDirectory:
		return "directory"
	case KindSymlink:
		return "symlink"
	case KindOther:
		return "special file"
	default:
		return "unknown kind"
	}
}

// Info describes a remote file, as Stater.Stat reports it.
type Info struct {
	// Size is the file's length in bytes.
	Size int64
	// Mode is the file's nine permission bits.
	Mode Mode
	// Kind is what sort of entry the path names.
	Kind Kind
	// ModTime is the file's last modification time as the device
	// reports it.
	ModTime time.Time
}

var (
	// ErrUnresolvedPath is returned for the zero Path, one that did not
	// come from Resolve. It is refused before any network call.
	ErrUnresolvedPath = errors.New("filexfer: path was not built by Resolve")

	// ErrSizeMismatch is returned when Put's source yields fewer or more
	// bytes than the size it declared.
	ErrSizeMismatch = errors.New("filexfer: source size does not match the declared size")

	// ErrLimitExceeded is returned when Get's file is larger than the
	// caller's limit.
	ErrLimitExceeded = errors.New("filexfer: remote file is larger than the limit")

	// ErrReplaceUnsupported is returned when the server cannot replace
	// an existing file atomically, so Put refuses rather than writing it
	// in place.
	ErrReplaceUnsupported = errors.New("filexfer: server cannot replace an existing file atomically")

	// ErrInvalidMode is returned for a Mode with bits outside 0o777.
	ErrInvalidMode = errors.New("filexfer: mode must hold only the nine permission bits")
)

// CheckPut validates Put's arguments before any network call: a
// resolved destination, a non-negative size and a valid mode. Every
// Store calls it first, so the refusals read the same whichever
// protocol a caller chose.
func CheckPut(dst Path, size int64, mode Mode) error {
	if dst.IsZero() {
		return ErrUnresolvedPath
	}
	if size < 0 {
		return fmt.Errorf("%w: size %d is negative", ErrSizeMismatch, size)
	}
	if !mode.Valid() {
		return fmt.Errorf("%w: got %s", ErrInvalidMode, mode)
	}
	return nil
}

// CheckGet validates Get's arguments before any network call: a
// resolved source and a non-negative limit.
func CheckGet(src Path, limit int64) error {
	if src.IsZero() {
		return ErrUnresolvedPath
	}
	if limit < 0 {
		return fmt.Errorf("%w: limit %d is negative", ErrLimitExceeded, limit)
	}
	return nil
}
