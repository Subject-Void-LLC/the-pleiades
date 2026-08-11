// Package announce is the operator-facing broadcast: a message an
// administrator puts in front of the people about to dispatch automation.
//
// It exists because "change freeze until Monday" and "the east region is
// degraded, do not dispatch" have to reach whoever is about to act, not
// whoever happened to be reading a chat channel. The dashboard is the one
// surface every operator passes through, so it is where the message goes.
//
// It is deliberately not a notification. This is a broadcast a human writes
// and every permitted reader sees, with a lifetime measured in days; Phase
// 28's Notification Engine is a per-event delivery to a configured target,
// with a lifetime of one event. Conflating them would mean one mechanism
// serving two audiences with two lifetimes, and the seam would land in the
// wrong place the first time either changed.
package announce

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Level is how prominently a message renders.
//
// It reuses the vocabulary the UI's status badges already use rather than
// inventing a second severity scale, so an announcement and a job outcome
// that mean the same thing look the same.
type Level string

// The levels, least to most urgent.
const (
	LevelInfo     Level = "info"
	LevelChanged  Level = "changed"
	LevelWarning  Level = "warning"
	LevelCritical Level = "critical"
)

// levelClasses maps a Level onto the closed set of badge classes the
// stylesheet has proven contrast for. A value outside the set renders
// neutral rather than being interpolated into a class attribute.
var levelClasses = map[Level]string{
	LevelInfo:     "badge-neutral",
	LevelChanged:  "badge-changed",
	LevelWarning:  "badge-skipped",
	LevelCritical: "badge-failed",
}

// Class returns the badge class for a level, neutral for anything
// unrecognized.
func (l Level) Class() string {
	if class, ok := levelClasses[l]; ok {
		return class
	}
	return "badge-neutral"
}

// ParseLevel reads a stored or submitted level.
//
// An unrecognized value falls back to info rather than erroring. That is
// the opposite of how a classification banner parses, and deliberately: a
// banner that silently downgraded would misrepresent how sensitive a system
// is, whereas an announcement that renders less loudly than intended is
// still displayed and still readable. Refusing would mean a level added by
// a newer version makes an older replica drop the message entirely.
func ParseLevel(raw string) Level {
	level := Level(strings.ToLower(strings.TrimSpace(raw)))
	if _, ok := levelClasses[level]; ok {
		return level
	}
	return LevelInfo
}

// Announcement is one broadcast message.
type Announcement struct {
	ID    int
	Title string
	Body  string
	Level Level

	// StartsAt and EndsAt bound when this is live. Both optional, and they
	// mean different things: no start is "already showing", no end is
	// "until somebody takes it down".
	StartsAt *time.Time
	EndsAt   *time.Time

	// OrganizationID is 0 for a system-wide message. The absence is
	// meaningful rather than missing: a platform maintenance notice has to
	// reach every tenant, while a tenant's own change freeze must not leak
	// that tenant's plans to the others.
	OrganizationID int

	// Author is captured at write time rather than joined. It is a
	// historical fact about who said this, and resolving it live would let
	// a deleted account silently blank the attribution on a message people
	// are still being asked to act on.
	Author string

	CreatedAt time.Time
}

// Live reports whether this announcement is showing at the given instant.
//
// Evaluated with no grace period either side. A tolerance would mean a
// change freeze that has visibly ended still being displayed, which is
// worse than one that disappears the moment it expires.
func (a Announcement) Live(now time.Time) bool {
	if a.StartsAt != nil && now.Before(*a.StartsAt) {
		return false
	}
	if a.EndsAt != nil && !now.Before(*a.EndsAt) {
		return false
	}
	return true
}

// SystemWide reports whether this message reaches every tenant.
func (a Announcement) SystemWide() bool { return a.OrganizationID == 0 }

// ErrNotFound is returned when an id names no announcement.
var ErrNotFound = errors.New("announcement not found")

// Query is a list request.
type Query struct {
	// OrganizationIDs restricts tenant-scoped messages to these tenants.
	// System-wide messages are returned regardless, because that is what
	// system-wide means; a caller that wanted only one tenant's messages
	// would be asking a different question than any real reader asks.
	OrganizationIDs []int

	// LiveAt, when non-zero, returns only messages live at that instant.
	// Zero returns every message, which is what an administrator managing
	// them needs and what an operator reading the dashboard must not get.
	LiveAt time.Time

	// Limit bounds the page. Zero means the store's default.
	Limit int
}

// Store persists announcements.
type Store interface {
	Create(ctx context.Context, a Announcement) (Announcement, error)
	Get(ctx context.Context, id int) (Announcement, error)
	List(ctx context.Context, q Query) ([]Announcement, error)
	Update(ctx context.Context, a Announcement) error
	Delete(ctx context.Context, id int) error
}
