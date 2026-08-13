package activity

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entactivity "github.com/Subject-Void-LLC/the-pleiades/internal/ent/activityentry"
)

// defaultPageSize bounds a List with no explicit limit.
const defaultPageSize = 50

// maxPageSize is the ceiling a caller cannot raise. The stream is the one
// table that grows without bound, so an unbounded read here would be the
// easiest way to make the control plane allocate a database's worth of rows
// from one request.
const maxPageSize = 200

type entStore struct{ client *ent.Client }

// NewEntStore builds a Store over an already-open ent client.
func NewEntStore(client *ent.Client) Store { return &entStore{client: client} }

// Record implements Recorder.
func (s *entStore) Record(ctx context.Context, e Entry) error {
	if err := e.Validate(); err != nil {
		return err
	}

	_, err := s.client.ActivityEntry.Create().
		SetActor(strings.TrimSpace(e.Actor)).
		SetAction(string(e.Action)).
		SetObjectKind(strings.TrimSpace(e.ObjectKind)).
		SetObjectID(e.ObjectID).
		SetObjectName(strings.TrimSpace(e.ObjectName)).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("activity: recording %s of %s %d: %w",
			e.Action, e.ObjectKind, e.ObjectID, err)
	}
	return nil
}

// List implements Store, newest first.
func (s *entStore) List(ctx context.Context, q Query) ([]Entry, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}

	query := s.client.ActivityEntry.Query().
		Order(ent.Desc(entactivity.FieldID)).
		Limit(limit)

	// The cursor walks downward, because the stream reads newest first.
	// Paging on the primary key rather than on created_at is deliberate:
	// two entries written in the same millisecond are indistinguishable by
	// time, and a timestamp cursor would either skip one or repeat it.
	if q.After > 0 {
		query = query.Where(entactivity.IDLT(q.After))
	}
	if actor := strings.TrimSpace(q.Actor); actor != "" {
		query = query.Where(entactivity.ActorEQ(actor))
	}
	if kind := strings.TrimSpace(q.ObjectKind); kind != "" {
		query = query.Where(entactivity.ObjectKindEQ(kind))
		// Only alongside a kind: an object id on its own means nothing
		// across tables, and applying it alone would silently mix one
		// organization's history with the team that happens to share its
		// primary key.
		if q.ObjectID > 0 {
			query = query.Where(entactivity.ObjectIDEQ(q.ObjectID))
		}
	}

	rows, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("activity: listing entries: %w", err)
	}

	out := make([]Entry, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrate(row))
	}
	return out, nil
}

// Get implements Store.
func (s *entStore) Get(ctx context.Context, id int) (Entry, error) {
	row, err := s.client.ActivityEntry.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return Entry{}, ErrNotFound
		}
		return Entry{}, fmt.Errorf("activity: loading entry %d: %w", id, err)
	}
	return hydrate(row), nil
}

// hydrate projects a stored row onto the domain type.
func hydrate(row *ent.ActivityEntry) Entry {
	return Entry{
		ID:         row.ID,
		Actor:      row.Actor,
		Action:     Action(row.Action),
		ObjectKind: row.ObjectKind,
		ObjectID:   row.ObjectID,
		ObjectName: row.ObjectName,
		At:         row.CreatedAt,
	}
}
