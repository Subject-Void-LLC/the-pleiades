// Shared fixtures for package iso9660's tests: a cloud-init NoCloud seed
// shaped file set, the fixed time every image is stamped with, and small
// decoders the structural tests use to read fields back out of an image.
// The independent checks (blkid, xorriso, bsdtar and the Linux kernel)
// use the fixtures but never these decoders.
package iso9660_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/iso9660"
)

// seedLabel is the volume label cloud-init's NoCloud source looks for.
const seedLabel = "cidata"

// longName is a Joliet name of exactly iso9660.MaxNameLength characters,
// long enough that its derived ISO 9660 identifier must be shortened.
const longName = "a-long-file-name-that-uses-all-sixty-four-joliet-characters.text"

// seedTime is the timestamp every test image is written with. The
// hundredths (78) are set so a test can see they reach the volume dates.
var seedTime = time.Date(2026, time.September, 27, 12, 34, 56, 780_000_000, time.UTC)

// seedUUID is what blkid derives from seedTime: libblkid builds an
// iso9660 volume's UUID from its modification date.
const seedUUID = "2026-09-27-12-34-56-78"

// seedFiles returns the test file set: a NoCloud seed's three files plus
// an empty vendor-data and one maximum-length name. The sizes are chosen
// for their edge cases: user-data spans three sectors, network-config is
// exactly one sector (no padding), and vendor-data is empty and sorts
// last, so its extent is the one clamped inside the volume.
func seedFiles(t testing.TB) []iso9660.File {
	t.Helper()
	if len(longName) != iso9660.MaxNameLength {
		t.Fatalf("longName is %d characters, want %d", len(longName), iso9660.MaxNameLength)
	}
	// Numbered lines make any shifted or misplaced sector visible as a
	// content mismatch, which a repeated pattern could hide.
	var user bytes.Buffer
	user.WriteString("#cloud-config\n")
	for i := 0; user.Len() < 5000; i++ {
		fmt.Fprintf(&user, "# line %04d of a user-data file longer than one sector\n", i)
	}
	network := []byte("version: 2\nethernets:\n  eth0:\n    dhcp4: true\n")
	network = append(network, bytes.Repeat([]byte("#"), iso9660.SectorSize-len(network)-1)...)
	network = append(network, '\n')
	return []iso9660.File{
		{Name: "user-data", Data: user.Bytes()},
		{Name: "meta-data", Data: []byte("instance-id: iid-seed-test\nlocal-hostname: seed-test\n")},
		{Name: "network-config", Data: network},
		{Name: "vendor-data", Data: nil},
		{Name: longName, Data: []byte("the longest name a Joliet tree records\n")},
	}
}

// seedPrimaryIDs are the ISO 9660 identifiers the seed files must get,
// worked out by hand from the rules in the package documentation rather
// than by calling the code under test.
var seedPrimaryIDs = map[string]string{
	"user-data":      "USER_DATA.;1",
	"meta-data":      "META_DATA.;1",
	"network-config": "NETWORK_CONFIG.;1",
	"vendor-data":    "VENDOR_DATA.;1",
	longName:         "A_LONG_FILE_NAME_THAT_USES.TEXT;1",
}

// writeImage writes files into a fresh image and fails the test on error.
func writeImage(t testing.TB, volumeID string, files []iso9660.File) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := iso9660.Write(&buf, volumeID, seedTime, files); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.Bytes()
}

// sectorAt returns sector n of img.
func sectorAt(img []byte, n uint32) []byte {
	return img[int(n)*iso9660.SectorSize : int(n+1)*iso9660.SectorSize]
}

// both16 reads a both-byte-order 16-bit field and fails if its two
// halves disagree.
func both16(t testing.TB, b []byte) uint16 {
	t.Helper()
	le, be := binary.LittleEndian.Uint16(b), binary.BigEndian.Uint16(b[2:])
	if le != be {
		t.Fatalf("both-byte-order field disagrees: little-endian %d, big-endian %d", le, be)
	}
	return le
}

// both32 reads a both-byte-order 32-bit field and fails if its two
// halves disagree.
func both32(t testing.TB, b []byte) uint32 {
	t.Helper()
	le, be := binary.LittleEndian.Uint32(b), binary.BigEndian.Uint32(b[4:])
	if le != be {
		t.Fatalf("both-byte-order field disagrees: little-endian %d, big-endian %d", le, be)
	}
	return le
}

// record is one directory record as the tests decode it.
type record struct {
	length int
	id     []byte
	sector uint32
	size   uint32
	flags  byte
	date   []byte
}

// decodeRecord decodes the directory record at the start of b.
func decodeRecord(t testing.TB, b []byte) record {
	t.Helper()
	n := int(b[0])
	idLen := int(b[32])
	if want := 33 + idLen + (33+idLen)%2; n != want {
		t.Fatalf("record length %d, want %d for a %d-byte identifier", n, want, idLen)
	}
	if seq := both16(t, b[28:32]); seq != 1 {
		t.Fatalf("volume sequence number %d, want 1", seq)
	}
	return record{
		length: n,
		id:     b[33 : 33+idLen],
		sector: both32(t, b[2:10]),
		size:   both32(t, b[10:18]),
		flags:  b[25],
		date:   b[18:25],
	}
}

// readDirectory decodes every record of the directory extent at sector,
// size bytes long. It fails if a record crosses a sector boundary or if
// a sector's unused tail is not zero.
func readDirectory(t testing.TB, img []byte, sector, size uint32) []record {
	t.Helper()
	if size%iso9660.SectorSize != 0 {
		t.Fatalf("directory size %d is not a whole number of sectors", size)
	}
	var out []record
	for s := uint32(0); s < size/iso9660.SectorSize; s++ {
		sec := sectorAt(img, sector+s)
		off := 0
		for off < len(sec) && sec[off] != 0 {
			if off+int(sec[off]) > len(sec) {
				t.Fatalf("record at sector %d offset %d crosses the sector boundary", sector+s, off)
			}
			r := decodeRecord(t, sec[off:])
			out = append(out, r)
			off += r.length
		}
		if !bytes.Equal(sec[off:], make([]byte, len(sec)-off)) {
			t.Fatalf("sector %d has non-zero bytes after its last record", sector+s)
		}
	}
	return out
}

// jolietName decodes a UCS-2 big-endian identifier made of ASCII.
func jolietName(t testing.TB, id []byte) string {
	t.Helper()
	if len(id)%2 != 0 {
		t.Fatalf("Joliet identifier %x has odd length", id)
	}
	var s []byte
	for i := 0; i < len(id); i += 2 {
		if id[i] != 0 {
			t.Fatalf("Joliet identifier %x has a non-ASCII code unit", id)
		}
		s = append(s, id[i+1])
	}
	return string(s)
}
