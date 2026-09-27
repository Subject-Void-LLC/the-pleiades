// Byte-level encoding for package iso9660: number fields, dates, text
// fields, directory records, path tables and volume descriptors, as
// ECMA-119 and the Joliet Specification define them.
package iso9660

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// Volume descriptor types (ECMA-119 8.1.1).
const (
	typePrimary       = 1
	typeSupplementary = 2
	typeTerminator    = 255
)

// standardID is the Standard Identifier every volume descriptor carries
// (ECMA-119 8.1.2).
const standardID = "CD001"

// jolietEscape is the escape sequence that marks a supplementary volume
// descriptor as Joliet, UCS-2 level 3 (ECMA-119 8.5.6 and the Joliet
// Specification).
const jolietEscape = "%/E"

// Directory record constants (ECMA-119 9.1).
const (
	// recordHeaderLen is the fixed part of a directory record, before its
	// file identifier.
	recordHeaderLen = 33
	// flagDirectory is the File Flags bit that marks a directory
	// (ECMA-119 9.1.6).
	flagDirectory = 0x02
)

// pathTableLen is the size of a path table that holds only the root: an
// 8-byte header, a 1-byte identifier and a padding byte (ECMA-119 9.4).
const pathTableLen = 10

// Identifiers of the two records every directory begins with (ECMA-119
// 6.8.2.2): the directory itself and its parent. The root is its own
// parent.
var (
	selfID   = []byte{0}
	parentID = []byte{1}
)

// unspecifiedDate is a 17-byte volume date meaning "not specified": sixteen
// '0' digits and a zero offset (ECMA-119 8.4.26.1).
const unspecifiedDate = "0000000000000000\x00"

// put723 records v in both byte orders, little-endian first (ECMA-119
// 7.2.3).
func put723(b []byte, v uint16) {
	binary.LittleEndian.PutUint16(b, v)
	binary.BigEndian.PutUint16(b[2:], v)
}

// put733 records v in both byte orders, little-endian first (ECMA-119
// 7.3.3).
func put733(b []byte, v uint32) {
	binary.LittleEndian.PutUint32(b, v)
	binary.BigEndian.PutUint32(b[4:], v)
}

// u32 converts a sector number or byte count to the 32-bit field that
// records it. The package limits keep every such value far below 1<<32,
// so a value outside that range is a bug in this package, not bad input.
func u32(n int) uint32 {
	if n < 0 || n > math.MaxUint32 {
		panic(fmt.Sprintf("iso9660: %d does not fit a 32-bit field", n))
	}
	return uint32(n)
}

// u8 converts a length or calendar field to the one byte that records it.
// Every caller's value is bounded before it arrives here, so a value
// outside 0 through 255 is a bug in this package, not bad input.
func u8(n int) byte {
	if n < 0 || n > math.MaxUint8 {
		panic(fmt.Sprintf("iso9660: %d does not fit a one-byte field", n))
	}
	return byte(n)
}

// recordDate is the 7-byte date a directory record carries (ECMA-119
// 9.1.5): years since 1900, month, day, hour, minute, second and an
// offset from UTC in 15-minute steps, which is zero because t is UTC.
func recordDate(t time.Time) [7]byte {
	return [7]byte{
		u8(t.Year() - minYear), u8(int(t.Month())), u8(t.Day()),
		u8(t.Hour()), u8(t.Minute()), u8(t.Second()), 0,
	}
}

// volumeDate is the 17-byte date a volume descriptor carries (ECMA-119
// 8.4.26.1): sixteen ASCII digits for year, month, day, hour, minute,
// second and hundredths, then a zero offset from UTC because t is UTC.
func volumeDate(t time.Time) []byte {
	d := fmt.Appendf(nil, "%04d%02d%02d%02d%02d%02d%02d",
		t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/1e7)
	return append(d, 0)
}

// aText fills a primary descriptor text field with s, padded with spaces
// as ECMA-119 8.4 requires of its a- and d-character fields.
func aText(dst []byte, s string) {
	n := copy(dst, s)
	for i := n; i < len(dst); i++ {
		dst[i] = ' '
	}
}

