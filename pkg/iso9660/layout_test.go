// Structural tests for package iso9660: every field the Layout section of
// the package documentation promises is read back out of a real image
// and checked against ECMA-119 and the Joliet Specification.
package iso9660_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/iso9660"
)

// seedRecordDate is seedTime as a directory record's 7-byte date:
// years since 1900, month, day, hour, minute, second, UTC offset.
var seedRecordDate = []byte{126, 9, 27, 12, 34, 56, 0}

// descriptorCase is what one volume descriptor must hold.
type descriptorCase struct {
	sector   uint32
	kind     byte
	volumeID []byte
	lPath    uint32
	mPath    uint32
	joliet   bool
}

// TestWriteLayout checks the system area, both volume descriptors, the
// terminator, both trees' path tables and root directories, and every
// file's extent in the seed image.
func TestWriteLayout(t *testing.T) {
	files := seedFiles(t)
	img := writeImage(t, seedLabel, files)
	if len(img)%iso9660.SectorSize != 0 {
		t.Fatalf("image is %d bytes, not a whole number of sectors", len(img))
	}
	if !bytes.Equal(img[:16*iso9660.SectorSize], make([]byte, 16*iso9660.SectorSize)) {
		t.Fatal("the system area, sectors 0-15, is not all zero")
	}

	jolietLabel := []byte{0, 'c', 0, 'i', 0, 'd', 0, 'a', 0, 't', 0, 'a'}
	jolietLabel = append(jolietLabel, bytes.Repeat([]byte{0, ' '}, 10)...)
	cases := []descriptorCase{
		{sector: 16, kind: 1, volumeID: []byte("CIDATA" + strings.Repeat(" ", 26)), lPath: 19, mPath: 20},
		{sector: 17, kind: 2, volumeID: jolietLabel, lPath: 21, mPath: 22, joliet: true},
	}
	byName := make(map[string][]byte, len(files))
	for _, f := range files {
		byName[f.Name] = f.Data
	}
	var shared [2]map[string][2]uint32
	for i, dc := range cases {
		root := checkDescriptor(t, img, dc)
		shared[i] = checkRootDirectory(t, img, root, dc.joliet, byName)
	}
	// Both trees must point every file at the same data extent.
	for name, where := range shared[1] {
		if got, ok := shared[0][name]; !ok || got != where {
			t.Errorf("%s: the primary tree records extent %v, the Joliet tree %v", name, got, where)
		}
	}

	term := sectorAt(img, 18)
	if term[0] != 255 || string(term[1:6]) != "CD001" || term[6] != 1 || !bytes.Equal(term[7:], make([]byte, len(term)-7)) {
		t.Fatal("sector 18 is not a volume descriptor set terminator")
	}
}

// checkDescriptor checks one volume descriptor and its two path tables
// and returns its root directory record.
func checkDescriptor(t *testing.T, img []byte, dc descriptorCase) record {
	t.Helper()
	d := sectorAt(img, dc.sector)
	if d[0] != dc.kind || string(d[1:6]) != "CD001" || d[6] != 1 || d[7] != 0 {
		t.Fatalf("sector %d: type %d, identifier %q, version %d, flags %d", dc.sector, d[0], d[1:6], d[6], d[7])
	}
	if !bytes.Equal(d[40:72], dc.volumeID) {
		t.Errorf("sector %d: volume identifier %q, want %q", dc.sector, d[40:72], dc.volumeID)
	}
	escape := make([]byte, 32)
	if dc.joliet {
		copy(escape, "%/E")
	}
	if !bytes.Equal(d[88:120], escape) {
		t.Errorf("sector %d: escape sequences %q, want %q", dc.sector, d[88:120], escape)
	}
	if got := both32(t, d[80:88]); int(got)*iso9660.SectorSize != len(img) {
		t.Errorf("sector %d: volume space size %d sectors, image is %d bytes", dc.sector, got, len(img))
	}
	for field, want := range map[string]uint16{"volume set size": 1, "volume sequence number": 1, "logical block size": 2048} {
		off := map[string]int{"volume set size": 120, "volume sequence number": 124, "logical block size": 128}[field]
		if got := both16(t, d[off:off+4]); got != want {
			t.Errorf("sector %d: %s %d, want %d", dc.sector, field, got, want)
		}
	}
	if got := both32(t, d[132:140]); got != 10 {
		t.Errorf("sector %d: path table size %d, want 10", dc.sector, got)
	}
	l, m := binary.LittleEndian.Uint32(d[140:144]), binary.BigEndian.Uint32(d[148:152])
	if l != dc.lPath || m != dc.mPath || !bytes.Equal(d[144:148], make([]byte, 4)) || !bytes.Equal(d[152:156], make([]byte, 4)) {
		t.Errorf("sector %d: path tables at L %d and M %d, want %d and %d with no optional copies", dc.sector, l, m, dc.lPath, dc.mPath)
	}
	root := decodeRecord(t, d[156:190])
	if root.length != 34 || !bytes.Equal(root.id, []byte{0}) || root.flags != 2 || !bytes.Equal(root.date, seedRecordDate) {
		t.Errorf("sector %d: root record %+v, want a 34-byte directory record named 0x00", dc.sector, root)
	}
	checkPathTable(t, sectorAt(img, l), binary.LittleEndian, root.sector)
	checkPathTable(t, sectorAt(img, m), binary.BigEndian, root.sector)
	for _, date := range [][2]int{{813, 830}, {830, 847}} {
		if got := string(d[date[0]:date[1]]); got != "2026092712345678\x00" {
			t.Errorf("sector %d: volume date %q, want seedTime", dc.sector, got)
		}
	}
	for _, date := range [][2]int{{847, 864}, {864, 881}} {
		if got := string(d[date[0]:date[1]]); got != "0000000000000000\x00" {
			t.Errorf("sector %d: expiration or effective date %q, want not specified", dc.sector, got)
		}
	}
	if d[881] != 1 {
		t.Errorf("sector %d: file structure version %d, want 1", dc.sector, d[881])
	}
	return root
}

