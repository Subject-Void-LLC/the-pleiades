// Tests for backup file names.
package backup_test

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/backup"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// TestName_RoundTripsAndNamesTheKey pins the name's shape: the key it
// carries is the fingerprint's first eight hex characters, never the key,
// and ParseName reads back exactly what String wrote.
func TestName_RoundTripsAndNamesTheKey(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	taken := time.Date(2026, 9, 18, 14, 2, 3, 999, time.FixedZone("elsewhere", 5*3600))

	for _, beforeRestore := range []bool{false, true} {
		n := backup.NewName(taken, key, beforeRestore)
		s := n.String()
		if strings.Contains(s, crypto.EncodeKey(key)) {
			t.Fatalf("the name %q holds the key", s)
		}
		want := "pleiades-20260918T090203Z-" + crypto.Fingerprint(key)[:8]
		if beforeRestore {
			want += "-before-restore"
		}
		if s != want+".dump" {
			t.Fatalf("String() = %q, want %q.dump", s, want)
		}
		back, ok := backup.ParseName(s)
		if !ok || !back.Taken.Equal(n.Taken) || back.Key != n.Key || back.BeforeRestore != beforeRestore {
			t.Fatalf("ParseName(%q) = %+v, %v; want %+v", s, back, ok, n)
		}
		if got := back.KeyLabel(); got != crypto.Fingerprint(key)[:4]+"-"+crypto.Fingerprint(key)[4:8] {
			t.Fatalf("KeyLabel() = %q", got)
		}
	}

	none := backup.NewName(taken, nil, false)
	if !strings.Contains(none.String(), "-nokey.dump") || none.KeyLabel() != "no key" {
		t.Fatalf("a backup with no key is named %q, labelled %q", none.String(), none.KeyLabel())
	}
	if back, ok := backup.ParseName(none.String()); !ok || back.Key != "" {
		t.Fatalf("ParseName(%q) = %+v, %v", none.String(), back, ok)
	}
}

// TestName_SortsInTimeOrder proves a directory listing is a timeline, which
// is what lets a person find the newest backup by looking.
func TestName_SortsInTimeOrder(t *testing.T) {
	key := []byte(strings.Repeat("s", 32))
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var names []string
	for _, d := range []time.Duration{48 * time.Hour, time.Second, 0, 30 * 24 * time.Hour} {
		names = append(names, backup.NewName(base.Add(d), key, false).String())
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for i := 1; i < len(sorted); i++ {
		a, _ := backup.ParseName(sorted[i-1])
		b, _ := backup.ParseName(sorted[i])
		if !a.Taken.Before(b.Taken) {
			t.Fatalf("sorted names are not in time order: %v", sorted)
		}
	}
}

// TestParseName_RefusesWhatItDidNotWrite covers names that look close.
func TestParseName_RefusesWhatItDidNotWrite(t *testing.T) {
	for _, s := range []string{
		"", "pleiades.dump", "pleiades-20260918T090203Z-3f9ac21b.sql",
		"pleiades-20260918T090203Z-3F9AC21B.dump", "pleiades-20260918T090203Z-3f9ac21.dump",
		"pleiades-20261318T090203Z-3f9ac21b.dump", "../pleiades-20260918T090203Z-3f9ac21b.dump",
		"pleiades-20260918T090203Z-3f9ac21b.dump.partial", "x/pleiades-20260918T090203Z-nokey.dump",
	} {
		if n, ok := backup.ParseName(s); ok {
			t.Errorf("ParseName(%q) = %+v, true", s, n)
		}
	}
}
