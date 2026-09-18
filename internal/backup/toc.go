// The table of contents `pg_restore --list` prints, read strictly, and the
// entries a backup of this schema may hold.
package backup

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// ErrNotABackup wraps every refusal of a file's contents: it is not an
// archive this command wrote, or not one of this schema.
var ErrNotABackup = errors.New("backup: the file is not a backup of this deployment's database")

// maxTOCBytes caps the listing read back. A backup of this schema lists a
// few hundred entries in about thirty kilobytes.
const maxTOCBytes = 4 << 20

// allowedKinds are the kinds of entry a backup of this schema holds, longest
// first so a kind that begins with another ("TABLE DATA", "TABLE") is
// matched whole.
//
// The list is measured, not assumed: it is exactly what pg_dump wrote for a
// database this version's migrations built, and no migration in any version
// creates a function, trigger, view, type, extension or rule, or drops a
// table. A kind outside it is a file from some other database, and is
// refused before anything is restored.
//
// This is the first check, not the one security rests on. A crafted archive
// can label any SQL with any kind, so what contains one is the restore
// running as a role with no rights beyond its own scratch database, and the
// schema comparison after it (see restore.go).
var allowedKinds = []string{
	"FK CONSTRAINT",
	"SEQUENCE SET",
	"TABLE DATA",
	"CONSTRAINT",
	"SEQUENCE",
	"INDEX",
	"TABLE",
}

// Entry is one line of a table of contents.
type Entry struct {
	// ID is the archive's dump id for the entry.
	ID int

	// Kind is the entry's kind, one of allowedKinds.
	Kind string

	// Namespace is the schema the entry belongs to.
	Namespace string

	// Tag names the object: a table for TABLE and TABLE DATA, "table
	// constraint" for the constraint kinds.
	Tag string
}

// TOC is a parsed table of contents.
type TOC struct {
	// Format is the archive format pg_restore reported, "CUSTOM" for every
	// backup this command writes.
	Format string

	// ServerVersion is the server version the archive was dumped from.
	ServerVersion string

	// Entries is every entry, in listing order.
	Entries []Entry
}

// ParseTOC reads pg_restore's listing. It refuses a line it cannot read and
// an entry of any kind outside allowedKinds, naming at most a few of each,
// sanitized, since the text comes from a file an operator supplied.
func ParseTOC(listing []byte) (TOC, error) {
	if len(listing) > maxTOCBytes {
		return TOC{}, fmt.Errorf("%w: its table of contents is larger than %d bytes", ErrNotABackup, maxTOCBytes)
	}
	var toc TOC
	unknown := map[string]bool{}
	for number, raw := range strings.Split(string(listing), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		switch {
		case strings.TrimSpace(line) == "":
			continue
		case strings.HasPrefix(line, ";"):
			readHeader(&toc, line)
			continue
		}
		entry, kindText, err := parseEntry(line)
		if err != nil {
			return TOC{}, fmt.Errorf("%w: line %d of its table of contents is not an entry", ErrNotABackup, number+1)
		}
		if entry.Kind == "" {
			unknown[printable(kindText)] = true
			continue
		}
		toc.Entries = append(toc.Entries, entry)
	}
	if len(unknown) > 0 {
		return TOC{}, fmt.Errorf("%w: it holds %s, which no backup of this schema does", ErrNotABackup, listOf(unknown))
	}
	return toc, nil
}

// readHeader picks the two header fields a check needs out of a comment line.
func readHeader(toc *TOC, line string) {
	field := strings.TrimSpace(strings.TrimPrefix(line, ";"))
	if v, ok := strings.CutPrefix(field, "Format: "); ok {
		toc.Format = strings.TrimSpace(v)
	}
	if v, ok := strings.CutPrefix(field, "Dumped from database version: "); ok {
		toc.ServerVersion = printable(strings.TrimSpace(v))
	}
}

// parseEntry reads "<id>; <tableoid> <oid> <kind> <namespace> <tag> <owner>".
// An entry whose kind is not allowed comes back with an empty Kind and the
// kind's first word, so the caller can name it.
func parseEntry(line string) (Entry, string, error) {
	idText, rest, ok := strings.Cut(line, "; ")
	if !ok {
		return Entry{}, "", errors.New("no id")
	}
	id, err := strconv.Atoi(idText)
	if err != nil || id < 0 {
		return Entry{}, "", errors.New("bad id")
	}
	fields := strings.SplitN(rest, " ", 3)
	if len(fields) != 3 || !isDigits(fields[0]) || !isDigits(fields[1]) {
		return Entry{}, "", errors.New("bad catalog id")
	}
	rest = fields[2]

	kind := ""
	for _, k := range allowedKinds {
		if strings.HasPrefix(rest, k+" ") {
			kind = k
			break
		}
	}
	if kind == "" {
		first, _, _ := strings.Cut(rest, " ")
		return Entry{}, first, nil
	}
	rest = strings.TrimPrefix(rest, kind+" ")
	namespace, rest, ok := strings.Cut(rest, " ")
	if !ok {
		return Entry{}, "", errors.New("no tag")
	}
	// The owner is the last field and may be empty, which leaves a trailing
	// space; everything between is the tag.
	tag := rest
	if i := strings.LastIndex(rest, " "); i >= 0 {
		tag = rest[:i]
	}
	if tag == "" {
		return Entry{}, "", errors.New("empty tag")
	}
	return Entry{ID: id, Kind: kind, Namespace: namespace, Tag: tag}, "", nil
}

// Check refuses a table of contents that is not a custom-format backup of
// this schema: every entry in the public schema, every table one of known,
// and the migration history table among them.
func (t TOC) Check(known map[string]bool) error {
	if t.Format != "CUSTOM" {
		return fmt.Errorf("%w: it is not a custom-format archive, which every backup this command takes is", ErrNotABackup)
	}
	strange := map[string]bool{}
	tables := map[string]bool{}
	for _, e := range t.Entries {
		if e.Namespace != "public" {
			strange["an entry in the schema "+printable(e.Namespace)] = true
			continue
		}
		if e.Kind == "TABLE" {
			tables[e.Tag] = true
			if !known[e.Tag] {
				strange["the table "+printable(e.Tag)] = true
			}
		}
	}
	if len(strange) > 0 {
		return fmt.Errorf("%w: it holds %s, which this version's schema does not. A backup from a newer version is restored with that version", ErrNotABackup, listOf(strange))
	}
	if !tables["schema_migrations"] {
		return fmt.Errorf("%w: it has no migration history table, which every database this controller has opened does", ErrNotABackup)
	}
	return nil
}

// isDigits reports whether s is a non-empty run of ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// printable makes text from an operator-supplied file safe to print: control
// characters become "?", which stops a crafted name from moving the cursor
// or rewriting the line a person is reading, and it is cut to 64 characters.
func printable(s string) string { return printableUpTo(s, 64) }

// printablePath is printable for a path: the same control characters
// replaced, and a limit long enough that a real path is never cut. A path
// cut short is a path nobody can find.
func printablePath(s string) string { return printableUpTo(s, 4096) }

// printableUpTo is printable with the length limit given.
func printableUpTo(s string, limit int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n == limit {
			b.WriteString("...")
			break
		}
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			r = '?'
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// listOf joins up to five of set's items in sorted order and says how many
// more there were.
func listOf(set map[string]bool) string {
	items := make([]string, 0, len(set))
	for item := range set {
		items = append(items, item)
	}
	sort.Strings(items)
	const shown = 5
	if len(items) <= shown {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:shown], ", ") + fmt.Sprintf(" and %d more", len(items)-shown)
}
