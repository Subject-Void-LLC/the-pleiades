// This file is `controller migrate --plan`: what this build would do to the
// database it is pointed at, and which controllers the database has seen,
// without changing anything.
//
// It exists for the moment before an upgrade. An operator runs the NEW build's
// plan against the live database and learns whether it would apply migrations
// (and so needs a backup first), whether any of them is a contract that older
// controllers cannot survive, and whether every controller still running has
// reached the build the upgrade needs. Compose's make up runs it and takes a
// backup on its answer; that is why its answer is also its exit code.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/migrate"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// migrateCommand is the subcommand this file implements.
const migrateCommand = "migrate"

// Exit codes of migrate --plan. Each verdict has its own, so a script can act
// on the answer without parsing text.
const (
	// planExitNothingToDo: the database is current, or new.
	planExitNothingToDo = 0

	// planExitRefused: this build must not use the database, or it could
	// not be read.
	planExitRefused = 1

	// planExitUsage: the command line was wrong.
	planExitUsage = 2

	// planExitPending: this build would upgrade a database that holds data.
	planExitPending = 3

	// planExitNewer: a newer build migrated the database, and this build
	// can still serve it.
	planExitNewer = 4
)

// planReportVersion is the JSON view's schema_version.
const planReportVersion = 1

// isMigrateCommand reports whether args ask for this file's subcommand. It is
// checked before the admin guard, which treats any non-flag word as a
// subcommand and would open (and migrate) the database first.
func isMigrateCommand(args []string) bool {
	return len(args) > 0 && args[0] == migrateCommand
}

// planReport is the one model both views print, so the human view and the
// JSON view cannot disagree.
type planReport struct {
	SchemaVersion int          `json:"schema_version"`
	BuildVersion  string       `json:"build_version"`
	Plan          migrate.Plan `json:"plan"`

	// Controllers is every controller heartbeat the database holds. It is
	// null when the database has no heartbeat table (nothing could be
	// checked) and empty when it has one with no rows.
	Controllers []controllerView `json:"controllers"`
}

// controllerView is one controller heartbeat as the plan reports it.
type controllerView struct {
	InstanceID       string `json:"instance_id"`
	Version          string `json:"version"`
	MigrationHead    string `json:"migration_head"`
	Host             string `json:"host"`
	SecondsSinceSeen int64  `json:"seconds_since_seen"`
	Live             bool   `json:"live"`
}

// runMigrate runs `controller migrate --plan [--json]` against the database
// DB_DSN (or DB_PATH) names, and returns the plan's exit code.
func runMigrate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("controller "+migrateCommand, flag.ContinueOnError)
	fs.SetOutput(stderr)
	plan := fs.Bool("plan", false, "report what this build would do to the database, changing nothing")
	asJSON := fs.Bool("json", false, "print the plan as JSON instead of text")
	if err := fs.Parse(args[1:]); err != nil {
		return planExitUsage
	}
	if !*plan || fs.NArg() != 0 {
		// There is deliberately no form that migrates: the server migrates
		// when it starts, and nothing else should.
		fmt.Fprintln(stderr, "usage: controller migrate --plan [--json]")
		return planExitUsage
	}

	dsn, err := resolveDatabaseDSN()
	if err != nil {
		fmt.Fprintf(stderr, "controller migrate: %v\n", err)
		return planExitRefused
	}
	report, err := buildPlanReport(context.Background(), dsn)
	if err != nil {
		fmt.Fprintf(stderr, "controller migrate: %s\n", termsafe.Escape(err.Error()))
		return planExitRefused
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(stderr, "controller migrate: writing the plan: %v\n", err)
			return planExitRefused
		}
	} else {
		writePlanText(stdout, report)
	}
	return planExitCode(report.Plan)
}

