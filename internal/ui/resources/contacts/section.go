package contacts

import (
	"context"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// This file is the per-record Contacts section: who answers for one
// organization or one team, rendered under that record.
//
// It exists for the reason the Access section does, and the two are the same
// argument applied to the two halves of accountability. The deployment-wide
// Contacts view is the reviewer's page: which tenants have nobody named
// against them, across everything. The section answers "who answers for
// *this*", which is the question somebody has while looking at a tenant that
// is behaving strangely at an hour when the answer matters.
//
// It lives here rather than in the Organizations and Teams packages because
// a contact's presentation belongs to the view that owns contacts. Two
// copies of the column set would drift the first time a column changed, and
// the drift would be invisible: both pages would render, and they would say
// different things about the same person.

// sectionFields are the section's columns.
//
// Not the same list as the table's, for the reason the Access section's own
// are not. The owner is the page you are on, so a column naming it would be
// one column of the same value repeated; the order column goes too, because
// the rows arrive in it and a number saying "1, 2, 3" beside rows already in
// that sequence is a column that carries nothing.
//
// What the section keeps that the table drops is the notes, and that is the
// whole reason it is a different list. "Sev1 only after 22:00 UTC" is
// context somebody needs at the moment they are deciding who to call, and
// this is the page they are on when they decide.
var sectionFields = []view.Field{
	{
		Name: "role", Label: "ROLE", Kind: view.KindText, InList: true,
	},
	{
		Name: "name", Label: "NAME", Kind: view.KindText,
		InList: true, MobilePrimary: true,
	},
	{Name: "reach", Label: "REACH", Kind: view.KindText, InList: true},
	{Name: "notes", Label: "NOTES", Kind: view.KindText, InList: true},
}

// sectionRow projects one contact for a section.
func sectionRow(c access.Contact) view.Row {
	return view.Row{
		ID: strconv.Itoa(c.ID),
		Cells: view.Cells{
			"role":  string(c.Role),
			"name":  c.Name,
			"reach": reach(c),
			"notes": c.Notes,
		},
	}
}

// SectionForOrganization is the Contacts section of one tenant.
func SectionForOrganization(s access.Contacts) view.Section {
	return section(s, "organization", func(id int) access.ContactQuery {
		return access.ContactQuery{Query: access.Query{Limit: 200}, OrganizationID: id}
	})
}

// SectionForTeam is the Contacts section of one team.
//
// A separate builder rather than a flag, matching the Access section's own
// split, because the query narrows on a different column and a single
// builder taking "which column" would be a parameter whose two values are
// the whole of its behaviour.
func SectionForTeam(s access.Contacts) view.Section {
	return section(s, "team", func(id int) access.ContactQuery {
		return access.ContactQuery{Query: access.Query{Limit: 200}, TeamID: id}
	})
}

// section builds the shared shape over whichever owner column narrows it.
func section(s access.Contacts, noun string, query func(id int) access.ContactQuery) view.Section {
	return view.Section{
		Status:  view.StatusImplemented,
		Title:   "Contacts",
		Summary: "Who answers for this " + noun + ", in the order they are tried.",
		Fields:  sectionFields,
		// A finding rather than a shrug. An empty Access section means
		// access may still arrive from a broader grant, so its own empty
		// text says where else to look. There is no broader place a
		// contact could come from: nobody named here is nobody, and that is
		// exactly what an access review is looking for.
		Empty: "Nobody is named as accountable for this " + noun + ". No owner would be asked whether it should still exist, and nobody would be woken if its automation misbehaved.",
		Rows: func(ctx context.Context, parentID string) ([]view.Row, error) {
			// A section under no record must not answer with every contact
			// in the deployment, which is what an unnarrowed query would
			// do: the store lists all of them when no owner is named, and
			// that listing belongs to the management view alone.
			id, err := strconv.Atoi(parentID)
			if err != nil || id < 1 {
				return nil, nil
			}

			contacts, err := s.ListContacts(ctx, query(id))
			if err != nil {
				return nil, err
			}

			rows := make([]view.Row, 0, len(contacts))
			for _, c := range contacts {
				rows = append(rows, sectionRow(c))
			}
			return rows, nil
		},
	}
}
