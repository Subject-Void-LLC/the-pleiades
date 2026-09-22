// This file answers what an upgrade would do to a database, and whether a
// running build may keep serving it, without changing anything.
//
// Two callers need exactly that and must never migrate: the operator asking
// before an upgrade (controller migrate --plan, and compose's make up, which
// takes a backup first when the answer is "pending"), and a running controller
// checking on every heartbeat that the database has not moved past what it
// can serve.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// Verdict is Inspect's one-word answer.
type Verdict string

const (
	// VerdictFresh means nothing has migrated this database yet.
	VerdictFresh Verdict = "fresh"

	// VerdictCurrent means the database is exactly where this build leaves
	// it.
	VerdictCurrent Verdict = "current"

	// VerdictPending means this build would apply migrations to a database
	// that already holds data: an upgrade, which is when a backup matters.
	VerdictPending Verdict = "pending"

	// VerdictNewerWithinWindow means a newer build migrated the database
	// further, and every migration this build does not know left a schema it
	// can still serve.
	VerdictNewerWithinWindow Verdict = "newer_within_window"

	// VerdictRefused means this build must not use the database; Refusal
	// says why.
	VerdictRefused Verdict = "refused"
)

// PlannedMigration is one migration this build would apply.
type PlannedMigration struct {
	// Name is the migration's file name.
	Name string `json:"name"`

	// Kind is "expand" (the build before it keeps working) or "contract"
	// (it does not; see compat.go).
	Kind string `json:"kind"`

	// Floor is the oldest migration a build must know to serve the database
	// once this one is applied.
	Floor string `json:"floor"`

	// Reason says what a contract breaks. Empty for an expand.
	Reason string `json:"reason,omitempty"`
}

// Plan is what Inspect found.
type Plan struct {
	// Dialect is the driver name the database was inspected with.
	Dialect string `json:"dialect"`

	// Verdict is the one-word answer.
	Verdict Verdict `json:"verdict"`

	// BuildHead is the newest migration this build knows.
	BuildHead string `json:"build_head"`

	// DatabaseHead is the newest migration the database records, or empty
	// when it records none.
	DatabaseHead string `json:"database_head"`

	// Recorded is how many migrations the database records.
	Recorded int `json:"recorded"`

	// Pending is what this build would apply, in order.
	Pending []PlannedMigration `json:"pending"`

	// Newer is every recorded migration this build does not know, when the
	// database is within this build's window.
	Newer []string `json:"newer"`

	// Unknown is every recorded migration this build does not know, whatever
	// the verdict: the same list as Newer inside the window, and the reason
	// for the refusal outside it.
	Unknown []string `json:"unknown"`

	// Floor is the oldest migration a build must know to serve the database
	// as it stands, when a newer build migrated it.
	Floor string `json:"floor,omitempty"`

	// Refusal is why this build must not use the database, when the verdict
	// is refused.
	Refusal string `json:"refusal,omitempty"`
}

// Serves reports whether a build already running against the database may
// keep serving it.
//
// Pending is not serving: a running build has already applied everything it
// knows, so a database that now lacks some of it was replaced underneath the
// build (a restore of an older backup, say), and the build must restart and
// migrate before it trusts it again.
func (p Plan) Serves() bool {
	return p.Verdict == VerdictCurrent || p.Verdict == VerdictNewerWithinWindow
}

// ErrUpgradeRequired is returned by ApplyWith with RefuseToUpgrade set, when
// the database holds data and this build has migrations to apply to it.
var ErrUpgradeRequired = errors.New("migrate: this database needs migrations this build brings, and this command does not upgrade a database")

// Inspect reads db's migration history and says what this build would do with
// it. It never writes: no table is created, no column added, nothing applied.
func Inspect(ctx context.Context, dialectName string, db *sql.DB) (Plan, error) {
	src, ok := migrationSources[dialectName]
	if !ok {
		return Plan{}, fmt.Errorf("migrate: no embedded migrations for dialect %q", dialectName)
	}
	names, err := migrationNames(src)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Dialect: dialectName, BuildHead: headOf(names), Pending: []PlannedMigration{}, Newer: []string{}, Unknown: []string{}}

	exists, err := versionTableExists(ctx, db, src)
	if err != nil {
		return Plan{}, err
	}
	var history []appliedRow
	if exists {
		if err := verifyVersionKey(ctx, db, src); err != nil {
			if !errors.Is(err, errVersionKey) {
				return Plan{}, err
			}
			plan.Verdict, plan.Refusal = VerdictRefused, err.Error()
			return plan, nil
		}
		if history, err = readHistory(ctx, db, src); err != nil {
			return Plan{}, err
		}
	}
	plan.Recorded = len(history)
	known := make(map[string]bool, len(names))
	for _, name := range names {
		known[name] = true
	}
	for _, row := range history {
		if row.version > plan.DatabaseHead {
			plan.DatabaseHead = row.version
		}
		if !known[row.version] {
			plan.Unknown = append(plan.Unknown, row.version)
		}
	}
	sort.Strings(plan.Unknown)

	gate, err := checkGate(names, history, gateWithinWindow)
	if err != nil {
		plan.Verdict, plan.Refusal = VerdictRefused, err.Error()
		return plan, nil
	}
	for _, name := range gate.pending {
		plan.Pending = append(plan.Pending, planned(dialectName, names, name))
	}
	plan.Newer, plan.Floor = gate.newer, gate.floor
	if plan.Newer == nil {
		plan.Newer = []string{}
	}

	switch {
	case len(history) == 0:
		plan.Verdict = VerdictFresh
	case len(plan.Newer) > 0:
		plan.Verdict = VerdictNewerWithinWindow
	case len(plan.Pending) > 0:
		plan.Verdict = VerdictPending
	default:
		plan.Verdict = VerdictCurrent
	}
	return plan, nil
}

// planned describes one migration of this build for a Plan.
func planned(dialectName string, names []string, name string) PlannedMigration {
	p := PlannedMigration{Name: name, Kind: "expand", Floor: floorAfter(dialectName, names, name)}
	if c, ok := contracts[dialectName][name]; ok {
		p.Kind, p.Reason = "contract", c.reason
	}
	return p
}

// PlanForNewDatabase is the plan for a database that does not exist yet, such
// as a SQLite file nobody has created: every migration of this build pending,
// and nothing recorded. It answers without touching anything, because there is
// nothing to touch.
func PlanForNewDatabase(dialectName string) (Plan, error) {
	src, ok := migrationSources[dialectName]
	if !ok {
		return Plan{}, fmt.Errorf("migrate: no embedded migrations for dialect %q", dialectName)
	}
	names, err := migrationNames(src)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Dialect: dialectName, Verdict: VerdictFresh, BuildHead: headOf(names), Pending: []PlannedMigration{}, Newer: []string{}, Unknown: []string{}}
	for _, name := range names {
		plan.Pending = append(plan.Pending, planned(dialectName, names, name))
	}
	return plan, nil
}