// checkPathTable checks a path table sector holds exactly one record: the
// root, at rootSector, as its own parent, in the given byte order.
func checkPathTable(t *testing.T, sec []byte, order binary.ByteOrder, rootSector uint32) {
	t.Helper()
	want := make([]byte, 10)
	want[0] = 1
	order.PutUint32(want[2:6], rootSector)
	order.PutUint16(want[6:8], 1)
	if !bytes.Equal(sec[:10], want) || !bytes.Equal(sec[10:], make([]byte, len(sec)-10)) {
		t.Errorf("path table (%v) = %x, want %x", order, sec[:10], want)
	}
}

// checkRootDirectory checks one tree's root directory: "." and ".." first,
// then one record per file in ECMA-119 9.3 order, each pointing at a
// sector-aligned extent holding that file's bytes. It returns each file's
// extent (sector and size) keyed by the file's Joliet name, so the two
// trees' answers can be compared.
func checkRootDirectory(t *testing.T, img []byte, root record, joliet bool, byName map[string][]byte) map[string][2]uint32 {
	t.Helper()
	recs := readDirectory(t, img, root.sector, root.size)
	if len(recs) != len(byName)+2 {
		t.Fatalf("root directory holds %d records, want %d", len(recs), len(byName)+2)
	}
	for i, id := range [][]byte{{0}, {1}} {
		r := recs[i]
		if !bytes.Equal(r.id, id) || r.sector != root.sector || r.size != root.size || r.flags != 2 {
			t.Errorf("record %d = %+v, want the root itself named %x", i, r, id)
		}
	}
	extents := make(map[string][2]uint32)
	var ids []string
	for _, r := range recs[2:] {
		id := string(r.id)
		name := id
		if joliet {
			name = jolietName(t, r.id)
			id = name
		} else {
			name = nameForPrimary(t, id)
		}
		ids = append(ids, id)
		want, ok := byName[name]
		if !ok || r.flags != 0 || !bytes.Equal(r.date, seedRecordDate) {
			t.Errorf("unexpected record %q (flags %d, date %v)", id, r.flags, r.date)
			continue
		}
		if int(r.size) != len(want) {
			t.Errorf("%s: data length %d, want %d", id, r.size, len(want))
		}
		// Every extent starts inside the volume, even an empty file's.
		if int(r.sector)*iso9660.SectorSize >= len(img) {
			t.Errorf("%s: extent at sector %d is outside the %d-sector volume", id, r.sector, len(img)/iso9660.SectorSize)
		}
		start := int(r.sector) * iso9660.SectorSize
		if !bytes.Equal(img[start:start+len(want)], want) {
			t.Errorf("%s: the extent at sector %d does not hold the file's bytes", id, r.sector)
		}
		extents[name] = [2]uint32{r.sector, r.size}
	}
	if !sort.SliceIsSorted(ids, func(i, j int) bool { return padLess(ids[i], ids[j]) }) {
		t.Errorf("records are not in ECMA-119 9.3 order: %q", ids)
	}
	return extents
}

