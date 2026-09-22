// Tests for reading a backup's table of contents, on listings pg_restore
// really printed.
package backup_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/backup"
)

// readListing loads a listing captured from a real pg_restore --list.
//
// toc_migrated.txt is PostgreSQL 15.19's listing of a database this
// version's migrations built and bootstrap-admin wrote to. toc_planted.txt
// is the same database after a function, a trigger and a view were added
// to it, the shape a crafted or foreign archive takes.
//
// Both were recaptured on 2026-09-19, when the launchables table arrived and
// the counts below stopped matching, and again on 2026-09-21 for
// controller_instances (and sync_runs.owner_instance, which adds no entry). The procedure is the one the pair's own
// provenance describes and it is worth writing down, because the alternative
// when a table is added is to hand-edit a fixture that claims to be a real
// backup: run postgres at the pinned image (internal/testsupport.PostgresImage,
// 15.19), apply the migrations and bootstrap an administrator through the real
// controller binary, then pg_dump -Fc and pg_restore --list from INSIDE the
// container so both the server and the tool read 15.19. Plant the three objects
// above with psql and list again for the second fixture.
func readListing(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name)) // #nosec G304 -- a fixture name from this file
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return data
}

// TestParseTOC_ReadsARealBackupOfThisSchema proves the parser and the check
// accept exactly what pg_dump writes for this schema, table by table.
func TestParseTOC_ReadsARealBackupOfThisSchema(t *testing.T) {
	toc, err := backup.ParseTOC(readListing(t, "toc_migrated.txt"))
	if err != nil {
		t.Fatalf("ParseTOC() error = %v", err)
	}
	if toc.Format != "CUSTOM" || toc.ServerVersion != "15.19" {
		t.Fatalf("header = %q, %q; want CUSTOM, 15.19", toc.Format, toc.ServerVersion)
	}
	if len(toc.Entries) != 269 {
		t.Fatalf("read %d entries, want the 269 the listing holds", len(toc.Entries))
	}
	if err := toc.Check(backup.KnownTables()); err != nil {
		t.Fatalf("Check() refused a real backup of this schema: %v", err)
	}

	kinds := map[string]int{}
	var data []string
	for _, e := range toc.Entries {
		kinds[e.Kind]++
		if e.Kind == "TABLE DATA" {
			data = append(data, e.Tag)
		}
	}
	if kinds["TABLE"] != 38 || kinds["TABLE DATA"] != 38 || kinds["FK CONSTRAINT"] != 44 {
		t.Fatalf("kinds = %v", kinds)
	}
	// Every table this version knows is in the backup with its data, so a
	// table added to the schema without reaching a backup would show here.
	if len(data) != len(backup.KnownTables()) {
		t.Fatalf("the backup carries data for %d tables, and this version knows %d", len(data), len(backup.KnownTables()))
	}
}

// TestParseTOC_RefusesAFunctionATriggerAndAView proves an archive holding
// anything this schema never creates is refused by kind, with each kind
// named.
func TestParseTOC_RefusesAFunctionATriggerAndAView(t *testing.T) {
	_, err := backup.ParseTOC(readListing(t, "toc_planted.txt"))
	if !errors.Is(err, backup.ErrNotABackup) {
		t.Fatalf("ParseTOC() error = %v, want ErrNotABackup", err)
	}
	for _, kind := range []string{"FUNCTION", "TRIGGER", "VIEW"} {
		if !strings.Contains(err.Error(), kind) {
			t.Errorf("the refusal does not name %s: %v", kind, err)
		}
	}
}

// TestTOCCheck_RefusesWhatIsNotThisSchema covers the refusals the parser
// leaves to Check.
func TestTOCCheck_RefusesWhatIsNotThisSchema(t *testing.T) {
	good := string(readListing(t, "toc_migrated.txt"))
	cases := map[string]struct {
		listing string
		want    string
	}{
		"a plain-format archive": {
			strings.Replace(good, "Format: CUSTOM", "Format: TAR", 1), "custom-format",
		},
		"another schema": {
			good + "900; 1259 1 TABLE other things pleiades\n", "the schema other",
		},
		"a table this version does not have": {
			good + "901; 1259 1 TABLE public from_the_future pleiades\n", "the table from_the_future",
		},
		"no migration history": {
			strings.ReplaceAll(good, "public schema_migrations", "public schema_migrations_x"), "table schema_migrations_x",
		},
		"a kind that starts like an allowed one": {
			good + "902; 0 0 SEQUENCE OWNED BY public devices_id_seq pleiades\n", "the schema OWNED",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			toc, err := backup.ParseTOC([]byte(c.listing))
			if err == nil {
				err = toc.Check(backup.KnownTables())
			}
			if !errors.Is(err, backup.ErrNotABackup) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want ErrNotABackup naming %q", err, c.want)
			}
		})
	}
}

// TestParseTOC_RefusesLinesThatAreNotEntries covers malformed lines.
func TestParseTOC_RefusesLinesThatAreNotEntries(t *testing.T) {
	for _, line := range []string{
		"not an entry", "12 1259 1 TABLE public jobs pleiades", "x; 1259 1 TABLE public jobs pleiades",
		"12; a 1 TABLE public jobs pleiades", "12; 1259 1 TABLE public", "12; 1259 1 TABLE public  ",
		"-1; 1259 1 TABLE public jobs pleiades",
	} {
		if _, err := backup.ParseTOC([]byte("; Format: CUSTOM\n" + line + "\n")); !errors.Is(err, backup.ErrNotABackup) {
			t.Errorf("ParseTOC(%q) error = %v, want ErrNotABackup", line, err)
		}
	}
}

// TestPrintable_KeepsATerminalSafe proves text from a supplied file cannot
// carry an escape sequence into a message.
func TestPrintable_KeepsATerminalSafe(t *testing.T) {
	got := backup.Printable("jobs\x1b[2K\x07\xff" + strings.Repeat("a", 100))
	if strings.ContainsAny(got, "\x1b\x07") || strings.ContainsRune(got, '�') || len([]rune(got)) > 67 {
		t.Fatalf("Printable() = %q", got)
	}
}
