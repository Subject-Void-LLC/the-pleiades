package announce_test

import (
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/announce"
)

// This file covers the domain type: the level vocabulary, the badge mapping,
// and the liveness window. These are pure functions, and every one of them
// answers a question the UI asks on every page load.

func TestLevel_ClassStaysInsideTheValidatedBadgeSet(t *testing.T) {
	// The stylesheet has computed contrast only for these four classes, and
	// the value reaches a class attribute, so an unrecognized level must land
	// on a real class rather than being interpolated as itself.
	valid := map[string]bool{
		"badge-neutral": true, "badge-changed": true,
		"badge-skipped": true, "badge-failed": true,
	}

	for _, level := range []announce.Level{
		announce.LevelInfo, announce.LevelChanged,
		announce.LevelWarning, announce.LevelCritical,
		announce.Level("apocalyptic"), announce.Level(""),
	} {
		if class := level.Class(); !valid[class] {
			t.Errorf("Level(%q).Class() = %q, which is not a validated badge class", level, class)
		}
	}
}

func TestLevel_ClassIsDistinctPerLevel(t *testing.T) {
	// Four levels mapping onto four classes. If two collided, the badge would
	// stop carrying information and the level would be decorative.
	seen := map[string]announce.Level{}
	for _, level := range []announce.Level{
		announce.LevelInfo, announce.LevelChanged,
		announce.LevelWarning, announce.LevelCritical,
	} {
		class := level.Class()
		if other, clash := seen[class]; clash {
			t.Errorf("levels %q and %q both render %q", other, level, class)
		}
		seen[class] = level
	}
}

// TestParseLevel_FallsBackRatherThanFailing pins the deliberate asymmetry
// with the classification banner, which refuses an unknown value. A banner
// that silently downgraded would misrepresent how sensitive a system is; an
// announcement that renders less loudly than intended is still displayed and
// still readable. Refusing would let a level added by a newer version make an
// older replica drop the message entirely.
func TestParseLevel_FallsBackRatherThanFailing(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want announce.Level
	}{
		{"info", announce.LevelInfo},
		{"changed", announce.LevelChanged},
		{"warning", announce.LevelWarning},
		{"critical", announce.LevelCritical},
		{"CRITICAL", announce.LevelCritical},
		{"  Warning  ", announce.LevelWarning},
		{"apocalyptic", announce.LevelInfo},
		{"", announce.LevelInfo},
	} {
		if got := announce.ParseLevel(tc.raw); got != tc.want {
			t.Errorf("ParseLevel(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// TestAnnouncement_Live walks the window, including both open-ended shapes.
// The boundaries are asserted explicitly because they are the whole
// behaviour: no start means "already showing", no end means "until somebody
// takes it down", and there is deliberately no grace period either side.
func TestAnnouncement_Live(t *testing.T) {
	at := func(day int) *time.Time {
		d := time.Date(2026, 6, day, 0, 0, 0, 0, time.UTC)
		return &d
	}
	now := *at(10)

	for _, tc := range []struct {
		name   string
		starts *time.Time
		ends   *time.Time
		want   bool
	}{
		{"no window at all is always live", nil, nil, true},
		{"inside a closed window", at(1), at(20), true},
		{"before it starts", at(11), at(20), false},
		{"after it ends", at(1), at(5), false},
		{"no start, not yet ended", nil, at(20), true},
		{"no start, already ended", nil, at(5), false},
		{"started, no end", at(1), nil, true},
		{"starts exactly now is live", at(10), nil, true},
		{"ends exactly now is not live", nil, at(10), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := announce.Announcement{StartsAt: tc.starts, EndsAt: tc.ends}
			if got := a.Live(now); got != tc.want {
				t.Errorf("Live(%v) = %v, want %v", now, got, tc.want)
			}
		})
	}
}

// TestAnnouncement_SystemWide pins the meaning of the absent organization.
// The absence is meaningful rather than missing: a platform maintenance
// notice has to reach every tenant.
func TestAnnouncement_SystemWide(t *testing.T) {
	if !(announce.Announcement{}).SystemWide() {
		t.Error("an announcement with no organization is not system-wide")
	}
	if (announce.Announcement{OrganizationID: 7}).SystemWide() {
		t.Error("a tenant-scoped announcement reported itself system-wide")
	}
}
