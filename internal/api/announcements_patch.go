// This file is the body PATCH /announcements/{id} accepts, and how it is
// folded over the stored announcement.
//
// PATCH is a partial update, as every other PATCH route here is (patch.go): a
// field the body leaves out keeps its stored value and a field it names is
// applied. An empty starts_at or ends_at removes that bound, as leaving it
// out does on create, and an empty or unrecognized level is info, as on
// create. The route used to decode its create body, so a field the body left
// out was written back empty: a PATCH naming only the body answered 500 for
// the title it blanked, and one naming the title and body reset the level to
// info and erased the window, turning a scheduled change freeze into one
// showing now and forever (FAILURE_PATTERNS 1075).
//
// The rules are the shared ones in patch.go. A null on any field is refused,
// naming it; an unknown key, the author and the id among them, a field named
// twice and a body that is not an object are malformed. The organization is
// fixed, 0 for a system-wide announcement: a different one is refused,
// naming it, where it used to be ignored with a 200 (FAILURE_PATTERNS 1055),
// and the stored one is accepted as an echo.
//
// The handler parses the timestamps, then hands the store a fold, which the
// store runs on the announcement it reads under the row's write lock
// (announce.Store.UpdateWith). The window is checked on the merged
// announcement inside the fold, so moving one bound past the stored other is
// refused, and the store refuses a blank title or body.
package api

import (
	"net/http"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/announce"
)

// announcementNoun names an announcement in the shared PATCH refusals.
const announcementNoun = "an announcement"

// announcementPatchDTO is the body PATCH /announcements/{id} accepts: every
// field of the create body, each a pointer so an absent key keeps its stored
// value.
type announcementPatchDTO struct {
	Title *string `json:"title"`
	Body  *string `json:"body"`
	Level *string `json:"level"`

	// Organization is fixed: moving an announcement between tenants would
	// silently change who it reaches. It is accepted only as the stored
	// value, 0 for a system-wide one; see fixedFieldRefusal.
	Organization *int `json:"organization"`

	// StartsAt and EndsAt are RFC 3339 timestamps, and "" removes the bound.
	StartsAt *string `json:"starts_at"`
	EndsAt   *string `json:"ends_at"`

	// nulls names every field the body set to JSON null, sorted.
	// Unexported, so decoding never fills it.
	nulls []string
}

// UnmarshalJSON decodes the body strictly and records which fields were null.
func (d *announcementPatchDTO) UnmarshalJSON(data []byte) error {
	// fields is the same struct without this method, so decoding into it
	// does not recurse back here.
	type fields announcementPatchDTO
	decoded, nulls, err := decodePatch[fields](data, announcementNoun)
	if err != nil {
		return err
	}
	*d = announcementPatchDTO(decoded)
	d.nulls = nulls
	return nil
}

// announcementWindow is the bounds a PATCH body named, parsed. A set flag
// with a nil time removes that bound.
type announcementWindow struct {
	startsSet, endsSet bool
	starts, ends       *time.Time
}

// window parses the bounds the body named, answering the caller itself on a
// malformed timestamp, as create does.
func (h *AnnouncementHandler) window(w http.ResponseWriter, r *http.Request, d announcementPatchDTO) (announcementWindow, bool) {
	var win announcementWindow
	if d.StartsAt != nil {
		starts, ok := h.parseInstant(w, r, "starts_at", *d.StartsAt)
		if !ok {
			return announcementWindow{}, false
		}
		win.startsSet, win.starts = true, starts
	}
	if d.EndsAt != nil {
		ends, ok := h.parseInstant(w, r, "ends_at", *d.EndsAt)
		if !ok {
			return announcementWindow{}, false
		}
		win.endsSet, win.ends = true, ends
	}
	return win, true
}

// fold returns the change the store runs on the announcement it read under
// the row's lock: refuse a move to another organization, apply the fields
// the body named, and refuse a window that would never be live.
func (d announcementPatchDTO) fold(win announcementWindow) func(announce.Announcement) (announce.Announcement, error) {
	return func(stored announce.Announcement) (announce.Announcement, error) {
		if d.Organization != nil && *d.Organization != stored.OrganizationID {
			return announce.Announcement{}, patchRefusal(fixedFieldMessage("organization", announcementNoun,
				"post the announcement for the other organization instead"))
		}
		patchField(&stored.Title, d.Title)
		patchField(&stored.Body, d.Body)
		if d.Level != nil {
			stored.Level = announce.ParseLevel(*d.Level)
		}
		if win.startsSet {
			stored.StartsAt = win.starts
		}
		if win.endsSet {
			stored.EndsAt = win.ends
		}
		if refusal := windowRefusal(stored); refusal != "" {
			return announce.Announcement{}, patchRefusal(refusal)
		}
		return stored, nil
	}
}

// windowRefusal is the 400 message for an announcement whose window ends
// before it starts, or "" when it does not. Refused rather than silently
// swapped or accepted: a window that ends before it starts is never live, so
// accepting it would mean posting an urgent notice that is invisible from
// the moment it is written, the worst failure for a control whose whole job
// is being seen.
func windowRefusal(a announce.Announcement) string {
	if a.StartsAt != nil && a.EndsAt != nil && !a.EndsAt.After(*a.StartsAt) {
		return "ends_at must be after starts_at"
	}
	return ""
}
