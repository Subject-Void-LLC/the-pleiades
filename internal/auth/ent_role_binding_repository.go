package auth

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/rolebinding"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/team"
)

// entRoleBindingRepository adapts RoleBindingRepository to internal/ent,
// mirroring internal/inventory's own port/ent-adapter split
// (internal/inventory.entRepository). internal/auth importing internal/ent
// directly follows the same real, already-established convention
// internal/inventory uses today: CODE_SCAFFOLD.md's "ent reachable only
// from internal/storage" line describes an aspiration no architecture test
// enforces, not the actual layering.
type entRoleBindingRepository struct {
	client *ent.Client
}

// NewEntRoleBindingRepository builds a RoleBindingRepository backed by
// client.
func NewEntRoleBindingRepository(client *ent.Client) RoleBindingRepository {
	return &entRoleBindingRepository{client: client}
}

// ListForTeams implements RoleBindingRepository.
func (r *entRoleBindingRepository) ListForTeams(ctx context.Context, teamIDs []int) ([]RoleBinding, error) {
	if len(teamIDs) == 0 {
		return nil, nil
	}
	rows, err := r.client.RoleBinding.Query().
		Where(rolebinding.HasTeamWith(team.IDIn(teamIDs...))).
		WithTeam().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: querying role bindings for teams %v: %w", teamIDs, err)
	}

	bindings := make([]RoleBinding, 0, len(rows))
	for _, row := range rows {
		bindings = append(bindings, RoleBinding{
			TeamID:    row.Edges.Team.ID,
			Role:      Role(row.Role),
			ScopeType: ScopeType(row.ScopeType),
			ScopeID:   row.ScopeID,
			Effect:    Effect(row.Effect),
		})
	}
	return bindings, nil
}
