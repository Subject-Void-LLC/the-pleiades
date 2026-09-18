// Making a checked scratch database the live one: migrating it, comparing
// its schema, recording what the restore changed, handing it over, and the
// swap.
package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entjob "github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
)

// restoreActor is who the activity trail records a restore as.
const restoreActor = "controller-restore"

// inFlightReason is what a job the backup caught mid-run is failed with.
const inFlightReason = "restored from a backup taken while this job was running: what it did after the backup is not recorded, so check its devices before running it again"

// prepare migrates the scratch database as the role, compares its schema
// with a fresh one, fails the jobs the backup caught running and ends every
// session, records the restore, and hands the database to the DB_DSN login.
func (s *scratch) prepare(archive string) (failed, ended int, err error) {
	client, err := ent.OpenDatabase(s.ctx, ent.Config{DSN: s.login(s.db).dsn()})
	if err != nil {
		return 0, 0, fmt.Errorf("backup: nothing was changed: the backup could not be brought up to this version's schema. A backup from a newer version is restored with that version or a later one: %w", err)
	}
	defer func() { _ = client.Close() }()

	if err := s.compare(); err != nil {
		return 0, 0, err
	}

	// A job the backup caught mid-run would otherwise be picked up again,
	// and its devices changed a second time. Its outcome after the backup
	// is unknown, which is what failing it with this reason says.
	failed, err = client.Job.Update().
		Where(entjob.StateIn(entjob.StatePending, entjob.StateFanningOut, entjob.StateRunning)).
		SetState(entjob.StateFailed).SetFailureReason(inFlightReason).AddFence(1).
		Save(s.ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("backup: failing the jobs the backup caught running: %w", err)
	}
	// A session in the backup may have been signed out since, or belong to
	// an account removed since. Ending them all costs one sign-in each.
	ended, err = client.Session.Delete().Exec(s.ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("backup: ending the backup's sessions: %w", err)
	}
	if err := activity.NewEntStore(client).Record(s.ctx, activity.Entry{
		Actor: restoreActor, Action: activity.ActionRestored, ObjectKind: activity.KindDatabase,
		ObjectID: 1, ObjectName: s.admin.database + " from " + printable(archive),
	}); err != nil {
		return 0, 0, fmt.Errorf("backup: recording the restore in the activity trail: %w", err)
	}
	_ = client.Close()
	return failed, ended, s.handOver()
}

// compare refuses unless the scratch database's schema is exactly the one
// this version's migrations make on an empty database.
func (s *scratch) compare() error {
	if _, err := s.maint.ExecContext(s.ctx, "CREATE DATABASE "+quoteIdent(s.reference)); err != nil {
		return fmt.Errorf("backup: creating the reference database: %w", err)
	}
	fresh, err := ent.OpenDatabase(s.ctx, ent.Config{DSN: s.admin.on(s.reference, s.admin.user, s.admin.password).dsn()})
	if err != nil {
		return fmt.Errorf("backup: migrating the reference database: %w", err)
	}
	_ = fresh.Close()

	want, err := shapeOf(s.ctx, s.admin.on(s.reference, s.admin.user, s.admin.password))
	if err != nil {
		return err
	}
	got, err := shapeOf(s.ctx, s.login(s.db))
	if err != nil {
		return err
	}
	extra, missing := shapeDifference(want, got)
	if len(extra) == 0 && len(missing) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("backup: nothing was changed: the backup's schema is not the one this version makes, so it was not restored.")
	for i, line := range extra {
		if i == 5 {
			fmt.Fprintf(&b, "\n  and %d more it has", len(extra)-5)
			break
		}
		b.WriteString("\n  it has: " + line)
	}
	for i, line := range missing {
		if i == 5 {
			fmt.Fprintf(&b, "\n  and %d more it lacks", len(missing)-5)
			break
		}
		b.WriteString("\n  it lacks: " + line)
	}
	return errors.New(b.String())
}

// shapeOf opens t with search_path pinned to the catalogs, so every name in
// the shape queries resolves to PostgreSQL's own objects, and reads its
// shape.
func shapeOf(ctx context.Context, t target) ([]string, error) {
	t.params = cloneValues(t.params)
	t.params.Set("search_path", "pg_catalog")
	db, err := sql.Open("postgres", t.dsn())
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return schemaShape(ctx, db)
}

// handOver gives everything the role owns to the DB_DSN login, and drops
// the role.
func (s *scratch) handOver() error {
	onScratch, err := sql.Open("postgres", s.admin.on(s.db, s.admin.user, s.admin.password).dsn())
	if err != nil {
		return err
	}
	_, err = onScratch.ExecContext(s.ctx, "REASSIGN OWNED BY "+quoteIdent(s.role)+" TO "+quoteIdent(s.admin.user)+"; DROP OWNED BY "+quoteIdent(s.role))
	_ = onScratch.Close()
	if err != nil {
		return fmt.Errorf("backup: handing the scratch database over: %w", err)
	}
	for _, stmt := range []string{
		"ALTER DATABASE " + quoteIdent(s.db) + " OWNER TO " + quoteIdent(s.admin.user),
		"DROP ROLE " + quoteIdent(s.role),
	} {
		if _, err := s.maint.ExecContext(s.ctx, stmt); err != nil {
			return fmt.Errorf("backup: handing the scratch database over: %w", err)
		}
	}
	return nil
}

// swap makes the scratch database the live one, in one transaction, and
// then drops the one it replaced, which was backed up first.
func (s *scratch) swap() error {
	if err := s.refuseIfInUse(); err != nil {
		return err
	}
	tx, err := s.maint.BeginTx(s.ctx, nil)
	if err != nil {
		return fmt.Errorf("backup: nothing was changed: %w", err)
	}
	for _, stmt := range []string{
		"ALTER DATABASE " + quoteIdent(s.admin.database) + " RENAME TO " + quoteIdent(s.replaced),
		"ALTER DATABASE " + quoteIdent(s.db) + " RENAME TO " + quoteIdent(s.admin.database),
	} {
		if _, err := tx.ExecContext(s.ctx, stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("backup: nothing was changed: replacing the database failed: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("backup: nothing was changed: replacing the database failed: %w", err)
	}
	// The replaced database, which was backed up before this, is dropped
	// by close.
	return nil
}
