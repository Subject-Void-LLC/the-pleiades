// This file tests what no other test here did: applying a migration to a
// database that already holds rows.
//
// Every other test in this package migrates an empty database, which is
// the one case where a table rebuild cannot hurt anybody, and that is why
// FAILURE_PATTERNS.md #266 survived thirty migrations. Each test below
// builds a database at the version before the migration under test, puts
// real rows in it through real SQL, and only then applies that migration,
// which is the shape an upgrade actually takes in a deployment.
//
// These tests live in the package rather than beside apply_test.go's
// external ones because they need applyPending to stop at a chosen
// version. They deliberately do not import internal/ent, which imports
// this package.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// openUpgradeDB opens a real on-disk SQLite database with exactly the
// options production opens one with, foreign-key enforcement included
// (internal/ent/open_sqlite.go's "_fk=1"). Enforcement is the whole
// subject here, so a test database without it would pass vacuously.
//
// The pool is capped at one connection so that a pragma read after Apply
// returns necessarily comes from the same connection the migrations ran
// on, which is what makes the restore assertion meaningful.
func openUpgradeDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s/upgrade-test.sqlite?_fk=1&_journal_mode=WAL&_busy_timeout=5000", t.TempDir())
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("opening sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// applyThrough migrates db up to and including the named migration file.
func applyThrough(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	ctx := context.Background()
	src := migrationSources["sqlite3"]

	names, err := migrationNames(src)
	if err != nil {
		t.Fatalf("listing migrations: %v", err)
	}
	found := false
	for _, n := range names {
		if n == name {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no migration named %q; the set was renumbered and this test needs rewriting", name)
	}

	if err := ensureVersionTable(ctx, db); err != nil {
		t.Fatalf("creating the version table: %v", err)
	}
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		t.Fatalf("reading applied versions: %v", err)
	}
	if err := applyPending(ctx, db, src, names, applied, name); err != nil {
		t.Fatalf("applying through %s: %v", name, err)
	}
}

// countRows answers how many rows a table holds, failing the test rather
// than returning an error, since every caller would only do the same.
func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	// The table name is a constant in every caller; SQLite cannot
	// parameterize an identifier.
	if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

// exec runs one statement, failing the test on error.
func exec(t *testing.T, db *sql.DB, query string, args ...any) sql.Result {
	t.Helper()
	res, err := db.ExecContext(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
	return res
}

// seedTemplateAt0021 writes the one organization, inventory and template
// a launch template needs at version 0021, and returns the template's id.
func seedTemplateAt0021(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	now := time.Now().UTC()

	org := exec(t, db, `INSERT INTO organizations (created_at, updated_at, name) VALUES (?, ?, 'upgrade-org')`, now, now)
	orgID, err := org.LastInsertId()
	if err != nil {
		t.Fatalf("organization id: %v", err)
	}
	inv := exec(t, db, `INSERT INTO inventories (created_at, updated_at, name, organization_inventories) VALUES (?, ?, 'upgrade-inventory', ?)`, now, now, orgID)
	invID, err := inv.LastInsertId()
	if err != nil {
		t.Fatalf("inventory id: %v", err)
	}
	tmpl := exec(t, db, `INSERT INTO templates
		(created_at, updated_at, name, kind, definition, inventory_templates, organization_templates)
		VALUES (?, ?, 'upgrade-template', 'runbook', 'upgrade.yaml', ?, ?)`, now, now, invID, orgID)
	tmplID, err := tmpl.LastInsertId()
	if err != nil {
		t.Fatalf("template id: %v", err)
	}
	return tmplID
}

// TestApply_AnUpgradeKeepsTheRowsOfARebuiltTablesChildren proves the
// cascade half of #266: migration 0022 rebuilds `templates`, and a
// rebuild is a DROP TABLE, which with enforcement on performs an implicit
// DELETE FROM and takes every ON DELETE CASCADE child with it. A survey
// question and a saved launch configuration are exactly those children,
// and both are things a user authored and would never be told they lost.
func TestApply_AnUpgradeKeepsTheRowsOfARebuiltTablesChildren(t *testing.T) {
	db := openUpgradeDB(t)
	applyThrough(t, db, "0021_add_journal_entries.sql")

	now := time.Now().UTC()
	tmplID := seedTemplateAt0021(t, db)
	exec(t, db, `INSERT INTO survey_questions
		(created_at, updated_at, variable, label, question_type, template_survey_questions)
		VALUES (?, ?, 'target_host', 'Target host', 'text', ?)`, now, now, tmplID)
	exec(t, db, `INSERT INTO saved_launch_configs
		(created_at, updated_at, template_saved_configs) VALUES (?, ?, ?)`, now, now, tmplID)

	applyThrough(t, db, "0022_add_projects.sql")

	if got := countRows(t, db, "templates"); got != 1 {
		t.Errorf("templates after the upgrade = %d, want 1", got)
	}
	if got := countRows(t, db, "survey_questions"); got != 1 {
		t.Errorf("survey_questions after the upgrade = %d, want 1: the rebuild of templates deleted an authored survey question", got)
	}
	if got := countRows(t, db, "saved_launch_configs"); got != 1 {
		t.Errorf("saved_launch_configs after the upgrade = %d, want 1: the rebuild of templates deleted a saved launch configuration", got)
	}
}

// TestApply_AnUpgradeSucceedsForAScheduledTemplate proves the other half:
// `schedules` points at `templates` with NO ACTION, so with enforcement
// on the same rebuild does not silently delete, it fails, and the
// controller never starts.
func TestApply_AnUpgradeSucceedsForAScheduledTemplate(t *testing.T) {
	db := openUpgradeDB(t)
	applyThrough(t, db, "0021_add_journal_entries.sql")

	now := time.Now().UTC()
	tmplID := seedTemplateAt0021(t, db)
	var orgID int64
	if err := db.QueryRow(`SELECT id FROM organizations`).Scan(&orgID); err != nil {
		t.Fatalf("reading the organization back: %v", err)
	}
	exec(t, db, `INSERT INTO schedules
		(created_at, updated_at, schedule_id, name, rrule, dtstart, organization_schedules, template_schedules)
		VALUES (?, ?, 'sched-upgrade', 'nightly', 'FREQ=DAILY', ?, ?, ?)`, now, now, now, orgID, tmplID)

	applyThrough(t, db, "0022_add_projects.sql")

	if got := countRows(t, db, "schedules"); got != 1 {
		t.Errorf("schedules after the upgrade = %d, want 1", got)
	}
	var referenced int64
	if err := db.QueryRow(`SELECT template_schedules FROM schedules`).Scan(&referenced); err != nil {
		t.Fatalf("reading the schedule's template back: %v", err)
	}
	if referenced != tmplID {
		t.Errorf("schedule points at template %d, want %d", referenced, tmplID)
	}
}

// TestApply_AnUpgradeSucceedsForAJobWithTasks is the same failure on the
// newest migration in the tree: 0030 rebuilds `jobs`, and `job_tasks`
// points at it with NO ACTION. Any deployment that has ever run a job
// holds such a row, so this is the case that stops a controller starting
// after an upgrade.
func TestApply_AnUpgradeSucceedsForAJobWithTasks(t *testing.T) {
	db := openUpgradeDB(t)
	applyThrough(t, db, "0029_add_job_task_unchecked.sql")

	now := time.Now().UTC()
	job := exec(t, db, `INSERT INTO jobs
		(created_at, updated_at, job_id, runbook_id, group_name, actor)
		VALUES (?, ?, 'job-upgrade', 'upgrade.yaml', '', 'tester')`, now, now)
	jobID, err := job.LastInsertId()
	if err != nil {
		t.Fatalf("job id: %v", err)
	}
	exec(t, db, `INSERT INTO job_tasks
		(created_at, updated_at, device_id, device_name, outcome, job_tasks)
		VALUES (?, ?, 'device-1', 'web-01', 'ok', ?)`, now, now, jobID)

	applyThrough(t, db, "0030_add_job_external_checks.sql")

	if got := countRows(t, db, "jobs"); got != 1 {
		t.Errorf("jobs after the upgrade = %d, want 1", got)
	}
	if got := countRows(t, db, "job_tasks"); got != 1 {
		t.Errorf("job_tasks after the upgrade = %d, want 1", got)
	}
}

// TestApply_LeavesForeignKeyEnforcementOn proves the suspension is undone.
// A connection handed back to the pool with enforcement off would serve
// application queries that silently accept a broken reference, which is a
// worse bug than the one being fixed.
func TestApply_LeavesForeignKeyEnforcementOn(t *testing.T) {
	db := openUpgradeDB(t)
	if err := Apply(context.Background(), "sqlite3", db); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var enforced bool
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&enforced); err != nil {
		t.Fatalf("reading foreign_keys: %v", err)
	}
	if !enforced {
		t.Error("foreign keys are off after Apply; the pool holds a connection that accepts a broken reference")
	}

	// And prove it by behavior, not only by the pragma: the schema refuses
	// a child row naming a parent that does not exist.
	_, err := db.Exec(`INSERT INTO job_tasks (created_at, updated_at, device_id, device_name, outcome, job_tasks)
		VALUES (?, ?, 'device-1', 'web-01', 'ok', 999999)`, time.Now().UTC(), time.Now().UTC())
	if err == nil {
		t.Error("inserting a job task for a job that does not exist succeeded; enforcement is off")
	}
}

// TestApplyOne_RefusesAMigrationThatLeavesARowReferencingNothing proves
// the price of suspending enforcement is paid back before commit. A
// migration that deletes a parent row while enforcement is off would
// otherwise commit a database no later insert could have produced.
func TestApplyOne_RefusesAMigrationThatLeavesARowReferencingNothing(t *testing.T) {
	db := openUpgradeDB(t)
	ctx := context.Background()
	if err := ensureVersionTable(ctx, db); err != nil {
		t.Fatalf("creating the version table: %v", err)
	}
	exec(t, db, `CREATE TABLE parents (id integer NOT NULL PRIMARY KEY AUTOINCREMENT)`)
	exec(t, db, `CREATE TABLE children (id integer NOT NULL PRIMARY KEY AUTOINCREMENT,
		parent_id integer NOT NULL,
		CONSTRAINT fk_parent FOREIGN KEY (parent_id) REFERENCES parents (id) ON DELETE NO ACTION)`)
	exec(t, db, `INSERT INTO parents (id) VALUES (1)`)
	exec(t, db, `INSERT INTO children (parent_id) VALUES (1)`)

	src := migrationSources["sqlite3"]
	err := applyOne(ctx, db, src, "0999_breaks_a_reference.sql", "DELETE FROM parents;")
	if err == nil {
		t.Fatal("a migration that orphaned a row was accepted")
	}
	if !strings.Contains(err.Error(), "referencing nothing") {
		t.Errorf("error = %q, want it to name the orphaned rows", err)
	}

	// Refused means rolled back and unrecorded, not partly applied.
	if got := countRows(t, db, "parents"); got != 1 {
		t.Errorf("parents = %d, want 1: the refused migration was not rolled back", got)
	}
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		t.Fatalf("reading applied versions: %v", err)
	}
	if applied["0999_breaks_a_reference.sql"] {
		t.Error("the refused migration was recorded as applied")
	}
}

