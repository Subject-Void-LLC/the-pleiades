package announce

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entannouncement "github.com/Subject-Void-LLC/the-pleiades/internal/ent/announcement"
	entorg "github.com/Subject-Void-LLC/the-pleiades/internal/ent/organization"
)

// defaultPageSize bounds a List with no explicit limit.
const defaultPageSize = 50

// maxPageSize is the ceiling a caller cannot raise.
const maxPageSize = 200

type entStore struct{ client *ent.Client }

// NewEntStore builds a Store over an already-open ent client.
func NewEntStore(client *ent.Client) Store { return &entStore{client: client} }

// Create persists a new announcement.
func (s *entStore) Create(ctx context.Context, a Announcement) (Announcement, error) {
	if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Body) == "" {
		return Announcement{}, fmt.Errorf("announce: an announcement needs a title and a body")
	}
	if strings.TrimSpace(a.Author) == "" {
		// Refused rather than defaulted to something like "system". An
		// unattributed instruction to change how production is operated is
		// one nobody can question or follow up on.
		return Announcement{}, fmt.Errorf("announce: an announcement needs an author")
	}

	builder := s.client.Announcement.Create().
		SetTitle(a.Title).
		SetBody(a.Body).
		SetLevel(string(ParseLevel(string(a.Level)))).
		SetAuthor(a.Author).
		SetNillableStartsAt(a.StartsAt).
		SetNillableEndsAt(a.EndsAt)

	if a.OrganizationID != 0 {
		builder = builder.SetOrganizationID(a.OrganizationID)
	}

	created, err := builder.Save(ctx)
	if err != nil {
		return Announcement{}, fmt.Errorf("announce: creating announcement: %w", err)
	}
	return s.Get(ctx, created.ID)
}

// Get loads one announcement.
func (s *entStore) Get(ctx context.Context, id int) (Announcement, error) {
	row, err := s.client.Announcement.Query().
		Where(entannouncement.IDEQ(id)).
		WithOrganization().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Announcement{}, fmt.Errorf("%w: %d", ErrNotFound, id)
		}
		return Announcement{}, fmt.Errorf("announce: loading announcement %d: %w", id, err)
	}
	return hydrate(row), nil
}

// List returns announcements matching q, most recent first.
//
// Most recent first, unlike every other list in this codebase, because an
// announcement's value decays: the newest change freeze is the one that
// applies, and a reader who sees only the first few must see those.
func (s *entStore) List(ctx context.Context, q Query) ([]Announcement, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	limit = min(limit, maxPageSize)

	query := s.client.Announcement.Query().
		WithOrganization().
		Order(ent.Desc(entannouncement.FieldCreatedAt)).
		Limit(limit)

	// A system-wide message has no organization and reaches everyone, so
	// the tenant filter is "mine OR nobody's" rather than "mine". Written
	// as an Or rather than two queries because a caller assembling the
	// union itself would have to re-sort and re-limit the merge, and would
	// get the boundary wrong the first time a page filled up.
	if len(q.OrganizationIDs) > 0 {
		query = query.Where(entannouncement.Or(
			entannouncement.Not(entannouncement.HasOrganization()),
			entannouncement.HasOrganizationWith(entorg.IDIn(q.OrganizationIDs...)),
		))
	}

	if !q.LiveAt.IsZero() {
		// Live means started (or no start) and not yet ended (or no end).
		// Expressed in the query rather than filtered in Go, so a page of
		// fifty live messages is fifty rows rather than fifty survivors of
		// however many expired ones happened to sort first.
		query = query.Where(
			entannouncement.Or(
				entannouncement.StartsAtIsNil(),
				entannouncement.StartsAtLTE(q.LiveAt),
			),
			entannouncement.Or(
				entannouncement.EndsAtIsNil(),
				entannouncement.EndsAtGT(q.LiveAt),
			),
		)
	}

	rows, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("announce: listing announcements: %w", err)
	}

	out := make([]Announcement, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrate(row))
	}
	return out, nil
}

// Update saves an announcement's editable fields.
//
// The author is not among them: it is immutable by schema, because an
// attribution somebody can rewrite is an attribution nobody can rely on.
func (s *entStore) Update(ctx context.Context, a Announcement) error {
	update := s.client.Announcement.UpdateOneID(a.ID).
		SetTitle(a.Title).
		SetBody(a.Body).
		SetLevel(string(ParseLevel(string(a.Level))))

	// Clear-then-set, so removing an end date is expressible. Without the
	// clear, a nil pointer would be indistinguishable from "leave it alone"
	// and an announcement could never be made permanent again.
	if a.StartsAt != nil {
		update = update.SetStartsAt(*a.StartsAt)
	} else {
		update = update.ClearStartsAt()
	}
	if a.EndsAt != nil {
		update = update.SetEndsAt(*a.EndsAt)
	} else {
		update = update.ClearEndsAt()
	}

	err := update.Exec(ctx)
	if ent.IsNotFound(err) {
		return fmt.Errorf("%w: %d", ErrNotFound, a.ID)
	}
	if err != nil {
		return fmt.Errorf("announce: updating announcement %d: %w", a.ID, err)
	}
	return nil
}

// Delete removes an announcement.
//
// A hard delete rather than an archived flag. A retired notice has no
// audience and no audit value -- it was advisory text, not a record of what
// the platform did -- and a lingering one is another condition every future
// query has to remember to exclude.
func (s *entStore) Delete(ctx context.Context, id int) error {
	err := s.client.Announcement.DeleteOneID(id).Exec(ctx)
	if ent.IsNotFound(err) {
		return fmt.Errorf("%w: %d", ErrNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("announce: deleting announcement %d: %w", id, err)
	}
	return nil
}

func hydrate(row *ent.Announcement) Announcement {
	a := Announcement{
		ID:        row.ID,
		Title:     row.Title,
		Body:      row.Body,
		Level:     ParseLevel(row.Level),
		StartsAt:  row.StartsAt,
		EndsAt:    row.EndsAt,
		Author:    row.Author,
		CreatedAt: row.CreatedAt,
	}
	if row.Edges.Organization != nil {
		a.OrganizationID = row.Edges.Organization.ID
	}
	return a
}
