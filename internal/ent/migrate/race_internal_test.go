// The proof that any number of starters can migrate one database at the same
// instant and every one of them ends with a migrated database, against real
// SQLite and real PostgreSQL.
//
// Two kinds of test, for two different claims:
//
//   - The interleaving tests hold a second connection's transaction at the
//     exact point that matters (a migration claimed and not yet committed)
//     and then commit it, roll it back, or commit something a newer build
//     would. Each of the three outcomes the apply loop distinguishes is
//     reached on purpose, not by luck.
//   - TestApplyAcrossRealProcesses starts separate processes released at one
//     instant, which is what a scaled deployment actually does, and checks
//     the property that matters to an operator: every process starts, and
//     every migration was applied exactly once.
//
// Before this, the loser of a race took the winner's DDL or the version key
// as a fatal error, and a controller failed to start because another one had
// done its work for it.
package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// claimFirst plays a racing starter that has claimed the first migration and
// not yet committed it: on its own pool it opens a transaction, records the
// migration first (as applyOneOn does) and runs its script. The caller decides
// what that starter does next.
func claimFirst(t *testing.T, dialectName, dsn string, alsoRecord ...string) *sql.Tx {
	t.Helper()
	ctx := context.Background()
	src := migrationSources[dialectName]
	names := namesOf(t, dialectName)

	other := openDSN(t, dialectName, dsn)
	tx, err := other.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("the racing starter could not begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })

	for _, name := range append([]string{names[0]}, alsoRecord...) {
		if _, err := tx.ExecContext(ctx, src.insertVersion, name, time.Now().UTC(), names[0]); err != nil {
			t.Fatalf("the racing starter could not claim %s: %v", name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, readMigration(t, src, names[0])); err != nil {
		t.Fatalf("the racing starter could not apply %s: %v", names[0], err)
	}
	return tx
}

// applyResult is what one background ApplyWith call returned.
type applyResult struct {
	out Outcome
	err error
}

// startApply runs ApplyWith on its own pool in the background.
func startApply(t *testing.T, dialectName, dsn string) <-chan applyResult {
	t.Helper()
	db := openDSN(t, dialectName, dsn)
	done := make(chan applyResult, 1)
	go func() {
		out, err := ApplyWith(context.Background(), dialectName, db, Options{})
		done <- applyResult{out, err}
	}()
	return done
}

// awaitBlocked waits until the background Apply is waiting on the racing
// starter's claim, and fails if it finished instead.
//
// PostgreSQL can be asked directly, so it is. SQLite cannot, so the test
// waits long enough that an Apply which was not blocked would have finished,
// and checks that it has not.
func awaitBlocked(t *testing.T, dialectName, dsn string, done <-chan applyResult) {
	t.Helper()
	if dialectName == "postgres" {
		probe := openDSN(t, dialectName, dsn)
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			var waiting int
			if err := probe.QueryRow(`SELECT count(*) FROM pg_stat_activity
				WHERE wait_event_type = 'Lock' AND query LIKE 'INSERT INTO schema_migrations%'`).Scan(&waiting); err != nil {
				t.Fatalf("asking postgres who is waiting: %v", err)
			}
			if waiting == 1 {
				return
			}
			select {
			case r := <-done:
				t.Fatalf("Apply finished (%+v, %v) instead of waiting on the claim", r.out, r.err)
			case <-time.After(20 * time.Millisecond):
			}
		}
		t.Fatal("Apply never waited on the racing starter's claim")
	}
	select {
	case r := <-done:
		t.Fatalf("Apply finished (%+v, %v) instead of waiting on the claim", r.out, r.err)
	case <-time.After(750 * time.Millisecond):
	}
}

// prepared returns a fresh database of the dialect with its history table
// ready and nothing applied, and the DSN that reaches it.
func prepared(t *testing.T, dialectName string) string {
	t.Helper()
	dsn := freshDSN(t, dialectName)
	db := openDSN(t, dialectName, dsn)
	if _, err := prepareHistory(context.Background(), db, migrationSources[dialectName]); err != nil {
		t.Fatalf("preparing the history table: %v", err)
	}
	return dsn
}

// TestApply_LosesAClaimAndCarriesOn is the ordinary race: another starter
// claimed the first migration and commits it while this one waits. This one
// must count it as applied elsewhere and go on to apply the rest.
func TestApply_LosesAClaimAndCarriesOn(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		dsn := prepared(t, dialectName)
		names := namesOf(t, dialectName)
		winner := claimFirst(t, dialectName, dsn)

		done := startApply(t, dialectName, dsn)
		awaitBlocked(t, dialectName, dsn, done)
		if err := winner.Commit(); err != nil {
			t.Fatalf("the racing starter could not commit: %v", err)
		}

		r := <-done
		if r.err != nil {
			t.Fatalf("Apply after losing the race = %v; a lost race is not a failure", r.err)
		}
		if len(r.out.AppliedElsewhere) != 1 || r.out.AppliedElsewhere[0] != names[0] {
			t.Errorf("AppliedElsewhere = %v; want [%s]", r.out.AppliedElsewhere, names[0])
		}
		if len(r.out.Applied) != len(names)-1 {
			t.Errorf("applied %d migrations after the lost one; want %d", len(r.out.Applied), len(names)-1)
		}
	})
}