// ucs2Text fills a Joliet descriptor text field with s as UCS-2
// big-endian, padded with UCS-2 spaces (0x00 0x20). A field of odd length
// ends in one zero byte, which is what libisofs writes too.
func ucs2Text(dst []byte, s string) {
	n := copy(dst, ucs2(s))
	for i := n; i+1 < len(dst); i += 2 {
		dst[i], dst[i+1] = 0, ' '
	}
	if len(dst)%2 == 1 {
		dst[len(dst)-1] = 0
	}
}

// ucs2 encodes an ASCII string as UCS-2 big-endian, the Joliet character
// set. Every string reaching it has passed checkName or checkVolumeID, so
// each byte is ASCII and maps to one code unit.
func ucs2(s string) []byte {
	out := make([]byte, 0, 2*len(s))
	for i := 0; i < len(s); i++ {
		out = append(out, 0, s[i])
	}
	return out
}

// recordLen is the length of a directory record whose file identifier is
// idLen bytes. A padding byte follows an identifier of even length, so
// the record's own length is always even (ECMA-119 9.1.12).
func recordLen(idLen int) int {
	n := recordHeaderLen + idLen
	return n + n%2
}

// dirRecord encodes one directory record (ECMA-119 9.1).
func dirRecord(id []byte, data extent, flags byte, date [7]byte) []byte {
	r := make([]byte, recordLen(len(id)))
	r[0] = u8(len(r))                 // 9.1.1 Length of Directory Record
	put733(r[2:10], u32(data.sector)) // 9.1.3 Location of Extent; r[1], 9.1.2, stays 0
	put733(r[10:18], u32(data.size))  // 9.1.4 Data Length
	copy(r[18:25], date[:])           // 9.1.5 Recording Date and Time
	r[25] = flags                     // 9.1.6 File Flags; 9.1.7 and 9.1.8 stay 0 (not interleaved)
	put723(r[28:32], 1)               // 9.1.9 Volume Sequence Number
	r[32] = u8(len(id))               // 9.1.10 Length of File Identifier
	copy(r[33:], id)                  // 9.1.11 File Identifier; the 9.1.12 pad byte stays 0
	return r
}

// dirLayout places a root directory's records: "." and "..", then each
// file record. A record that would cross a sector boundary starts the
// next sector instead, since each record must end in the sector it
// begins in (ECMA-119 6.8.1.1). It returns each record's byte offset and
// the directory's length in whole sectors.
func dirLayout(files []fileRecord) (offsets []int, sectors int) {
	lengths := []int{recordLen(len(selfID)), recordLen(len(parentID))}
	for _, f := range files {
		lengths = append(lengths, recordLen(len(f.id)))
	}
	off := 0
	for _, n := range lengths {
		if used := off % SectorSize; used+n > SectorSize {
			off += SectorSize - used
		}
		offsets = append(offsets, off)
		off += n
	}
	return offsets, (off + SectorSize - 1) / SectorSize
}

// dirSectors is how many sectors a root directory holding files needs.
func dirSectors(files []fileRecord) int {
	_, n := dirLayout(files)
	return n
}

// directory encodes a root directory extent. The unused bytes after the
// last record in each sector stay zero, as ECMA-119 6.8.1.1 requires.
func directory(root extent, files []fileRecord, date [7]byte) []byte {
	offsets, sectors := dirLayout(files)
	out := make([]byte, sectors*SectorSize)
	copy(out[offsets[0]:], dirRecord(selfID, root, flagDirectory, date))
	copy(out[offsets[1]:], dirRecord(parentID, root, flagDirectory, date))
	for i, f := range files {
		copy(out[offsets[i+2]:], dirRecord(f.id, f.data, 0, date))
	}
	return out
}

