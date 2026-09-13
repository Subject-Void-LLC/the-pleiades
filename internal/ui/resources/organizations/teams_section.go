// Package organizations' Teams section: the teams an organization is composed
// of.
//
// The relationship was walkable one way only before this existed. A team names
// its organization and links to it; the organization had no way back.
package organizations

import (
	"context"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// sectionLimit bounds one section's read.
//
// A section is a drill-down, not a listing: somebody who needs every team in
// a large organization goes to the Teams view, which pages. Two hundred is
// enough that the cap is never reached in practice and small enough that a
// pathological organization cannot make this page expensive.
const sectionLimit = 200

// teamsSection is an organization's teams.
//
// AWX's organization carries a Teams tab and this one did not, which left the
// relationship walkable in one direction only: a team names its organization
// and links to it, and the organization had no way back. That is the shape of
// gap this whole pass is about -- the data was already there, the port already
// filtered by organization, and nothing rendered it.
//
// Teams rather than Users, which is what AWX puts under Access, because this
// platform grants roles to teams and never to a person: a team is the unit an
// organization is actually composed of here.
func teamsSection(s access.Teams) view.Section {
	return view.Section{
		Status:  view.StatusImplemented,
		Title:   "Teams",
		Summary: "The teams in this organization. Roles are granted to teams, never to a person, so this is who can be given access to anything it owns.",
		Empty:   "This organization has no teams, so nothing can be granted access to what it owns.",
		Fields: []view.Field{
			{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true, References: "teams"},
			{Name: "description", Label: "DESCRIPTION", Kind: view.KindText, InList: true},
		},
		Rows: func(ctx context.Context, parentID string) ([]view.Row, error) {
			// A section under no record must not answer with every team in
			// the deployment, which is what an unfiltered query would do.
			id, err := strconv.Atoi(parentID)
			if err != nil || id < 1 {
				return nil, nil
			}

			teams, err := s.ListTeams(ctx, access.TeamQuery{
				Query:           access.Query{Limit: sectionLimit},
				OrganizationIDs: []int{id},
			})
			if err != nil {
				return nil, err
			}

			rows := make([]view.Row, 0, len(teams))
			for _, team := range teams {
				rows = append(rows, view.Row{
					ID: strconv.Itoa(team.ID),
					Cells: view.Cells{
						"name":        team.Name,
						"description": team.Description,
					},
					// The cell shows the name and the link is built from
					// the id, which is what every team URL uses.
					Refs: map[string]string{"name": strconv.Itoa(team.ID)},
				})
			}
			return rows, nil
		},
	}
}