// TestApply_TakesOverFromAWinnerThatRolledBack covers a winner that dies or
// fails: its claim disappears with its transaction, and the waiting starter
// must then apply the migration itself rather than wait for ever or give up.
func TestApply_TakesOverFromAWinnerThatRolledBack(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		dsn := prepared(t, dialectName)
		names := namesOf(t, dialectName)
		winner := claimFirst(t, dialectName, dsn)

		done := startApply(t, dialectName, dsn)
		awaitBlocked(t, dialectName, dsn, done)
		if err := winner.Rollback(); err != nil {
			t.Fatalf("the racing starter could not roll back: %v", err)
		}

		r := <-done
		if r.err != nil {
			t.Fatalf("Apply after the winner rolled back = %v", r.err)
		}
		if len(r.out.Applied) != len(names) || len(r.out.AppliedElsewhere) != 0 {
			t.Errorf("outcome = applied %d, elsewhere %v; want all %d applied here", len(r.out.Applied), r.out.AppliedElsewhere, len(names))
		}
	})
}

// TestApply_RefusesANewerBuildThatWonTheRace covers the one loss that is not
// harmless: the starter that won was a newer build, and recorded a migration
// this one does not know. The reload after losing must run the gate again and
// refuse, rather than carry on applying against a schema it cannot read.
func TestApply_RefusesANewerBuildThatWonTheRace(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		dsn := prepared(t, dialectName)
		names := namesOf(t, dialectName)
		future := nextName(t, names[len(names)-1], "from_a_newer_build")
		winner := claimFirst(t, dialectName, dsn, future)

		done := startApply(t, dialectName, dsn)
		awaitBlocked(t, dialectName, dsn, done)
		if err := winner.Commit(); err != nil {
			t.Fatalf("the racing starter could not commit: %v", err)
		}

		r := <-done
		if r.err == nil || !strings.Contains(r.err.Error(), future) {
			t.Fatalf("Apply after a newer build won = %v; want a refusal naming %s", r.err, future)
		}
	})
}

// racerChildVar names the environment variable that turns a re-executed test
// binary into one starting migrator. A child process rather than a goroutine,
// because the claim is about separate processes sharing one database, and a
// reviewer is entitled to ask whether a pass came from shared memory instead.
const racerChildVar = "RUN_MIGRATE_RACER"

// racerCounts is how many starters race, one subtest each. Thirty-two is far
// past any plausible replica count; contention is what makes a race show.
var racerCounts = []int{2, 4, 16, 32}

// racerOutcome matches the line a racer child prints about what it did.
var racerOutcome = regexp.MustCompile(`racer applied=(\d+) elsewhere=(\d+)`)