// pathTable encodes a path table that holds only the root directory
// (ECMA-119 9.4): little-endian for a type L table, big-endian for type M.
func pathTable(rootSector int, order binary.ByteOrder) []byte {
	t := make([]byte, pathTableLen)
	t[0] = 1                                 // 9.4.1 Length of Directory Identifier
	order.PutUint32(t[2:6], u32(rootSector)) // 9.4.3 Location of Extent; t[1], 9.4.2, stays 0
	order.PutUint16(t[6:8], 1)               // 9.4.4 Parent Directory Number: the root is its own
	// t[8] is the root's identifier, a single zero byte, and t[9] pads
	// the record to an even length (9.4.5 and 9.4.6).
	return t
}

// terminator encodes the volume descriptor set terminator (ECMA-119 8.3)
// into dst, a zeroed sector.
func terminator(dst []byte) {
	dst[0] = typeTerminator
	copy(dst[1:6], standardID)
	dst[6] = 1
}

// descriptor encodes tree t's volume descriptor into dst, a zeroed
// sector: the primary volume descriptor (ECMA-119 8.4) for the primary
// tree, a Joliet supplementary volume descriptor (ECMA-119 8.5) for the
// Joliet one. The two share one layout and differ in their type, their
// escape sequences and how their text fields are coded. Fields this
// package has no value for are recorded blank or unspecified.
func (p *plan) descriptor(dst []byte, t tree, date [7]byte) {
	text, volumeID := aText, dChars(p.volumeID)
	dst[0] = typePrimary
	if t.joliet {
		text, volumeID = ucs2Text, p.volumeID
		dst[0] = typeSupplementary
		copy(dst[88:120], jolietEscape) // 8.5.6 Escape Sequences
	}
	copy(dst[1:6], standardID)                                         // 8.4.2 Standard Identifier
	dst[6] = 1                                                         // 8.4.3 Volume Descriptor Version; dst[7] (8.5.3 Volume Flags) stays 0
	text(dst[8:40], "")                                                // 8.4.5 System Identifier
	text(dst[40:72], volumeID)                                         // 8.4.6 Volume Identifier
	put733(dst[80:88], u32(p.sectors))                                 // 8.4.8 Volume Space Size
	put723(dst[120:124], 1)                                            // 8.4.10 Volume Set Size
	put723(dst[124:128], 1)                                            // 8.4.11 Volume Sequence Number
	put723(dst[128:132], SectorSize)                                   // 8.4.12 Logical Block Size
	put733(dst[132:140], pathTableLen)                                 // 8.4.13 Path Table Size
	binary.LittleEndian.PutUint32(dst[140:144], u32(t.lPath))          // 8.4.14 Type L Path Table (7.3.1); 8.4.15 stays 0
	binary.BigEndian.PutUint32(dst[148:152], u32(t.mPath))             // 8.4.16 Type M Path Table (7.3.2); 8.4.17 stays 0
	copy(dst[156:190], dirRecord(selfID, t.root, flagDirectory, date)) // 8.4.18 root directory record, 34 bytes
	for _, field := range [][2]int{
		{190, 318}, // 8.4.19 Volume Set Identifier
		{318, 446}, // 8.4.20 Publisher Identifier
		{446, 574}, // 8.4.21 Data Preparer Identifier
		{574, 702}, // 8.4.22 Application Identifier
		{702, 739}, // 8.4.23 Copyright File Identifier
		{739, 776}, // 8.4.24 Abstract File Identifier
		{776, 813}, // 8.4.25 Bibliographic File Identifier
	} {
		text(dst[field[0]:field[1]], "")
	}
	copy(dst[813:830], volumeDate(p.stamp)) // 8.4.26 Volume Creation Date and Time
	copy(dst[830:847], volumeDate(p.stamp)) // 8.4.27 Volume Modification Date and Time
	copy(dst[847:864], unspecifiedDate)     // 8.4.28 Volume Expiration Date and Time
	copy(dst[864:881], unspecifiedDate)     // 8.4.29 Volume Effective Date and Time
	dst[881] = 1                            // 8.4.30 File Structure Version
}
