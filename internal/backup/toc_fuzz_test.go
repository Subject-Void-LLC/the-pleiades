// The fuzz target for the table of contents parser.
package backup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/Subject-Void-LLC/the-pleiades/internal/backup"
)

// FuzzParseTOC feeds the parser arbitrary listings. The listing is text a
// file an operator supplied made pg_restore print, so the parser is the
// first thing an attacker's bytes reach.
//
// Three properties, checked on every input:
//   - it never panics;
//   - whatever it accepts holds only allowed kinds, and whatever Check then
//     accepts is entirely in the public schema with the history table in it;
//   - no refusal carries a control character, since refusals are printed to
//     the terminal of the person running the restore.
func FuzzParseTOC(f *testing.F) {
	for _, name := range []string{"toc_migrated.txt", "toc_planted.txt"} {
		data, err := os.ReadFile(filepath.Join("testdata", name)) // #nosec G304 -- a fixture name from this file
		if err != nil {
			f.Fatalf("reading %s: %v", name, err)
		}
		f.Add(data)
	}
	f.Add([]byte("; Format: CUSTOM\n1; 1 1 TABLE public schema_migrations x\n"))
	f.Add([]byte("1; 1 1 TABLE\x1b[2K public jobs x\n"))
	f.Add([]byte("1; 1 1 EVENT TRIGGER - t x\n"))

	allowed := map[string]bool{
		"TABLE": true, "TABLE DATA": true, "SEQUENCE": true, "SEQUENCE SET": true,
		"INDEX": true, "CONSTRAINT": true, "FK CONSTRAINT": true,
	}
	known := backup.KnownTables()
	f.Fuzz(func(t *testing.T, listing []byte) {
		toc, err := backup.ParseTOC(listing)
		if err != nil {
			assertPrintable(t, err.Error())
			return
		}
		for _, e := range toc.Entries {
			if !allowed[e.Kind] || e.Tag == "" {
				t.Fatalf("ParseTOC accepted an entry of kind %q with tag %q", e.Kind, e.Tag)
			}
		}
		if err := toc.Check(known); err != nil {
			assertPrintable(t, err.Error())
			return
		}
		history := false
		for _, e := range toc.Entries {
			if e.Namespace != "public" {
				t.Fatalf("Check accepted an entry in %q", e.Namespace)
			}
			if e.Kind == "TABLE" && !known[e.Tag] {
				t.Fatalf("Check accepted the unknown table %q", e.Tag)
			}
			history = history || (e.Kind == "TABLE" && e.Tag == "schema_migrations")
		}
		if !history {
			t.Fatal("Check accepted a listing with no migration history table")
		}
	})
}

// assertPrintable fails when s holds a control character other than the
// newline a multi-line message may use.
func assertPrintable(t *testing.T, s string) {
	t.Helper()
	if strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' }) >= 0 {
		t.Fatalf("a refusal carries a control character: %q", s)
	}
}
