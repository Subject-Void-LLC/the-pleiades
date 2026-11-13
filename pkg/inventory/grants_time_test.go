// Tests for the time a grant records: every time a grant may hold reads
// back as the same instant, and one it cannot read is reported, never
// passed off as the zero time.
package inventory_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// grantAt is a granted property holding one assertion of acme.widget
// whose recorded time is at, or none when at is nil.
func grantAt(at any) inventory.Properties {
	entry := map[string]any{"source": "assertion", "reason": "r"}
	if at != nil {
		entry["at"] = at
	}
	return granted(map[string]any{"acme.widget": entry})
}

// TestGrantsFrom_ATimeItCannotReadLeavesTheGrantUnreadable: a recorded
// time that is missing, not text, not RFC 3339, or outside the years a
// grant may hold once read in UTC leaves the grant unreadable, saying why,
// with no time; the record itself still loads.
func TestGrantsFrom_ATimeItCannotReadLeavesTheGrantUnreadable(t *testing.T) {
	for name, tc := range map[string]struct {
		at   any
		want string
	}{
		"no time":                     {nil, "records no time"},
		"a number":                    {float64(1700000000), "not text"},
		"words":                       {"yesterday", `"yesterday" is not an RFC 3339 time`},
		"a date alone":                {"2026-10-22", "not an RFC 3339 time"},
		"the zero time":               {"0001-01-01T00:00:00Z", "records no time"},
		"the year 0":                  {"0000-06-01T00:00:00Z", "year 0"},
		"before year 1 once in UTC":   {"0001-01-01T00:30:00+01:00", "year 0"},
		"after year 9999 once in UTC": {"9999-12-31T23:00:00-02:00", "year 10000"},
		"a very long string":          {strings.Repeat("9", 4096), "..."},
	} {
		t.Run(name, func(t *testing.T) {
			grants, _, err := inventory.GrantsFrom(grantAt(tc.at))
			if err != nil || len(grants) != 1 {
				t.Fatalf("GrantsFrom = %+v, %v; want the one grant, read as unreadable", grants, err)
			}
			g := grants[0]
			if !g.At.IsZero() || !strings.Contains(g.Unreadable, tc.want) {
				t.Errorf("read back at %v, unreadable %q; want no time and a reason saying %q", g.At, g.Unreadable, tc.want)
			}
			if len(g.Unreadable) > 200 {
				t.Errorf("the reason is %d bytes, quoting too much of an untrusted record", len(g.Unreadable))
			}
		})
	}
}

// TestGrants_EveryTimeItAcceptsSurvivesItsEncoding: a time a grant reads
// (a fraction of a second, an offset, the first and last years it may
// hold) is the same instant once written and read again, and the time it
// writes reads back with nothing lost.
func TestGrants_EveryTimeItAcceptsSurvivesItsEncoding(t *testing.T) {
	for _, at := range []string{
		"2026-10-22T09:00:00Z",
		"2024-05-06T07:08:09.123456789+02:00",
		"2024-05-06T07:08:09.5Z",
		"0001-01-01T00:00:01Z",
		"0001-01-01T01:30:00+01:00",
		"9999-12-31T23:59:59.999999999Z",
		"9999-12-31T22:00:00-01:00",
	} {
		t.Run(at, func(t *testing.T) {
			first, _, err := inventory.GrantsFrom(grantAt(at))
			if err != nil || len(first) != 1 || first[0].Unreadable != "" {
				t.Fatalf("GrantsFrom(%q) = %+v, %v; want a readable grant", at, first, err)
			}
			again, _, err := inventory.GrantsFrom(granted(inventory.GrantsProperty(first)))
			if err != nil || len(again) != 1 || again[0].Unreadable != "" || !again[0].At.Equal(first[0].At) {
				t.Fatalf("written and read again, %q became %+v (%v); want %v", at, again, err, first[0].At)
			}
		})
	}
}

// TestGrantsProperty_WritesNoTimeItCannotReadBack: a grant holding a time
// outside the years a grant may hold, or none, is written with no time,
// so it reads back unreadable rather than as some other time.
func TestGrantsProperty_WritesNoTimeItCannotReadBack(t *testing.T) {
	for name, at := range map[string]time.Time{
		"no time":         {},
		"after year 9999": time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
		"before year 1":   time.Date(0, 6, 1, 0, 0, 0, 0, time.UTC),
	} {
		t.Run(name, func(t *testing.T) {
			encoded := inventory.GrantsProperty([]inventory.Grant{{Capability: "acme.widget", Source: inventory.GrantAsserted, At: at}})
			if _, written := encoded["acme.widget"].(map[string]any)["at"]; written {
				t.Fatalf("a time a grant cannot hold was written: %v", encoded)
			}
			back, _, err := inventory.GrantsFrom(granted(encoded))
			if err != nil || len(back) != 1 || back[0].Unreadable == "" || !back[0].At.IsZero() {
				t.Errorf("read back as %+v, %v; want unreadable", back, err)
			}
		})
	}
}

// TestValidateGrantTime: a grant holds a time from the year 1 to the year
// 9999 in UTC, and nothing else.
func TestValidateGrantTime(t *testing.T) {
	for _, ok := range []time.Time{time.Now(), time.Unix(0, 0), time.Date(1, 1, 1, 0, 0, 1, 0, time.UTC), time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)} {
		if err := inventory.ValidateGrantTime(ok); err != nil {
			t.Errorf("%v refused: %v", ok, err)
		}
	}
	for _, bad := range []time.Time{{}, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(0, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(9999, 12, 31, 23, 0, 0, 0, time.FixedZone("west", -2*3600))} {
		if err := inventory.ValidateGrantTime(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