// nameForPrimary maps a primary identifier back to the seed file it was
// derived from, failing if it is not one seedPrimaryIDs expects.
func nameForPrimary(t *testing.T, id string) string {
	t.Helper()
	for name, want := range seedPrimaryIDs {
		if want == id {
			return name
		}
	}
	t.Errorf("primary identifier %q is not one the derivation rules give", id)
	return id
}

// padLess reports whether identifier a sorts before b under ECMA-119 9.3:
// name, then extension, each padded with spaces. It is written apart from
// the package's own comparison so the two can disagree.
func padLess(a, b string) bool {
	split := func(s string) (string, string) {
		s = strings.TrimSuffix(s, ";1")
		if i := strings.LastIndex(s, "."); i >= 0 {
			return s[:i], s[i+1:]
		}
		return s, ""
	}
	an, ae := split(a)
	bn, be := split(b)
	pad := func(x, y string) (string, string) {
		w := fmt.Sprintf("%d", max(len(x), len(y)))
		return fmt.Sprintf("%-"+w+"s", x), fmt.Sprintf("%-"+w+"s", y)
	}
	an, bn = pad(an, bn)
	if an != bn {
		return an < bn
	}
	ae, be = pad(ae, be)
	return ae < be
}

// TestWriteEmptyImage checks an image with no files still has both trees:
// 23 fixed sectors and a one-sector root directory for each tree.
func TestWriteEmptyImage(t *testing.T) {
	img := writeImage(t, "EMPTY", nil)
	if got := len(img) / iso9660.SectorSize; got != 25 {
		t.Fatalf("an empty image is %d sectors, want 25", got)
	}
	for _, sec := range []uint32{16, 17} {
		root := decodeRecord(t, sectorAt(img, sec)[156:190])
		if recs := readDirectory(t, img, root.sector, root.size); len(recs) != 2 {
			t.Fatalf("descriptor %d's root holds %d records, want only . and ..", sec, len(recs))
		}
	}
}

// TestWriteLargeDirectory fills both root directories past one sector
// with MaxFiles maximum-length names, so the rule that no record crosses
// a sector boundary is exercised, and checks every name is recorded.
func TestWriteLargeDirectory(t *testing.T) {
	files := make([]iso9660.File, iso9660.MaxFiles)
	for i := range files {
		// Names differ only near the end, so every primary identifier
		// collides after shortening and needs its numeric suffix.
		files[i] = iso9660.File{Name: fmt.Sprintf("%s%02d", longName[:iso9660.MaxNameLength-2], i), Data: []byte{byte(i)}}
	}
	img := writeImage(t, "BIG", files)
	primary := map[string]bool{}
	for _, sec := range []uint32{16, 17} {
		root := decodeRecord(t, sectorAt(img, sec)[156:190])
		if root.size <= iso9660.SectorSize {
			t.Fatalf("descriptor %d's root directory is %d bytes; the test needs more than one sector", sec, root.size)
		}
		recs := readDirectory(t, img, root.sector, root.size)
		if len(recs) != iso9660.MaxFiles+2 {
			t.Fatalf("descriptor %d's root holds %d records, want %d", sec, len(recs), iso9660.MaxFiles+2)
		}
		for _, r := range recs[2:] {
			// Each file holds one byte, its own index, so a record
			// pointing at the wrong extent reads back the wrong byte.
			var index int
			if sec == 16 {
				primary[string(r.id)] = true
				n, ext, _ := strings.Cut(strings.TrimSuffix(string(r.id), ";1"), ".")
				if len(n)+len(ext) > 30 {
					t.Errorf("primary identifier %q is longer than 30 characters", r.id)
				}
				index = int(img[int(r.sector)*iso9660.SectorSize])
			} else if _, err := fmt.Sscanf(jolietName(t, r.id)[iso9660.MaxNameLength-2:], "%02d", &index); err != nil {
				t.Fatalf("Joliet name %q does not end in its index: %v", jolietName(t, r.id), err)
			}
			if r.size != 1 || index >= iso9660.MaxFiles || img[int(r.sector)*iso9660.SectorSize] != byte(index) {
				t.Errorf("record %x: extent at sector %d, %d bytes, does not hold file %d", r.id, r.sector, r.size, index)
			}
		}
	}
	if len(primary) != iso9660.MaxFiles {
		t.Fatalf("%d distinct primary identifiers for %d files", len(primary), iso9660.MaxFiles)
	}
}