// TestApply_TheSyncRunRebuildKeepsTheHistory covers the migration this
// session added, on the same terms: 0031 rebuilds sync_runs to make
// finished_at nullable, add the actor and cascade the project's delete, and a
// deployment applying it holds every past attempt.
//
// The rows it copies have no actor, which is the honest answer for an attempt
// recorded before anybody was asked: empty rather than a backfilled name.
func TestApply_TheSyncRunRebuildKeepsTheHistory(t *testing.T) {
	db := openUpgradeDB(t)
	applyThrough(t, db, "0030_add_job_external_checks.sql")

	now := time.Now().UTC()
	org := exec(t, db, `INSERT INTO organizations (created_at, updated_at, name) VALUES (?, ?, 'upgrade-org')`, now, now)
	orgID, err := org.LastInsertId()
	if err != nil {
		t.Fatalf("organization id: %v", err)
	}
	proj := exec(t, db, `INSERT INTO projects (created_at, updated_at, name, organization_projects)
		VALUES (?, ?, 'automation', ?)`, now, now, orgID)
	projID, err := proj.LastInsertId()
	if err != nil {
		t.Fatalf("project id: %v", err)
	}
	exec(t, db, `INSERT INTO sync_runs
		(created_at, updated_at, status, revision, error, started_at, finished_at, project_sync_runs)
		VALUES (?, ?, 'succeeded', 'abc123', '', ?, ?, ?)`, now, now, now, now.Add(time.Second), projID)

	applyThrough(t, db, "0031_add_sync_run_origin.sql")

	var status, revision string
	var actor sql.NullString
	if err := db.QueryRow(`SELECT status, revision, actor FROM sync_runs`).Scan(&status, &revision, &actor); err != nil {
		t.Fatalf("reading the migrated history row: %v", err)
	}
	if status != "succeeded" || revision != "abc123" {
		t.Errorf("migrated row = %q/%q, want the recorded attempt", status, revision)
	}
	if actor.Valid && actor.String != "" {
		t.Errorf("actor = %q, want it empty for an attempt recorded before attribution", actor.String)
	}

	// And the new cascade is real: deleting the project takes the history.
	exec(t, db, `DELETE FROM projects WHERE id = ?`, projID)
	if got := countRows(t, db, "sync_runs"); got != 0 {
		t.Errorf("%d history rows outlived their project, so the cascade did not land", got)
	}
}

