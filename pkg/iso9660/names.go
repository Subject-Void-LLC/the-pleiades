// Name rules for package iso9660: which file names and volume
// identifiers Write accepts, how a Joliet name becomes an ISO 9660 file
// identifier, and the order ECMA-119 9.3 gives directory records.
package iso9660

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// maxPrimaryChars is the most characters an ISO 9660 file name and its
// extension may hold together (ECMA-119 7.5.1).
const maxPrimaryChars = 30

// keptExtension is how many characters of an extension survive when a
// derived ISO 9660 identifier has to be shortened to fit maxPrimaryChars.
const keptExtension = 8

// checkVolumeID refuses a volume identifier outside the documented limit:
// 1 to MaxVolumeIDLength ASCII letters, digits and '_'.
func checkVolumeID(id string) error {
	if id == "" {
		return errors.New("iso9660: empty volume identifier")
	}
	if len(id) > MaxVolumeIDLength {
		return fmt.Errorf("iso9660: volume identifier %q is longer than %d characters", id, MaxVolumeIDLength)
	}
	for i := 0; i < len(id); i++ {
		// Upper-casing first lets one test accept both letter cases.
		if !isDChar(upper(id[i])) {
			return fmt.Errorf("iso9660: volume identifier %q may hold only ASCII letters, digits and '_'", id)
		}
	}
	return nil
}

// checkName refuses a file name outside the documented limit.
func checkName(name string) error {
	switch {
	case name == "":
		return errors.New("iso9660: empty file name")
	case len(name) > MaxNameLength:
		return fmt.Errorf("iso9660: file name %q is longer than %d characters", name, MaxNameLength)
	case name[len(name)-1] == '.':
		// This also refuses "." and "..", which name directories.
		return fmt.Errorf("iso9660: file name %q ends in '.', which Linux and Windows strip from a Joliet name", name)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !isDChar(upper(c)) && c != '.' && c != '-' {
			return fmt.Errorf("iso9660: file name %q may hold only ASCII letters, digits, '.', '_' and '-'", name)
		}
	}
	return nil
}

// checkFiles applies every per-file and whole-image limit and returns the
// files as entries sorted into Joliet directory order, each carrying its
// derived ISO 9660 identifier. Sorting here, not later, is what makes the
// output independent of the order the caller listed the files in.
func checkFiles(files []File) ([]entry, error) {
	if len(files) > MaxFiles {
		return nil, fmt.Errorf("iso9660: %d files is more than the %d one image may hold", len(files), MaxFiles)
	}
	// seen maps each lower-cased name to the name as given, so a
	// duplicate's error can name both spellings.
	seen := make(map[string]string, len(files))
	total := 0
	entries := make([]entry, 0, len(files))
	for _, f := range files {
		if err := checkName(f.Name); err != nil {
			return nil, err
		}
		folded := strings.ToLower(f.Name)
		if prior, dup := seen[folded]; dup {
			return nil, fmt.Errorf("iso9660: file name %q repeats %q, and names must be unique ignoring case", f.Name, prior)
		}
		seen[folded] = f.Name
		if len(f.Data) > MaxFileSize {
			return nil, fmt.Errorf("iso9660: file %q is %d bytes, more than the %d one file may hold", f.Name, len(f.Data), MaxFileSize)
		}
		total += len(f.Data)
		if total > MaxTotalSize {
			return nil, fmt.Errorf("iso9660: files total more than the %d bytes one image may hold", MaxTotalSize)
		}
		entries = append(entries, entry{name: f.Name, data: f.Data})
	}
	slices.SortFunc(entries, func(a, b entry) int { return compareIdentifiers(a.name, b.name) })
	assignPrimaryIDs(entries)
	return entries, nil
}

// assignPrimaryIDs derives each entry's ISO 9660 file identifier from its
// Joliet name, in the entries' (sorted) order, so which of two colliding
// names gets the numeric suffix is decided by name rather than by input
// order.
func assignPrimaryIDs(entries []entry) {
	taken := make(map[string]bool, len(entries))
	for i := range entries {
		base, ext := splitName(entries[i].name)
		base, ext = dChars(base), dChars(ext)
		id := primaryID(base, ext, "")
		// At most MaxFiles-1 other identifiers exist, so this loop ends
		// by n = MaxFiles and the suffix never exceeds three characters.
		for n := 1; taken[id]; n++ {
			id = primaryID(base, ext, "_"+strconv.Itoa(n))
		}
		taken[id] = true
		entries[i].primary = id
	}
}

// primaryID assembles an ISO 9660 file identifier, "NAME.EXT;1", from
// d-character parts. The separator '.' is always present and the version
// is always 1 (ECMA-119 7.5.1). When name, suffix and extension together
// pass maxPrimaryChars, the extension keeps at most keptExtension
// characters and the name is cut to what remains. Joliet names are at
// most 64 characters and the suffix at most 3, so the name keeps at
// least 19.
func primaryID(base, ext, suffix string) string {
	if len(base)+len(ext)+len(suffix) > maxPrimaryChars {
		ext = ext[:min(len(ext), keptExtension)]
		base = base[:min(len(base), maxPrimaryChars-len(ext)-len(suffix))]
	}
	return base + suffix + "." + ext + ";1"
}

// splitName splits a name at its last '.' into a file name and an
// extension, the two parts ECMA-119 7.5.1 and 9.3 treat separately. A
// name with no '.' has an empty extension.
func splitName(name string) (base, ext string) {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return name, ""
	}
	return name[:i], name[i+1:]
}

// compareIdentifiers orders two identifiers the way ECMA-119 9.3 orders
// directory records: by file name, then by extension, each compared as if
// the shorter one were padded with spaces. A trailing ";1" version is
// ignored, since every file here has version 1. Joliet names are ASCII,
// so comparing their bytes equals comparing their UCS-2 code units.
func compareIdentifiers(a, b string) int {
	aBase, aExt := splitName(strings.TrimSuffix(a, ";1"))
	bBase, bExt := splitName(strings.TrimSuffix(b, ";1"))
	if c := comparePadded(aBase, bBase); c != 0 {
		return c
	}
	if c := comparePadded(aExt, bExt); c != 0 {
		return c
	}
	// Distinct names never tie above, but a total order costs nothing.
	return strings.Compare(a, b)
}

// comparePadded compares two strings as ECMA-119 9.3 does: byte by byte,
// with the shorter one treated as padded with spaces (0x20).
func comparePadded(a, b string) int {
	for i := 0; i < max(len(a), len(b)); i++ {
		ca, cb := byte(' '), byte(' ')
		if i < len(a) {
			ca = a[i]
		}
		if i < len(b) {
			cb = b[i]
		}
		if ca != cb {
			return cmp.Compare(ca, cb)
		}
	}
	return 0
}

// dChars maps a string onto d-characters (ECMA-119 7.4.1): letters are
// upper-cased and anything that is not A-Z, 0-9 or '_' becomes '_'.
func dChars(s string) string {
	b := []byte(s)
	for i, c := range b {
		c = upper(c)
		if !isDChar(c) {
			c = '_'
		}
		b[i] = c
	}
	return string(b)
}

// isDChar reports whether c is a d-character: A-Z, 0-9 or '_'.
func isDChar(c byte) bool {
	return ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9') || c == '_'
}

// upper upper-cases an ASCII letter and returns any other byte unchanged.
func upper(c byte) byte {
	if 'a' <= c && c <= 'z' {
		return c - ('a' - 'A')
	}
	return c
}
