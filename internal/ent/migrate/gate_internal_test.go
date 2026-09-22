// These tests pin the gate's decision on every shape of history it can be
// handed: the ordinary ones, the within-window ones a rollback produces, and
// the tampered ones (gaps, duplicates, unknown names, missing floors) a fuzzer
// finds. The fuzz target compares the gate against a second, deliberately
// naive implementation written only from the rules in gate.go's comments, so
// a disagreement is a bug in one of the two rather than a crash nobody
// specified.
package migrate

import (
	"database/sql"
	"reflect"
	"sort"
	"testing"
)

// windowKnown is the migration set the window tests pretend this build has.
var windowKnown = []string{"0001_a.sql", "0002_b.sql", "0003_c.sql"}

// row builds one history row, with a floor when floor is not empty.
func row(version, floor string) appliedRow {
	return appliedRow{version: version, floor: sql.NullString{String: floor, Valid: floor != ""}}
}

// TestCheckGate_WithinWindow covers what a build may do with a database a
// newer build migrated further, in both modes.
func TestCheckGate_WithinWindow(t *testing.T) {
	base := []appliedRow{row("0001_a.sql", ""), row("0002_b.sql", ""), row("0003_c.sql", "")}
	with := func(extra ...appliedRow) []appliedRow {
		return append(append([]appliedRow{}, base...), extra...)
	}

	tests := []struct {
		name      string
		applied   []appliedRow
		mode      gateMode
		wantErr   bool
		wantNewer []string
		wantFloor string
	}{
		{
			name:    "strict refuses any newer migration, however compatible",
			applied: with(row("0004_d.sql", "0001_a.sql")),
			mode:    gateStrict,
			wantErr: true,
		},
		{
			name:      "an expand-only newer migration is served",
			applied:   with(row("0004_d.sql", "0002_b.sql")),
			mode:      gateWithinWindow,
			wantNewer: []string{"0004_d.sql"},
			wantFloor: "0002_b.sql",
		},
		{
			name:      "a floor equal to this build's newest migration is served",
			applied:   with(row("0004_d.sql", "0003_c.sql"), row("0005_e.sql", "0003_c.sql")),
			mode:      gateWithinWindow,
			wantNewer: []string{"0004_d.sql", "0005_e.sql"},
			wantFloor: "0003_c.sql",
		},
		{
			name:    "a contract past this build is refused",
			applied: with(row("0004_d.sql", "0004_d.sql")),
			mode:    gateWithinWindow,
			wantErr: true,
		},
		{
			name:    "the highest floor decides, not the newest row's",
			applied: with(row("0004_d.sql", "0004_d.sql"), row("0005_e.sql", "0002_b.sql")),
			mode:    gateWithinWindow,
			wantErr: true,
		},
		{
			name:    "a newer migration with no floor was written by a build that promised nothing",
			applied: with(row("0004_d.sql", "")),
			mode:    gateWithinWindow,
			wantErr: true,
		},
		{
			name:    "a floor that is not a migration name is refused",
			applied: with(row("0004_d.sql", "yesterday")),
			mode:    gateWithinWindow,
			wantErr: true,
		},
		{
			name:    "a floor naming another lineage's migration is refused",
			applied: with(row("0004_d.sql", "0002_other.sql")),
			mode:    gateWithinWindow,
			wantErr: true,
		},
		{
			name:    "two floors sharing a number, one from another lineage, are refused in either order",
			applied: with(row("0004_d.sql", "0002_other.sql"), row("0005_e.sql", "0002_b.sql")),
			mode:    gateWithinWindow,
			wantErr: true,
		},
		{
			name:    "newer migrations that skip a number are refused",
			applied: with(row("0005_e.sql", "0001_a.sql")),
			mode:    gateWithinWindow,
			wantErr: true,
		},
		{
			name:    "a newer migration while this build still has one pending means diverged histories",
			applied: []appliedRow{row("0001_a.sql", ""), row("0002_b.sql", ""), row("0004_d.sql", "0001_a.sql")},
			mode:    gateWithinWindow,
			wantErr: true,
		},
		{
			name:    "two rows under one number are two lineages",
			applied: with(row("0003_other.sql", "0001_a.sql")),
			mode:    gateWithinWindow,
			wantErr: true,
		},
		{
			name:    "the same row twice is a tampered table",
			applied: with(row("0003_c.sql", "")),
			mode:    gateWithinWindow,
			wantErr: true,
		},
		{
			name:    "a recorded version that is not a migration name is refused",
			applied: with(row("0004_d.sql\x1b[2J", "0001_a.sql")),
			mode:    gateWithinWindow,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := checkGate(windowKnown, tt.applied, tt.mode)
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkGate error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if len(got.pending) != 0 {
				t.Errorf("pending = %v, want none: a build never applies anything to a database ahead of it", got.pending)
			}
			if !reflect.DeepEqual(got.newer, tt.wantNewer) || got.floor != tt.wantFloor {
				t.Errorf("newer, floor = %v, %q; want %v, %q", got.newer, got.floor, tt.wantNewer, tt.wantFloor)
			}
		})
	}
}

// gateCandidates is the alphabet FuzzCheckGate builds histories from: every
// name this build knows, newer names that continue it, a newer name that skips
// a number, names that collide by number, and names that are not migration
// names at all.
var gateCandidates = []string{
	"0001_a.sql", "0002_b.sql", "0003_c.sql",
	"0004_d.sql", "0005_e.sql", "0007_g.sql",
	"0002_other.sql", "0004_other.sql",
	"bad.sql", "0001_a.sql ", "", "0001_A.sql",
}

