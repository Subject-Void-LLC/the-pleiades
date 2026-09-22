// This file declares which migrations break the build before them, and turns
// those declarations into the compatibility floor every migration records.
//
// # The policy
//
// Every migration is EXPAND by default: it adds tables, nullable columns,
// NOT NULL columns with a default, or indexes that are not unique, and a
// build from before it keeps working against the schema it leaves. That is
// what lets a rolling upgrade overlap two builds, and what lets a build be
// rolled back while the database stays where it is.
//
// A migration that removes or narrows something (drops a table or column,
// changes a type, tightens or relaxes NOT NULL, changes a foreign key's delete
// action, adds a unique index over existing columns, adds a trigger or
// function) is a CONTRACT, and must be declared in the table below with the
// oldest migration whose build can still serve after it. The build that
// applies it records that floor, and older builds refuse the database, or
// stop serving it, instead of guessing.
//
// Three things enforce this, because a policy nothing enforces is a sentence:
//
//   - compat_shape_internal_test.go captures each dialect's schema after
//     every migration and fails when an undeclared migration contracts.
//   - tests/e2e's upgrade gate runs the previous build's real binary against
//     the newly migrated schema and requires it to keep serving.
//   - checkGate refuses, at every start and every heartbeat, a database whose
//     recorded floor this build does not reach.
//
// What none of them can see, and review must: a column whose MEANING changes
// while its shape does not (declare it with semantic set), NATS stream and
// wire formats, and a new value of an enum stored as text. One more is seen
// and deliberately allowed: a NEW table's foreign key onto an existing one.
// Its rows are written only by the newer build, but while they exist an older
// build deleting the parent row can be refused by that key. Counting every
// such table as a contract would make nearly every added table one and the
// window worthless, so a new table's key onto an existing table should be
// ON DELETE CASCADE or SET NULL unless there is a reason, which review states.
package migrate

// contract declares one migration that leaves a schema a build from before it
// cannot serve.
type contract struct {
	// compatibleFrom is the oldest migration whose build can serve the schema
	// this one leaves. For a contract that no earlier build survives it is
	// the migration itself.
	compatibleFrom string

	// semantic marks a contract whose shape does not change (a column keeps
	// its type and name and changes what it means). The shape test would see
	// nothing, so it requires this flag to accept such a declaration.
	semantic bool

	// grandfathered marks a contract that shipped before this policy existed.
	// Only those may appear today: a new contract needs the apply-time guard
	// that refuses to contract while an older controller is still running,
	// and that guard is not built yet (TestANewContractNeedsTheGuard).
	grandfathered bool

	// reason says, in a sentence, what the build before it would do wrong.
	reason string
}

// contracts holds every declared contract, by dialect and then by migration
// file name. The two dialects are numbered differently (SQLite's history is
// three files longer), so a declaration names each dialect's own file, and
// the shape test checks that the two dialects declare the same changes.
//
// Every entry below is grandfathered history, and goes when the 1.0.0 golden
// image squashes the migrations it names into one baseline. The table and the
// rules around it stay.
var contracts = map[string]map[string]contract{
	"sqlite3": {
		"0002_add_device_type.sql": {
			compatibleFrom: "0002_add_device_type.sql",
			grandfathered:  true,
			reason:         "adds devices.type as NOT NULL with no default, which the build before it never writes",
		},
		"0003_add_rbac_teams.sql": {
			compatibleFrom: "0003_add_rbac_teams.sql",
			grandfathered:  true,
			reason:         "rebuilds users without the role column the build before it reads",
		},
		"0031_add_sync_run_origin.sql": {
			compatibleFrom: "0031_add_sync_run_origin.sql",
			grandfathered:  true,
			reason:         "makes sync_runs.finished_at nullable, which the build before it scans into a required time, and cascades a project's delete",
		},
		"0032_add_launchables.sql": {
			compatibleFrom: "0032_add_launchables.sql",
			grandfathered:  true,
			reason:         "drops schedules.template_schedules for a required launchable_schedules the build before it never writes",
		},
	},
	"postgres": {
		"0028_add_sync_run_origin.sql": {
			compatibleFrom: "0028_add_sync_run_origin.sql",
			grandfathered:  true,
			reason:         "makes sync_runs.finished_at nullable, which the build before it scans into a required time, and cascades a project's delete",
		},
		"0029_add_launchables.sql": {
			compatibleFrom: "0029_add_launchables.sql",
			grandfathered:  true,
			reason:         "drops schedules.template_schedules for a required launchable_schedules the build before it never writes",
		},
	},
}

// floorAfter returns the compatibility floor a database of this dialect has
// once the named migration is applied: the highest compatibleFrom among the
// contracts declared at or before it, or the dialect's first migration when
// none is, since every build knows that one.
//
// names is the dialect's full ordered migration set. The floor only ever
// rises, which is what makes it safe for a reader to take the maximum over
// whatever rows it cannot explain.
func floorAfter(dialectName string, names []string, name string) string {
	if len(names) == 0 {
		return ""
	}
	floor := names[0]
	declared := contracts[dialectName]
	for _, candidate := range names {
		if c, ok := declared[candidate]; ok && c.compatibleFrom > floor {
			floor = c.compatibleFrom
		}
		if candidate == name {
			break
		}
	}
	return floor
}