// TestApplyAcrossRealProcesses starts N separate processes against one fresh
// database, released at the same instant, and requires every one of them to
// start and every migration to have been applied exactly once between them.
func TestApplyAcrossRealProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("starts up to thirty-two processes per dialect")
	}
	forEachDialect(t, func(t *testing.T, dialectName string) {
		names := namesOf(t, dialectName)
		for _, n := range racerCounts {
			t.Run(strconv.Itoa(n), func(t *testing.T) {
				dsn := createdDSN(t, dialectName)
				outputs, failures := runRacers(t, dialectName, dsn, n)

				applied := 0
				for i := range outputs {
					if failures[i] != nil {
						t.Errorf("racer %d of %d refused to start (%v):\n%s", i, n, failures[i], outputs[i])
						continue
					}
					match := racerOutcome.FindStringSubmatch(outputs[i])
					if match == nil {
						t.Errorf("racer %d reported no outcome:\n%s", i, outputs[i])
						continue
					}
					mine, _ := strconv.Atoi(match[1])
					applied += mine
				}
				if t.Failed() {
					return
				}
				if applied != len(names) {
					t.Errorf("the racers applied %d migrations between them; want each of %d exactly once", applied, len(names))
				}
				assertFullyMigrated(t, dialectName, dsn, names)
			})
		}
	})
}

// TestApplyAcrossRealProcesses_ABrokenMigrationFailsEveryRacer is the
// control: a migration that genuinely cannot apply must fail in every racer,
// be recorded by none, and never be reported as somebody else's success.
// Without it, the test above would also pass for an apply loop that treated
// every failure as a lost race.
func TestApplyAcrossRealProcesses_ABrokenMigrationFailsEveryRacer(t *testing.T) {
	if testing.Short() {
		t.Skip("starts several processes per dialect")
	}
	forEachDialect(t, func(t *testing.T, dialectName string) {
		dsn := createdDSN(t, dialectName)
		names := namesOf(t, dialectName)
		// A devices table of the wrong shape makes the first migration's own
		// CREATE TABLE fail for real, in every process that tries it.
		if _, err := openDSN(t, dialectName, dsn).Exec(`CREATE TABLE devices (id INTEGER PRIMARY KEY)`); err != nil {
			t.Fatalf("planting a conflicting table: %v", err)
		}

		outputs, failures := runRacers(t, dialectName, dsn, 4)
		for i := range outputs {
			if failures[i] == nil {
				t.Errorf("racer %d started against a migration that cannot apply:\n%s", i, outputs[i])
			}
			if !strings.Contains(outputs[i], "applying "+names[0]) {
				t.Errorf("racer %d did not report the migration's own failure:\n%s", i, outputs[i])
			}
		}
		if n := len(historyOf(t, openDSN(t, dialectName, dsn), dialectName)); n != 0 {
			t.Errorf("history holds %d rows after a migration no racer could apply; want none", n)
		}
	})
}

// createdDSN returns a fresh, empty database that already exists, so the race
// is between migrations and not between processes creating a file.
//
// For SQLite that means the file is created, in WAL mode, before any racer
// opens it. Creating a brand new file from several processes at once has its
// own race, inside the driver's Open, which is not this package's to solve:
// internal/ent's open path settles it before Apply ever runs
// (ensureSQLiteWAL), and is raced through that real path in its own test.
// PostgreSQL has no such step; the database exists once created.
func createdDSN(t *testing.T, dialectName string) string {
	t.Helper()
	dsn := freshDSN(t, dialectName)
	if err := openDSN(t, dialectName, dsn).Ping(); err != nil {
		t.Fatalf("creating the database: %v", err)
	}
	return dsn
}

// runRacers starts n racer processes against dsn, releases them together, and
// returns each one's output and exit error.
//
// The release is a file every child waits for after announcing itself, so no
// child starts migrating until all of them are loaded and ready. Without the
// barrier the processes start one after another, which is the arrangement
// that never failed even before any of this was fixed.
func runRacers(t *testing.T, dialectName, dsn string, n int) ([]string, []error) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Skipf("this platform cannot name the running test binary: %v", err)
	}
	gate := t.TempDir()

	outputs := make([]string, n)
	failures := make([]error, n)
	var wg sync.WaitGroup
	// exited is closed-over by every child's goroutine: a child that ends
	// before announcing itself will never announce itself, and waiting for
	// it would only turn its own error into a timeout.
	exited := make(chan int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { exited <- i }()
			// #nosec G204 -- this test binary's own path and a fixed flag.
			cmd := osexec.Command(self, "-test.run=^TestApplyRacerChild$", "-test.v")
			cmd.Env = append(os.Environ(),
				racerChildVar+"=1",
				"MIGRATE_RACER_DIALECT="+dialectName,
				"MIGRATE_RACER_DSN="+dsn,
				"MIGRATE_RACER_GATE="+gate,
				fmt.Sprintf("MIGRATE_RACER_ID=%d", i),
			)
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			failures[i] = cmd.Run()
			outputs[i] = out.String()
		}(i)
	}

	// Release every child once all of them have announced themselves, or
	// as soon as one has ended without doing so, whose output then says why.
	deadline := time.Now().Add(2 * time.Minute)