// FuzzCheckGate compares checkGate with oracleGate on histories built from
// gateCandidates: each pair of input bytes chooses one row's version and its
// floor (the floor alphabet adds "no floor").
func FuzzCheckGate(f *testing.F) {
	// Seeds: an empty history, a full one, the missing-prefix case
	// (FAILURE_PATTERNS.md #276), a gap, a duplicate, and a within-window
	// newer migration.
	f.Add([]byte{}, false)
	f.Add([]byte{0, 12, 1, 12, 2, 12}, false)
	f.Add([]byte{1, 12}, false)
	f.Add([]byte{0, 12, 2, 12}, false)
	f.Add([]byte{0, 12, 0, 12}, false)
	f.Add([]byte{0, 12, 1, 12, 2, 12, 3, 1}, true)
	f.Add([]byte{0, 12, 1, 12, 2, 12, 3, 3}, true)

	f.Fuzz(func(t *testing.T, data []byte, window bool) {
		var applied []appliedRow
		for i := 0; i+1 < len(data) && len(applied) < 12; i += 2 {
			version := gateCandidates[int(data[i])%len(gateCandidates)]
			floorIndex := int(data[i+1]) % (len(gateCandidates) + 1)
			floor := sql.NullString{}
			if floorIndex < len(gateCandidates) {
				floor = sql.NullString{String: gateCandidates[floorIndex], Valid: true}
			}
			applied = append(applied, appliedRow{version: version, floor: floor})
		}
		mode := gateStrict
		if window {
			mode = gateWithinWindow
		}

		got, err := checkGate(windowKnown, applied, mode)
		want, ok := oracleGate(windowKnown, applied, mode)
		if (err == nil) != ok {
			t.Fatalf("checkGate(%v, window=%t) error = %v; the oracle says accepted = %t", applied, window, err, ok)
		}
		if err != nil {
			return
		}
		if !equalStrings(got.pending, want.pending) || !equalStrings(got.newer, want.newer) || got.floor != want.floor {
			t.Fatalf("checkGate(%v, window=%t) = %+v; the oracle says %+v", applied, window, got, want)
		}
	})
}

// oracleGate is the gate's rules restated as plainly as possible, without
// sharing a line of code with checkGate, so FuzzCheckGate compares two
// independent readings of the same rules.
func oracleGate(known []string, applied []appliedRow, mode gateMode) (gateResult, bool) {
	number := func(name string) int {
		n, err := versionNumber(name)
		if err != nil {
			return -1
		}
		return n
	}
	isKnown := func(name string) bool {
		for _, k := range known {
			if k == name {
				return true
			}
		}
		return false
	}

	// Every row well formed, every number used once.
	numbers := map[int]bool{}
	var versions []string
	for _, r := range applied {
		n := number(r.version)
		if n < 0 || numbers[n] {
			return gateResult{}, false
		}
		numbers[n] = true
		versions = append(versions, r.version)
	}
	sort.Strings(versions)

	var knownApplied, unknown []appliedRow
	for _, r := range applied {
		if isKnown(r.version) {
			knownApplied = append(knownApplied, r)
		} else {
			unknown = append(unknown, r)
		}
	}
	// The known rows are exactly the first len(knownApplied) known names.
	for i := range knownApplied {
		found := false
		for _, r := range knownApplied {
			if r.version == known[i] {
				found = true
			}
		}
		if !found {
			return gateResult{}, false
		}
	}
	if len(unknown) == 0 {
		return gateResult{pending: known[len(knownApplied):]}, true
	}
	if mode == gateStrict || len(knownApplied) != len(known) {
		return gateResult{}, false
	}
	// The unknown rows are exactly the next numbers after this build's head.
	head := number(known[len(known)-1])
	for i := 1; i <= len(unknown); i++ {
		if !numbers[head+i] {
			return gateResult{}, false
		}
	}
	best := ""
	for _, r := range unknown {
		if !r.floor.Valid || number(r.floor.String) < 0 {
			return gateResult{}, false
		}
		// A floor within this build's numbering must be this build's own.
		if number(r.floor.String) <= head && !isKnown(r.floor.String) {
			return gateResult{}, false
		}
		if best == "" || number(r.floor.String) > number(best) {
			best = r.floor.String
		}
	}
	if !isKnown(best) {
		return gateResult{}, false
	}
	newer := make([]string, 0, len(unknown))
	for _, v := range versions {
		if !isKnown(v) {
			newer = append(newer, v)
		}
	}
	return gateResult{newer: newer, floor: best}, true
}

// equalStrings compares two string slices, treating nil and empty as equal.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FuzzVersionNumber proves versionNumber never panics and accepts exactly
// the names versionPattern describes, returning the number they start with.
func FuzzVersionNumber(f *testing.F) {
	for _, seed := range []string{"0001_initial.sql", "9999_z.sql", "0001_A.sql", "001_a.sql", "0001_a.sql\n", "", "0001_.sql", "٠٠٠١_a.sql"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		n, err := versionNumber(name)
		if versionPattern.MatchString(name) != (err == nil) {
			t.Fatalf("versionNumber(%q) error = %v, but the pattern match is %t", name, err, versionPattern.MatchString(name))
		}
		if err != nil {
			return
		}
		if n < 0 || n > 9999 {
			t.Fatalf("versionNumber(%q) = %d, outside four digits", name, n)
		}
		for _, c := range name {
			if c < 0x20 || c == 0x7f {
				t.Fatalf("versionNumber accepted %q, which holds a control character", name)
			}
		}
	})
}
