package access

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entcontact "github.com/Subject-Void-LLC/the-pleiades/internal/ent/contact"
	entorg "github.com/Subject-Void-LLC/the-pleiades/internal/ent/organization"
	entteam "github.com/Subject-Void-LLC/the-pleiades/internal/ent/team"
)

// This file is the ent adapter for Contacts. It is its own file rather than
// more of ent_store.go because the exactly-one-owner invariant is the whole
// of its complexity, and burying it among four other entities' CRUD is how
// it stops being noticed.

// CreateContact persists a contact against exactly one owner.
func (s *entStore) CreateContact(ctx context.Context, contact Contact) (Contact, error) {
	if err := validateContact(contact); err != nil {
		return Contact{}, err
	}

	create := s.client.Contact.Create().
		SetName(strings.TrimSpace(contact.Name)).
		SetRole(string(contact.Role)).
		SetEmail(strings.TrimSpace(contact.Email)).
		SetPhone(strings.TrimSpace(contact.Phone)).
		SetURL(strings.TrimSpace(contact.URL)).
		SetNotes(strings.TrimSpace(contact.Notes)).
		SetDisplayOrder(contact.Order)

	// validateContact has already established that exactly one is set, so
	// this is a choice between two known-good branches rather than a second
	// place deciding the invariant.
	if contact.OwnedByOrganization() {
		create = create.SetOrganizationID(contact.OrganizationID)
	} else {
		create = create.SetTeamID(contact.TeamID)
	}

	created, err := create.Save(ctx)
	if err != nil {
		// A named owner that does not exist arrives as a constraint error,
		// and reporting it as ErrInUse or ErrExists would describe the
		// wrong record entirely. It is the owner that is missing.
		if ent.IsConstraintError(err) {
			return Contact{}, fmt.Errorf("%w: %s", ErrNotFound, ownerLabel(contact))
		}
		return Contact{}, fmt.Errorf("access: creating contact %q: %w", contact.Name, err)
	}
	return s.GetContact(ctx, created.ID)
}

// GetContact loads one contact with its owner resolved.
func (s *entStore) GetContact(ctx context.Context, id int) (Contact, error) {
	row, err := s.client.Contact.Query().
		Where(entcontact.IDEQ(id)).
		WithOrganization().
		WithTeam().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Contact{}, fmt.Errorf("%w: contact %d", ErrNotFound, id)
		}
		return Contact{}, fmt.Errorf("access: loading contact %d: %w", id, err)
	}
	return hydrateContact(row), nil
}

// ListContacts returns a page of contacts for one owner, or across the
// deployment when no owner is named.
//
// Ordered by display order and then by id, because an escalation path is a
// sequence: "who do I try first" is the question being asked, and answering
// it in insertion order would make the order depend on which contact somebody
// happened to type in first.
func (s *entStore) ListContacts(ctx context.Context, q ContactQuery) ([]Contact, error) {
	query := s.client.Contact.Query().
		WithOrganization().
		WithTeam().
		Order(ent.Asc(entcontact.FieldDisplayOrder), ent.Asc(entcontact.FieldID)).
		Limit(boundLimit(q.Limit))

	if q.OrganizationID > 0 {
		query = query.Where(entcontact.HasOrganizationWith(entorg.IDEQ(q.OrganizationID)))
	}
	if q.TeamID > 0 {
		query = query.Where(entcontact.HasTeamWith(entteam.IDEQ(q.TeamID)))
	}
	if q.After > 0 {
		query = query.Where(entcontact.IDGT(q.After))
	}
	if q.Search != "" {
		query = query.Where(entcontact.Or(
			entcontact.NameContainsFold(q.Search),
			entcontact.EmailContainsFold(q.Search),
		))
	}

	rows, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("access: listing contacts: %w", err)
	}
	out := make([]Contact, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrateContact(row))
	}
	return out, nil
}

// UpdateContact changes a contact's details.
//
// The owner is carried forward from storage rather than taken from the
// submission, the same rule UpdateTeam applies to a team's organization.
// Moving a contact between owners is indistinguishable from deleting one and
// creating another, and an accountability record that quietly changed what it
// was accountable for would defeat the attestation beside it.
func (s *entStore) UpdateContact(ctx context.Context, contact Contact) error {
	current, err := s.GetContact(ctx, contact.ID)
	if err != nil {
		return err
	}
	contact.OrganizationID, contact.TeamID = current.OrganizationID, current.TeamID

	if err := validateContact(contact); err != nil {
		return err
	}
	err = s.client.Contact.UpdateOneID(contact.ID).
		SetName(strings.TrimSpace(contact.Name)).
		SetRole(string(contact.Role)).
		SetEmail(strings.TrimSpace(contact.Email)).
		SetPhone(strings.TrimSpace(contact.Phone)).
		SetURL(strings.TrimSpace(contact.URL)).
		SetNotes(strings.TrimSpace(contact.Notes)).
		SetDisplayOrder(contact.Order).
		Exec(ctx)
	return mapWriteError(err, "contact", contact.ID, contact.Name)
}

// DeleteContact removes a contact.
//
// No last-contact guard, deliberately, and the asymmetry with the
// last-system-grant guard is the point. Refusing to delete the final grant
// protects against locking every administrator out of a running deployment,
// which is unrecoverable from inside the product. An owner-less organization
// is a bad state but a visible and fixable one, and a store that refused the
// deletion would leave somebody unable to remove a contact who has left.
// Surfacing it is the job of the attestation, which goes stale and says so.
func (s *entStore) DeleteContact(ctx context.Context, id int) error {
	err := s.client.Contact.DeleteOneID(id).Exec(ctx)
	return mapWriteError(err, "contact", id, "")
}

// ownerLabel names the owner a rejected contact claimed, for an error a
// reader can act on.
func ownerLabel(c Contact) string {
	if c.OwnedByOrganization() {
		return fmt.Sprintf("organization %d", c.OrganizationID)
	}
	return fmt.Sprintf("team %d", c.TeamID)
}

func hydrateContact(row *ent.Contact) Contact {
	contact := Contact{
		ID:        row.ID,
		Name:      row.Name,
		Role:      ContactRole(row.Role),
		Email:     row.Email,
		Phone:     row.Phone,
		URL:       row.URL,
		Notes:     row.Notes,
		Order:     row.DisplayOrder,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	if row.Edges.Organization != nil {
		contact.OrganizationID = row.Edges.Organization.ID
	}
	if row.Edges.Team != nil {
		contact.TeamID = row.Edges.Team.ID
	}
	return contact
}
