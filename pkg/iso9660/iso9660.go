// Package iso9660 writes small ISO 9660 images that carry a Joliet
// directory tree, using the standard library only.
//
// It exists to build cloud-init NoCloud seeds: a volume labeled cidata
// whose root directory holds user-data, meta-data and network-config,
// which a VirtualBox host attaches to a guest as a DVD. Linux's iso9660
// driver, blkid and cloud-init all read the result. Joliet is what makes
// that work. A plain ISO 9660 file identifier may hold only upper-case
// letters, digits and '_' (the d-characters of ECMA-119 7.4.1), so it
// cannot spell user-data. A Joliet tree records the same files under
// their real names, and Linux reads the Joliet tree by default.
//
// # Scope
//
// The writer is deliberately small. Write refuses anything outside these
// limits with an error, rather than writing an image a reader might
// misread:
//
//   - Files live in the root directory. There are no subdirectories.
//   - At most MaxFiles files, each at most MaxFileSize bytes, and at most
//     MaxTotalSize bytes of file data in all.
//   - A file name is 1 to MaxNameLength characters of ASCII letters,
//     digits, '.', '_' and '-'. It must not end in '.', because Linux and
//     Windows both strip a trailing '.' from a Joliet name, and names must
//     be unique ignoring case, because Windows compares them that way.
//   - The volume identifier is 1 to MaxVolumeIDLength characters of ASCII
//     letters, digits and '_'. The primary descriptor records it
//     upper-cased and the Joliet descriptor records it as given. The limit
//     is 16 rather than the 32 bytes the field holds, because Joliet spends
//     two bytes per character in the same field, so a longer identifier
//     could not be recorded as given.
//   - The timestamp's year, in UTC, is 1900 through 2155: the range of a
//     directory record's one-byte year (ECMA-119 9.1.5).
//
// Each file's ISO 9660 identifier is derived from its Joliet name:
// upper-cased, with every character that is not a d-character mapped to
// '_', split at the last '.' into a name and an extension, shortened to
// the 30 characters ECMA-119 7.5.1 allows the two together, and given a
// numeric suffix when it would collide with another file's. A reader that
// ignores Joliet shows these names; nothing in this package's intended use
// does.
//
// # Layout
//
// Every image has the same shape, in 2048-byte sectors:
//
//	0-15   system area, all zero
//	16     primary volume descriptor (ECMA-119 8.4)
//	17     Joliet supplementary volume descriptor (ECMA-119 8.5)
//	18     volume descriptor set terminator (ECMA-119 8.3)
//	19-20  primary tree's type L and type M path tables
//	21-22  Joliet tree's type L and type M path tables
//	23-    primary root directory, then the Joliet root directory
//	then   each file's data, starting on a sector boundary
//
// Both trees point at the same file data, so each file is stored once.
// An empty file records the sector where the next file's data begins, as
// mkisofs does, because some readers drop a file whose extent is sector 0.
//
// # Determinism
//
// Every timestamp in the image is the time passed to Write, converted to
// UTC, and files are ordered by name rather than by their position in the
// slice. The same label, time, names and contents therefore always give
// byte-identical images.
//
// Section numbers in comments refer to ECMA-119, 2nd edition (December
// 1987), the text of ISO 9660:1988. The Joliet fields follow Microsoft's
// Joliet Specification (1995).
package iso9660

import (
	"errors"
	"fmt"
	"io"
	"time"
)

// Limits on what one image may hold. Write refuses input past any of them.
const (
	// SectorSize is the logical sector size and the logical block size,
	// in bytes, of every image this package writes.
	SectorSize = 2048
	// MaxFiles is the most files one image may hold.
	MaxFiles = 64
	// MaxFileSize is the largest one file may be, in bytes (16 MiB).
	MaxFileSize = 16 << 20
	// MaxTotalSize is the most file data one image may hold, in bytes
	// (64 MiB).
	MaxTotalSize = 64 << 20
	// MaxNameLength is the longest file name, in characters. It is the
	// Joliet limit: 64 UCS-2 characters, or 128 bytes.
	MaxNameLength = 64
	// MaxVolumeIDLength is the longest volume identifier, in characters.
	MaxVolumeIDLength = 16
)

// File is one file to place in the image's root directory.
type File struct {
	// Name is the file's name as a Joliet reader shows it, for example
	// "user-data".
	Name string
	// Data is the file's contents. Write reads it and never modifies it.
	Data []byte
}

// Write writes a complete image holding files to w, with volumeID as its
// label and modified as every timestamp it records.
//
// Every limit in the package documentation is checked before the first
// byte is written, so a refused input leaves w untouched. An error from w
// itself is returned wrapped, and whatever was written before it stays
// written.
func Write(w io.Writer, volumeID string, modified time.Time, files []File) error {
	if w == nil {
		return errors.New("iso9660: nil writer")
	}
	p, err := newPlan(volumeID, modified, files)
	if err != nil {
		return err
	}
	return p.write(w)
}

// write emits the planned image: every metadata sector from one buffer,
// then each file's data padded with zeros to a whole sector, in the order
// the plan assigned their extents.
func (p *plan) write(w io.Writer) error {
	if _, err := w.Write(p.metadata()); err != nil {
		return fmt.Errorf("iso9660: writing the volume metadata: %w", err)
	}
	pad := make([]byte, SectorSize)
	for _, e := range p.entries {
		// An empty file owns no sectors, so there is nothing to write.
		if len(e.data) == 0 {
			continue
		}
		if _, err := w.Write(e.data); err != nil {
			return fmt.Errorf("iso9660: writing %q: %w", e.name, err)
		}
		// Pad the last partial sector so the next extent starts aligned.
		if rem := len(e.data) % SectorSize; rem != 0 {
			if _, err := w.Write(pad[:SectorSize-rem]); err != nil {
				return fmt.Errorf("iso9660: padding %q: %w", e.name, err)
			}
		}
	}
	return nil
}
