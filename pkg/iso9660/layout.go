// Layout for package iso9660: where every structure of an image goes,
// worked out in full before any byte is written.
package iso9660

import (
	"encoding/binary"
	"fmt"
	"slices"
	"time"
)

// Fixed sector numbers. Everything before firstDirSector is the same in
// every image; see the Layout section of the package documentation.
const (
	// pvdSector holds the primary volume descriptor. The descriptor set
	// starts at sector 16, after the system area (ECMA-119 6.7.1).
	pvdSector = 16
	// svdSector holds the Joliet supplementary volume descriptor.
	svdSector = 17
	// terminatorSector holds the volume descriptor set terminator.
	terminatorSector = 18
	// primaryLPath and primaryMPath hold the primary tree's path tables.
	primaryLPath = 19
	primaryMPath = 20
	// jolietLPath and jolietMPath hold the Joliet tree's path tables.
	jolietLPath = 21
	jolietMPath = 22
	// firstDirSector is where the primary root directory begins.
	firstDirSector = 23
)

// minYear and maxYear bound the timestamp: a directory record stores the
// year as years since 1900 in one byte (ECMA-119 9.1.5).
const (
	minYear = 1900
	maxYear = 1900 + 255
)

// entry is one file as both trees record it.
type entry struct {
	// name is the Joliet name, as the caller gave it.
	name string
	// primary is the derived ISO 9660 identifier, "NAME.EXT;1".
	primary string
	// data is the caller's contents, written once and shared by both trees.
	data []byte
	// sector is the first sector of the file's data extent.
	sector int
}

// extent is a run of whole sectors: where it starts and its length in
// bytes, which is how a directory record describes one (ECMA-119 9.1.3
// and 9.1.4).
type extent struct {
	sector int
	size   int
}

// plan is an image worked out in full: every name, order and sector
// number that write and metadata need.
type plan struct {
	// volumeID is the label as the caller gave it.
	volumeID string
	// stamp is the caller's time in UTC, used for every date recorded.
	stamp time.Time
	// entries are the files in Joliet directory order, which is also the
	// order their data extents are laid out in.
	entries []entry
	// primaryOrder lists indexes into entries in primary directory order,
	// which can differ from Joliet order once names are upper-cased.
	primaryOrder []int
	// primaryDir and jolietDir are the two root directory extents.
	primaryDir extent
	jolietDir  extent
	// dataStart is the first sector after the metadata: where file data
	// begins.
	dataStart int
	// sectors is the whole image's length in sectors, the Volume Space
	// Size both descriptors record (ECMA-119 8.4.8).
	sectors int
}

// newPlan checks every input against the package limits and lays the
// image out: directories first, then each file's data in entry order.
func newPlan(volumeID string, modified time.Time, files []File) (*plan, error) {
	if err := checkVolumeID(volumeID); err != nil {
		return nil, err
	}
	stamp := modified.UTC()
	if y := stamp.Year(); y < minYear || y > maxYear {
		return nil, fmt.Errorf("iso9660: timestamp year %d is outside %d through %d", y, minYear, maxYear)
	}
	entries, err := checkFiles(files)
	if err != nil {
		return nil, err
	}
	p := &plan{volumeID: volumeID, stamp: stamp, entries: entries}

	// The primary tree sorts its own identifiers, which can order
	// differently from the Joliet names they came from.
	p.primaryOrder = make([]int, len(entries))
	for i := range p.primaryOrder {
		p.primaryOrder[i] = i
	}
	slices.SortFunc(p.primaryOrder, func(a, b int) int {
		return compareIdentifiers(entries[a].primary, entries[b].primary)
	})

	// A directory's size depends only on its identifiers' lengths, so
	// both can be sized before any file has a sector.
	primarySectors := dirSectors(p.records(false))
	jolietSectors := dirSectors(p.records(true))
	p.primaryDir = extent{sector: firstDirSector, size: primarySectors * SectorSize}
	p.jolietDir = extent{sector: firstDirSector + primarySectors, size: jolietSectors * SectorSize}
	p.dataStart = p.jolietDir.sector + jolietSectors

	// Each file's data starts on the next free sector. An empty file
	// takes that sector number without using it, as mkisofs does.
	next := p.dataStart
	for i := range p.entries {
		p.entries[i].sector = next
		next += (len(p.entries[i].data) + SectorSize - 1) / SectorSize
	}
	p.sectors = next

	// An empty file placed after the last data would name the sector one
	// past the end of the volume. Point it at the last recorded sector
	// instead, so every extent lies inside the volume.
	for i := range p.entries {
		if p.entries[i].sector >= p.sectors {
			p.entries[i].sector = p.sectors - 1
		}
	}
	return p, nil
}

// fileRecord is one file as a directory record in one tree describes it.
type fileRecord struct {
	// id is the file identifier exactly as recorded: d-characters for the
	// primary tree, UCS-2 big-endian for the Joliet tree.
	id []byte
	// data is where the file's contents live.
	data extent
}

// records returns one tree's file records in that tree's directory order.
// Before newPlan assigns sectors, the records still have their final
// identifiers, which is all directory sizing needs.
func (p *plan) records(joliet bool) []fileRecord {
	out := make([]fileRecord, 0, len(p.entries))
	if joliet {
		for _, e := range p.entries {
			out = append(out, fileRecord{id: ucs2(e.name), data: extent{e.sector, len(e.data)}})
		}
		return out
	}
	for _, i := range p.primaryOrder {
		e := p.entries[i]
		out = append(out, fileRecord{id: []byte(e.primary), data: extent{e.sector, len(e.data)}})
	}
	return out
}

// tree is one directory hierarchy as the metadata records it: its volume
// descriptor, its two path tables and its root directory.
type tree struct {
	// joliet is true for the Joliet tree and false for the primary one.
	joliet bool
	// descriptor, lPath and mPath are the sectors of the tree's volume
	// descriptor and its type L and type M path tables.
	descriptor, lPath, mPath int
	// root is the tree's root directory extent.
	root extent
}

// trees returns the primary tree and the Joliet tree, in that order.
func (p *plan) trees() [2]tree {
	return [2]tree{
		{joliet: false, descriptor: pvdSector, lPath: primaryLPath, mPath: primaryMPath, root: p.primaryDir},
		{joliet: true, descriptor: svdSector, lPath: jolietLPath, mPath: jolietMPath, root: p.jolietDir},
	}
}

// metadata encodes every sector before the file data: the zero system
// area, both volume descriptors, the terminator, both trees' path tables
// and both root directories.
func (p *plan) metadata() []byte {
	buf := make([]byte, p.dataStart*SectorSize)
	date := recordDate(p.stamp)
	for _, t := range p.trees() {
		p.descriptor(sector(buf, t.descriptor), t, date)
		copy(sector(buf, t.lPath), pathTable(t.root.sector, binary.LittleEndian))
		copy(sector(buf, t.mPath), pathTable(t.root.sector, binary.BigEndian))
		copy(buf[t.root.sector*SectorSize:], directory(t.root, p.records(t.joliet), date))
	}
	terminator(sector(buf, terminatorSector))
	return buf
}

// sector returns sector n of buf as a slice of exactly SectorSize bytes.
func sector(buf []byte, n int) []byte {
	return buf[n*SectorSize : (n+1)*SectorSize]
}
