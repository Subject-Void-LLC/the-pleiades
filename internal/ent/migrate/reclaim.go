// Package migrate: running what a migration owes once it has committed
// (sqliteAfterCommit), at every start until it has run.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// reclaimOwed runs the after-commit statements of every migration history
// records that are still owed (sqliteAfterCommit says why they are recorded),
// in order, on one connection of their own, and records each as done once its
// statements all ran. What it ran is added to out.Reclaimed and what failed to
// out.NotReclaimed, with the failure.
//
// It is best effort by design, and says so through the outcome rather than by
// failing the start: the migration is already recorded, and refusing to start
// over a reclamation would turn a database that is correct into one that is
// down. What failed is still owed, so the next start tries it again, as does
// any starter that finds it owed, a crash between a migration's commit and its
// reclamation included. Two starters that both find it owed both run it; the
// second waits for the first's lock and then has little left to do.
func reclaimOwed(ctx context.Context, db *sql.DB, src migrationSource, history []appliedRow, out *Outcome) {
	owed := reclaimable(src, history)
	if len(owed) == 0 || src.reclaimedMark == "" {
		return
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		out.NotReclaimed = append(out.NotReclaimed, owed[0].name+": taking a connection: "+err.Error())
		return
	}
	defer func() { _ = conn.Close() }()
	var mark int
	if err := conn.QueryRowContext(ctx, src.reclaimedMark).Scan(&mark); err != nil {
		out.NotReclaimed = append(out.NotReclaimed, owed[0].name+": reading what was reclaimed: "+err.Error())
		return
	}
	for _, m := range owed {
		if m.number <= mark {
			// Done by an earlier start, or covered by a later migration's run.
			continue
		}
		if err := reclaim(ctx, conn, src, m.name); err != nil {
			out.NotReclaimed = append(out.NotReclaimed, m.name+": "+err.Error())
			return
		}
		// A number from versionPattern's four digits, never input, so it is
		// written into the statement, which takes no parameter.
		if _, err := conn.ExecContext(ctx, fmt.Sprintf(src.markReclaimed, m.number)); err != nil {
			out.NotReclaimed = append(out.NotReclaimed, m.name+": recording it as reclaimed: "+err.Error())
			return
		}
		mark = m.number
		out.Reclaimed = append(out.Reclaimed, m.name)
	}
}

// numbered is a migration's file name and the number it starts with.
type numbered struct {
	name   string
	number int
}

// reclaimable is every migration history records that has after-commit
// statements, oldest first.
func reclaimable(src migrationSource, history []appliedRow) []numbered {
	var out []numbered
	for _, row := range history {
		if len(src.afterCommit[row.version]) == 0 {
			continue
		}
		n, err := versionNumber(row.version)
		if err != nil {
			// The gate already refused a history holding such a name.
			continue
		}
		out = append(out, numbered{name: row.version, number: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].number < out[j].number })
	return out
}

// reclaim runs the named migration's after-commit statements on conn, after
// the migration has committed.
func reclaim(ctx context.Context, conn *sql.Conn, src migrationSource, name string) error {
	for _, statement := range src.afterCommit[name] {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("%s: %w", statement, err)
		}
	}
	return nil
}
