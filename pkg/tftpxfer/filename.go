// Remote filename rules: what one TFTP request can carry whole, and what a
// server or a log could read as something else.
package tftpxfer

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// requestBytes is the buffer github.com/pin/tftp/v3 packs every read or
// write request into (its datagramLength). It copies the filename, the
// mode and each option there with no bounds check of its own.
const requestBytes = 516

// MaxFilenameBytes is the longest remote filename, in bytes, that one
// request can carry whole beside the octet mode and the longest block size
// option this package sends. Every field of a request ends in a NUL:
//
//	516 - 2 (opcode)
//	    - 1 (the filename's NUL)
//	    - 6 ("octet" and its NUL)
//	    - 8 ("blksize" and its NUL)
//	    - 6 ("65464", MaxBlockSize, and its NUL) = 493
//
// Past it, pin/tftp indexes beyond its buffer and panics when a block size
// is set, and without one it cuts the name short, which requests a
// different file.
const MaxFilenameBytes = requestBytes - 2 - 1 - (len(mode) + 1) - (len("blksize") + 1) - (len("65464") + 1)

// ErrInvalidFilename is wrapped by every refusal of a remote filename, all
// of them made before anything is sent, so a caller can tell a name that
// can never be requested from a network failure worth retrying.
var ErrInvalidFilename = errors.New("invalid remote filename")

// validateFilename refuses a remote filename that one request cannot carry
// whole, or that a server or a log could read as something else. See the
// package doc comment for what this does and does not defend against.
func validateFilename(filename string) error {
	if filename == "" {
		return fmt.Errorf("%w: it must not be empty", ErrInvalidFilename)
	}
	// Checked before anything else echoes the name, so a refusal never
	// repeats an unbounded one into a log.
	if len(filename) > MaxFilenameBytes {
		return fmt.Errorf("%w: it is %d bytes, over the %d one request can carry",
			ErrInvalidFilename, len(filename), MaxFilenameBytes)
	}
	if !utf8.ValidString(filename) {
		return fmt.Errorf("%w %q: it is not valid UTF-8", ErrInvalidFilename, filename)
	}
	for _, r := range filename {
		// The same character rule pkg/filexfer applies to a path segment.
		// IsControl covers NUL, which ends the filename in a request, so one
		// inside a name would make what follows it the transfer mode or an
		// option; it also covers every other C0 control, DEL and the C1
		// range. Cf covers the invisible format characters (a right-to-left
		// override, zero-width joiners) that can make a log lie about which
		// file moved.
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return fmt.Errorf("%w %q: it contains the control or format character %U",
				ErrInvalidFilename, filename, r)
		}
	}
	if strings.HasPrefix(filename, "/") || strings.HasPrefix(filename, `\`) {
		return fmt.Errorf("%w %q: it must not be an absolute path", ErrInvalidFilename, filename)
	}
	// Refuses a Windows drive-letter path (e.g. "C:\Windows") too: ":"
	// is never meaningful in a legitimate TFTP filename, and without
	// this check such a path slips past the leading-slash test above
	// entirely -- found by this package's own test suite hitting a real
	// ~30s network retry against an unreachable port instead of an
	// instant refusal, exactly the gap this check now closes.
	if strings.Contains(filename, ":") {
		return fmt.Errorf("%w %q: it must not contain \":\"", ErrInvalidFilename, filename)
	}
	// A backslash stays allowed, since Windows TFTP servers (tftpd32 among
	// them) treat it as a separator; that is also why ".." is refused
	// under either separator.
	for _, seg := range strings.FieldsFunc(filename, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return fmt.Errorf("%w %q: it must not contain a \"..\" path segment", ErrInvalidFilename, filename)
		}
	}
	return nil
}