// TestApply_TheLaunchableBackfillRepointsEverySchedule is the migration this
// session's main change needs, and the one with a real backfill: every
// template and project gains the launchable row that now stands for it, and
// every schedule is repointed from the template it named at that row.
//
// It is the test that would catch the whole change silently breaking an
// existing deployment, which is the case no unit test over the stores can
// reach: those run against a database built by Schema.Create, where there is
// nothing to carry across.
func TestApply_TheLaunchableBackfillRepointsEverySchedule(t *testing.T) {
	db := openUpgradeDB(t)
	applyThrough(t, db, "0031_add_sync_run_origin.sql")

	now := time.Now().UTC()
	tmplID := seedTemplateAt0021(t, db)
	var orgID int64
	if err := db.QueryRow(`SELECT id FROM organizations`).Scan(&orgID); err != nil {
		t.Fatalf("reading the organization back: %v", err)
	}

	// A second template, so the join has to pick the right row rather than
	// the only row.
	other := exec(t, db, `INSERT INTO templates
		(created_at, updated_at, name, kind, definition, inventory_templates, organization_templates)
		SELECT ?, ?, 'other-template', 'runbook', 'other.yaml', inventory_templates, organization_templates
		FROM templates WHERE id = ?`, now, now, tmplID)
	otherID, err := other.LastInsertId()
	if err != nil {
		t.Fatalf("second template id: %v", err)
	}

	proj := exec(t, db, `INSERT INTO projects (created_at, updated_at, name, organization_projects)
		VALUES (?, ?, 'automation', ?)`, now, now, orgID)
	projID, err := proj.LastInsertId()
	if err != nil {
		t.Fatalf("project id: %v", err)
	}

	// A saved configuration on the schedule, so the repoint is proven to keep
	// the rest of the row rather than only the foreign key.
	cfg := exec(t, db, `INSERT INTO saved_launch_configs (created_at, updated_at, template_saved_configs)
		VALUES (?, ?, ?)`, now, now, otherID)
	cfgID, err := cfg.LastInsertId()
	if err != nil {
		t.Fatalf("saved config id: %v", err)
	}
	sched := exec(t, db, `INSERT INTO schedules
		(created_at, updated_at, schedule_id, name, rrule, dtstart, organization_schedules, template_schedules, schedule_saved_config)
		VALUES (?, ?, 'sched-1', 'nightly', 'FREQ=DAILY', ?, ?, ?, ?)`,
		now, now, now, orgID, otherID, cfgID)
	schedID, err := sched.LastInsertId()
	if err != nil {
		t.Fatalf("schedule id: %v", err)
	}
	exec(t, db, `INSERT INTO schedule_occurrences
		(created_at, updated_at, occurrence_at, outcome, reason, suppressed_count, job_id, schedule_occurrences)
		VALUES (?, ?, ?, 'fired', '', 0, 'job-abc', ?)`, now, now, now, schedID)
	exec(t, db, `INSERT INTO schedule_occurrences
		(created_at, updated_at, occurrence_at, outcome, reason, suppressed_count, job_id, schedule_occurrences)
		VALUES (?, ?, ?, 'skipped', 'missed_window', 0, '', ?)`, now, now, now.Add(time.Hour), schedID)

	applyThrough(t, db, "0032_add_launchables.sql")

	// One launchable row per target, of the right type, carrying the target's
	// own name and organization.
	if got := countRows(t, db, "launchables"); got != 3 {
		t.Errorf("launchables after the backfill = %d, want one per template and project", got)
	}
	var gotType, gotName string
	var gotOrg int64
	if err := db.QueryRow(`SELECT type, name, organization_launchables FROM launchables WHERE template_launchable = ?`, tmplID).
		Scan(&gotType, &gotName, &gotOrg); err != nil {
		t.Fatalf("reading the template's launchable: %v", err)
	}
	if gotType != "job_template" || gotName != "upgrade-template" || gotOrg != orgID {
		t.Errorf("template launchable = %q/%q/org %d, want a job_template carrying the template's own name and tenant", gotType, gotName, gotOrg)
	}
	if err := db.QueryRow(`SELECT type, name FROM launchables WHERE project_launchable = ?`, projID).
		Scan(&gotType, &gotName); err != nil {
		t.Fatalf("reading the project's launchable: %v", err)
	}
	if gotType != "project" || gotName != "automation" {
		t.Errorf("project launchable = %q/%q, want a project carrying the project's own name", gotType, gotName)
	}

	// The schedule points at the launchable standing for ITS template, and
	// keeps everything else it had.
	var pointsAt, keptConfig int64
	var keptName string
	if err := db.QueryRow(`SELECT launchable_schedules, name, schedule_saved_config FROM schedules WHERE id = ?`, schedID).
		Scan(&pointsAt, &keptName, &keptConfig); err != nil {
		t.Fatalf("reading the migrated schedule: %v", err)
	}
	var wantPointsAt int64
	if err := db.QueryRow(`SELECT id FROM launchables WHERE template_launchable = ?`, otherID).Scan(&wantPointsAt); err != nil {
		t.Fatalf("reading the expected launchable: %v", err)
	}
	if pointsAt != wantPointsAt {
		t.Errorf("schedule points at launchable %d, want %d (the row for its own template)", pointsAt, wantPointsAt)
	}
	if keptName != "nightly" || keptConfig != cfgID {
		t.Errorf("migrated schedule = %q with config %d, want the rest of the row kept", keptName, keptConfig)
	}

	// The old column is gone, so nothing can still read the reference that no
	// longer decides anything.
	if _, err := db.Exec(`SELECT template_schedules FROM schedules`); err == nil {
		t.Error("schedules still has a template_schedules column")
	}

	// A fired occurrence is classified as having started a job; a skipped one
	// names no run and so says nothing.
	var firedType, skippedType sql.NullString
	if err := db.QueryRow(`SELECT unified_job_type FROM schedule_occurrences WHERE job_id = 'job-abc'`).Scan(&firedType); err != nil {
		t.Fatalf("reading the fired occurrence: %v", err)
	}
	if firedType.String != "job" {
		t.Errorf("fired occurrence unified_job_type = %q, want \"job\"", firedType.String)
	}
	if err := db.QueryRow(`SELECT unified_job_type FROM schedule_occurrences WHERE outcome = 'skipped'`).Scan(&skippedType); err != nil {
		t.Fatalf("reading the skipped occurrence: %v", err)
	}
	if skippedType.Valid && skippedType.String != "" {
		t.Errorf("skipped occurrence unified_job_type = %q, want it empty: nothing ran", skippedType.String)
	}

	// And the integrity the whole design rests on: the scheduled template
	// cannot be deleted, while an unscheduled one can, taking its launchable
	// row with it.
	if _, err := db.Exec(`DELETE FROM templates WHERE id = ?`, otherID); err == nil {
		t.Error("deleting a scheduled template succeeded, so a schedule can be left pointing at nothing")
	}
	if _, err := db.Exec(`DELETE FROM templates WHERE id = ?`, tmplID); err != nil {
		t.Errorf("deleting an unscheduled template = %v, want it to succeed", err)
	}
	if got := countRows(t, db, "launchables"); got != 2 {
		t.Errorf("launchables after deleting one template = %d, want 2: the row did not cascade", got)
	}
}
