package migrate

import (
	"embed"
	"sort"
	"testing"
)

//go:embed testdata/mixed
var mixedTestdataFS embed.FS

// TestCheckGate is a white-box, table-driven unit test for the
// startup schema-version gate's pure decision logic. It is internal
// (package migrate, not migrate_test) because exercising a real gap
// between two committed migration files would need a second real
// migration to exist purely for the test's sake; checkGate's own inputs
// (known filenames, an applied set) are already plain values, so testing
// it directly here proves the same decision logic apply_test.go's
// real-database tests exercise only the "zero vs one migration" shape of.
func TestCheckGate(t *testing.T) {
	tests := []struct {
		name    string
		known   []string
		applied map[string]bool
		wantErr bool
	}{
		{
			name:    "fresh database, nothing applied yet",
			known:   []string{"0001_a.sql", "0002_b.sql"},
			applied: map[string]bool{},
			wantErr: false,
		},
		{
			name:    "fully applied, up to date",
			known:   []string{"0001_a.sql", "0002_b.sql"},
			applied: map[string]bool{"0001_a.sql": true, "0002_b.sql": true},
			wantErr: false,
		},
		{
			name:    "partially applied prefix, later migrations still pending",
			known:   []string{"0001_a.sql", "0002_b.sql", "0003_c.sql"},
			applied: map[string]bool{"0001_a.sql": true},
			wantErr: false,
		},
		{
			name:    "gap: a later migration is applied but an earlier one is not",
			known:   []string{"0001_a.sql", "0002_b.sql", "0003_c.sql"},
			applied: map[string]bool{"0001_a.sql": true, "0003_c.sql": true},
			wantErr: true,
		},
		{
			name:    "missing prefix: a later migration is applied but the first one is not",
			known:   []string{"0001_a.sql", "0002_b.sql", "0003_c.sql"},
			applied: map[string]bool{"0002_b.sql": true},
			wantErr: true,
		},
		{
			name:    "unrecognized applied version not in the known set",
			known:   []string{"0001_a.sql"},
			applied: map[string]bool{"0001_a.sql": true, "9999_future.sql": true},
			wantErr: true,
		},
		{
			name:    "unrecognized applied version, nothing else applied",
			known:   []string{"0001_a.sql"},
			applied: map[string]bool{"9999_future.sql": true},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := checkGate(tt.known, rowsOf(tt.applied), gateStrict)
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkGate(%v, %v) error = %v, wantErr %v", tt.known, tt.applied, err, tt.wantErr)
			}
		})
	}
}

// TestMigrationNames_SkipsSubdirectories proves migrationNames lists only
// files, in sorted order, from a real embedded filesystem containing a
// subdirectory (testdata/mixed/subdir) alongside two migration files.
// Real embedded content, not a synthetic fs.FS, since the production
// migrationSources value is itself a //go:embed'd directory and the claim
// is specifically about how migrationNames walks one.
func TestMigrationNames_SkipsSubdirectories(t *testing.T) {
	names, err := migrationNames(migrationSource{fsys: mixedTestdataFS, dir: "testdata/mixed"})
	if err != nil {
		t.Fatalf("migrationNames: %v", err)
	}

	want := []string{"0001_a.sql", "0002_b.sql"}
	if len(names) != len(want) {
		t.Fatalf("migrationNames = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("migrationNames = %v, want %v", names, want)
		}
	}
}

// rowsOf turns a set of applied versions into history rows with no floor, in
// a fixed order, the shape readHistory returns for a database written before
// the floor column existed.
func rowsOf(applied map[string]bool) []appliedRow {
	rows := make([]appliedRow, 0, len(applied))
	for version, ok := range applied {
		if ok {
			rows = append(rows, appliedRow{version: version})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].version < rows[j].version })
	return rows
}