// buildPlanReport reads the database without migrating it.
func buildPlanReport(ctx context.Context, dsn string) (planReport, error) {
	report := planReport{SchemaVersion: planReportVersion, BuildVersion: buildinfo.Version()}

	db, err := ent.OpenExisting(ctx, dsn)
	if errors.Is(err, ent.ErrNoDatabase) {
		// Only a SQLite file can be missing; a server's database either
		// answers or does not.
		report.Plan, err = migrate.PlanForNewDatabase("sqlite3")
		return report, err
	}
	if err != nil {
		return planReport{}, err
	}
	defer func() { _ = db.Close() }()

	if report.Plan, err = db.SchemaPlan(ctx); err != nil {
		return planReport{}, err
	}
	instances, err := db.Instances(ctx)
	if err != nil {
		return planReport{}, err
	}
	if instances != nil {
		report.Controllers = make([]controllerView, 0, len(instances))
	}
	for _, i := range instances {
		report.Controllers = append(report.Controllers, controllerView{
			InstanceID: i.InstanceID, Version: i.Version, MigrationHead: i.MigrationHead, Host: i.Host,
			SecondsSinceSeen: i.SecondsSinceSeen,
			Live:             i.SecondsSinceSeen <= int64(liveWithin.Seconds()),
		})
	}
	return report, nil
}

// planExitCode maps a verdict to its exit code.
func planExitCode(p migrate.Plan) int {
	switch p.Verdict {
	case migrate.VerdictFresh, migrate.VerdictCurrent:
		return planExitNothingToDo
	case migrate.VerdictPending:
		return planExitPending
	case migrate.VerdictNewerWithinWindow:
		return planExitNewer
	default:
		return planExitRefused
	}
}

// writePlanText prints the human view. Everything read from the database goes
// through termsafe, since a tampered row could otherwise put terminal escapes
// in front of the person deciding whether to upgrade.
func writePlanText(w io.Writer, r planReport) {
	p := r.Plan
	safe := termsafe.Escape
	fmt.Fprintf(w, "This build:  %s, migrations through %s (%s)\n", safe(r.BuildVersion), p.BuildHead, p.Dialect)
	if p.Recorded == 0 {
		fmt.Fprintln(w, "Database:    new, no migrations recorded")
	} else {
		fmt.Fprintf(w, "Database:    migrations through %s (%d recorded)\n", safe(p.DatabaseHead), p.Recorded)
	}

	switch p.Verdict {
	case migrate.VerdictFresh:
		fmt.Fprintf(w, "Verdict:     fresh. Starting this build creates the schema (%d migrations).\n", len(p.Pending))
	case migrate.VerdictCurrent:
		fmt.Fprintln(w, "Verdict:     current. Nothing to apply.")
	case migrate.VerdictPending:
		fmt.Fprintf(w, "Verdict:     pending. Starting this build upgrades the database. Take a backup first.\n")
	case migrate.VerdictNewerWithinWindow:
		fmt.Fprintf(w, "Verdict:     newer. A newer build migrated this database (%s); this build can still serve it.\n", safe(strings.Join(p.Newer, ", ")))
	default:
		fmt.Fprintf(w, "Verdict:     refused. %s\n", safe(p.Refusal))
	}
	if p.Verdict == migrate.VerdictPending {
		for _, m := range p.Pending {
			line := fmt.Sprintf("  %-45s %s", m.Name, m.Kind)
			if m.Kind == "contract" {
				line += ": " + m.Reason + " (needs every controller at " + m.Floor + " or later)"
			}
			fmt.Fprintln(w, line)
		}
	}

	switch {
	case r.Controllers == nil:
		fmt.Fprintln(w, "Controllers: no heartbeat table yet; the database has not been migrated that far.")
	case len(r.Controllers) == 0:
		fmt.Fprintln(w, "Controllers: none has a heartbeat recorded.")
	default:
		fmt.Fprintln(w, "Controllers:")
		for _, c := range r.Controllers {
			state := "live"
			if !c.Live {
				state = "not seen recently"
			}
			fmt.Fprintf(w, "  %s  %s  through %s  on %s  last seen %ds ago (%s)\n",
				safe(c.InstanceID), safe(c.Version), safe(c.MigrationHead), safe(c.Host), c.SecondsSinceSeen, state)
		}
	}
}

// runMigrateMain is the route's entry point, bound to the process's streams.
func runMigrateMain(args []string) int {
	return runMigrate(args, os.Stdout, os.Stderr)
}