wait:
	for {
		ready, _ := filepath.Glob(filepath.Join(gate, "ready-*"))
		if len(ready) == n {
			break
		}
		select {
		case i := <-exited:
			exited <- i
			break wait
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d racers became ready", len(ready), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(gate, "go"), nil, 0o600); err != nil {
		t.Fatalf("releasing the racers: %v", err)
	}
	wg.Wait()
	return outputs, failures
}

// TestApplyRacerChild is one racer process, and does nothing at all when it is
// not being used as one.
func TestApplyRacerChild(t *testing.T) {
	if os.Getenv(racerChildVar) == "" {
		t.Skip("not a racer child; see TestApplyAcrossRealProcesses")
	}
	dialectName := os.Getenv("MIGRATE_RACER_DIALECT")
	gate := os.Getenv("MIGRATE_RACER_GATE")

	db, err := sql.Open(dialectName, os.Getenv("MIGRATE_RACER_DSN"))
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	defer db.Close()
	// A small pool, the way a deployment's replicas each hold their own.
	db.SetMaxOpenConns(4)
	// Open the connection before the release, so the race is between
	// migrations and not between connection handshakes.
	if err := db.Ping(); err != nil {
		t.Fatalf("connecting: %v", err)
	}

	if err := os.WriteFile(filepath.Join(gate, "ready-"+os.Getenv("MIGRATE_RACER_ID")), nil, 0o600); err != nil {
		t.Fatalf("announcing readiness: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(gate, "go")); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}

	out, err := ApplyWith(context.Background(), dialectName, db, Options{})
	if err != nil {
		t.Fatalf("ApplyWith: %v", err)
	}
	t.Logf("racer applied=%d elsewhere=%d", len(out.Applied), len(out.AppliedElsewhere))
}

// assertFullyMigrated checks that dsn's history names every migration exactly
// once, each with the floor this build computes, and that a further Apply
// finds nothing left to do.
func assertFullyMigrated(t *testing.T, dialectName, dsn string, names []string) {
	t.Helper()
	db := openDSN(t, dialectName, dsn)
	history := historyOf(t, db, dialectName)
	if len(history) != len(names) {
		t.Fatalf("history holds %d rows; want %d", len(history), len(names))
	}
	for _, r := range history {
		if want := floorAfter(dialectName, names, r.version); r.floor.String != want {
			t.Errorf("%s records floor %q; want %q", r.version, r.floor.String, want)
		}
	}
	out, err := ApplyWith(context.Background(), dialectName, db, Options{})
	if err != nil || len(out.Applied) != 0 {
		t.Errorf("a further Apply = %+v, %v; want nothing left to do", out, err)
	}
}

// BenchmarkApply measures the two costs a controller pays: migrating a fresh
// database from nothing, and the check every later start makes against a
// database already up to date. The second is the one paid on every restart
// of every replica, so it is the one that has to stay small.
func BenchmarkApply(b *testing.B) {
	for _, dialectName := range dialects {
		b.Run(dialectName+"/fresh", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				db := freshDB(b, dialectName)
				b.StartTimer()
				if err := Apply(context.Background(), dialectName, db); err != nil {
					b.Fatalf("Apply(): %v", err)
				}
			}
		})
		b.Run(dialectName+"/up-to-date", func(b *testing.B) {
			db := freshDB(b, dialectName)
			if err := Apply(context.Background(), dialectName, db); err != nil {
				b.Fatalf("Apply(): %v", err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := Apply(context.Background(), dialectName, db); err != nil {
					b.Fatalf("Apply(): %v", err)
				}
			}
		})
	}
}
