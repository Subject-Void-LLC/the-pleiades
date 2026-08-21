package zoneinfo_test

import (
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule/zoneinfo"
)

// TestEveryListedZoneLoads is the property the generated list exists to
// guarantee: nothing is offered to an operator that this binary cannot
// then load. A zone in the picker that fails at save time would be the
// worst version of this feature.
func TestEveryListedZoneLoads(t *testing.T) {
	names := zoneinfo.Names()
	if len(names) < 100 {
		t.Fatalf("only %d zones listed; the generated list looks truncated", len(names))
	}
	for _, n := range names {
		if _, err := time.LoadLocation(n); err != nil {
			t.Errorf("listed zone %q does not load: %v", n, err)
		}
	}
}

func TestNamesIsSortedAndCopied(t *testing.T) {
	names := zoneinfo.Names()
	if !sort.StringsAreSorted(names) {
		t.Error("Names is not sorted; a picker's order would be arbitrary")
	}
	// Mutating the returned slice must not affect the allowlist.
	if len(names) > 0 {
		names[0] = "TAMPERED"
	}
	if !zoneinfo.Valid(zoneinfo.Names()[0]) {
		t.Error("mutating the slice Names returned corrupted the allowlist")
	}
}

// TestLoadRefusesHostileNames covers Phase 23's Schema/Injection Hardening
// gate for the one operator-supplied string that is used to look something
// up by path.
func TestLoadRefusesHostileNames(t *testing.T) {
	hostile := []string{
		"../../etc/passwd",
		"/etc/passwd",
		"America/../../../etc/passwd",
		"America/New_York/../../etc/shadow",
		"",
		".",
		"..",
		"UTC\x00/etc/passwd",
		"America/New_York\n",
		" America/New_York",
		"america/new_york",
		"Not/AZone",
		"Local",
	}
	for _, name := range hostile {
		t.Run(name, func(t *testing.T) {
			if zoneinfo.Valid(name) {
				t.Fatalf("Valid(%q) accepted a name outside the allowlist", name)
			}
			loc, err := zoneinfo.Load(name)
			if err == nil {
				t.Fatalf("Load(%q) returned %v; it must be refused", name, loc)
			}
			var unknown *zoneinfo.UnknownZoneError
			if !errors.As(err, &unknown) {
				t.Errorf("Load(%q) = %v, want an *UnknownZoneError so a handler can answer 400", name, err)
			}
		})
	}
}

// TestLoadAcceptsRealZones proves the allowlist is not so strict it refuses
// the zones the release gate itself depends on.
func TestLoadAcceptsRealZones(t *testing.T) {
	for _, name := range []string{
		"UTC", "America/New_York", "Europe/London",
		"Australia/Sydney", "Asia/Kolkata",
	} {
		loc, err := zoneinfo.Load(name)
		if err != nil {
			t.Errorf("Load(%q): %v", name, err)
			continue
		}
		if loc.String() != name {
			t.Errorf("Load(%q) returned location %q", name, loc)
		}
	}
}

func TestCommonIsAllValid(t *testing.T) {
	common := zoneinfo.Common()
	if len(common) < 10 {
		t.Fatalf("Common returned only %d zones", len(common))
	}
	if common[0] != "UTC" {
		t.Errorf("Common starts with %q; UTC should lead the picker", common[0])
	}
	for _, n := range common {
		if !zoneinfo.Valid(n) {
			t.Errorf("Common offers %q, which is not in the allowlist", n)
		}
	}
}
