package main_test

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entoccurrence "github.com/Subject-Void-LLC/the-pleiades/internal/ent/scheduleoccurrence"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"

	_ "github.com/mattn/go-sqlite3"
)

// Phase 23's duplicate-fire gate, driven through real controller
// subprocesses rather than in process.
//
// internal/schedule already proves the guard directly: eight concurrent
// Scanners against one real database and one real unique index produce one
// job (TestConcurrentSweepsFireOnce). That test cannot prove the thing this
// one is for, which is that the SHIPPED BINARY reaches any of it. A
// complete, well-tested component the running program never constructs is
// this repository's most-recorded failure (FAILURE_PATTERNS.md #52, #110),
// and the scheduler is a standing candidate for it: cmd/controller elected
// a scheduler lease from Phase 4 to Phase 23 and gated nothing on it.
//
// So this starts three real controller processes against one real NATS and
// one shared database holding one overdue schedule, and asserts that
// between them they run it exactly once. RULE 0's representative-or-nothing
// rule is what makes the subprocesses necessary: three goroutines in one
// process would share a connection pool and an election object, which is
// not the arrangement that fails.

// schedulerGateTimeout bounds how long the gate waits for the fleet to run
// the overdue schedule. The scan interval is 30 seconds and a replica may
// have to win the lease first, so this is deliberately generous: the
// assertion is about how MANY times it ran, never how quickly.
const schedulerGateTimeout = 90 * time.Second

func TestControllerScheduler_FiresExactlyOnce_ReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	natsContainer, err := nats.RunContainer(ctx,
		testcontainers.WithImage(testsupport.NATSImage),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready").WithStartupTimeout(testsupport.ContainerStartupTimeout)),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	t.Cleanup(func() { _ = natsContainer.Terminate(context.Background()) })

	natsURL, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	// ONE database file for all three replicas, which is the whole point.
	// The existing election gate gives each replica its own, because it
	// only watches log lines; here the replicas must contend for the same
	// rows, since the unique index on (schedule, occurrence_at) is what
	// actually decides the race.
	stateDir := t.TempDir()
	dbPath := filepath.Join(stateDir, "shared-controller.db")
	runbookDir := t.TempDir()

	scheduleID := seedOverdueSchedule(t, dbPath, runbookDir)

	const jwtSecret = "scheduler-gate-jwt-secret-not-a-real-credential"
	masterEncryptionKey := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))

	for i := 0; i < 3; i++ {
		startSchedulerController(t, natsURL, dbPath, runbookDir, jwtSecret, masterEncryptionKey, i)
	}

	occurrences := waitForOccurrences(t, dbPath, scheduleID)

	var fired, claimed, skipped int
	for _, o := range occurrences {
		switch o.Outcome {
		case entoccurrence.OutcomeFired:
			fired++
		case entoccurrence.OutcomeClaimed:
			claimed++
		case entoccurrence.OutcomeSkipped:
			skipped++
		}
	}

	if fired != 1 {
		t.Errorf("three controllers ran the overdue schedule %d times, want exactly 1 (skipped=%d, claimed=%d)",
			fired, skipped, claimed)
	}
	if claimed != 0 {
		t.Errorf("%d occurrences were claimed but never resolved; a replica won the claim and stopped before recording what happened", claimed)
	}

	// The job must exist too. A "fired" row whose job was never created
	// would be a scheduler that records runs it did not perform, which is
	// worse than one that does not run at all.
	client := openSharedDB(t, dbPath)
	defer func() { _ = client.Close() }()

	jobs, err := client.Job.Query().All(context.Background())
	if err != nil {
		t.Fatalf("reading jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("the fleet created %d jobs for one overdue occurrence, want exactly 1", len(jobs))
	}
	if want := schedule.ScheduleActor(scheduleID); jobs[0].Actor != want {
		t.Errorf("job actor = %q, want %q so the audit trail names the schedule that caused it", jobs[0].Actor, want)
	}
}

// seedOverdueSchedule writes an organization, inventory, template and one
// already-due schedule straight into the database, and returns the
// schedule's opaque id.
//
// Seeding through ent rather than through the API keeps this gate about the
// one thing it is for. Creating the same four records over HTTP would mean
// minting a token, waiting for a replica to serve, and threading four
// requests through TLS -- all of which is covered elsewhere, and any of
// which failing would fail this test for a reason that has nothing to do
// with firing exactly once.
func seedOverdueSchedule(t *testing.T, dbPath, runbookDir string) string {
	t.Helper()

	// A real runbook file, because the template names a definition the
	// launch path resolves. An empty directory is enough for the election
	// gate, which never dispatches; this one does.
	runbookPath := filepath.Join(runbookDir, "gate.yml")
	const runbookYAML = "id: gate\nname: scheduler gate\nhosts: all\ntasks: []\n"
	if err := os.WriteFile(runbookPath, []byte(runbookYAML), 0o600); err != nil {
		t.Fatalf("writing a runbook for the template to name: %v", err)
	}

	client := openSharedDB(t, dbPath)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	org, err := client.Organization.Create().SetName("gate").Save(ctx)
	if err != nil {
		t.Fatalf("seeding an organization: %v", err)
	}
	inv, err := client.Inventory.Create().SetName("gate-inventory").SetOrganization(org).Save(ctx)
	if err != nil {
		t.Fatalf("seeding an inventory: %v", err)
	}
	tmpl, err := client.Template.Create().
		SetName("gate-template").
		SetKind("runbook").
		SetDefinition("gate").
		SetOrganization(org).
		SetInventory(inv).
		Save(ctx)
	if err != nil {
		t.Fatalf("seeding a template: %v", err)
	}

	// Overdue by an hour, on an hourly rule, so every replica's very first
	// scan finds it due. next_run is written directly rather than computed,
	// because the point is to have something already waiting the moment the
	// processes start rather than to wait out a real recurrence.
	dtstart := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Hour)
	sched, err := client.Schedule.Create().
		SetName("gate-schedule").
		SetEnabled(true).
		SetRrule("FREQ=HOURLY").
		SetTimezone("UTC").
		SetDtstart(dtstart).
		SetNextRun(time.Now().UTC().Add(-time.Hour).Truncate(time.Hour)).
		SetOrganization(org).
		SetTemplate(tmpl).
		Save(ctx)
	if err != nil {
		t.Fatalf("seeding a schedule: %v", err)
	}
	return sched.ScheduleID
}

// openSharedDB opens the shared database through the same entry point the
// controller uses, so the test and the processes agree on journal mode,
// busy timeout and migration state rather than fighting each other over
// locks or disagreeing about the schema. It also means the seed runs
// against migrations, not against Schema.Create, which is what the shipped
// binary will find when it starts.
func openSharedDB(t *testing.T, dbPath string) *ent.Client {
	t.Helper()
	client, err := ent.OpenDatabase(context.Background(), ent.Config{DSN: "sqlite://" + dbPath})
	if err != nil {
		t.Fatalf("opening the shared database: %v", err)
	}
	return client
}

// waitForOccurrences polls until the schedule has produced at least one
// occurrence row, then waits a further grace window and returns everything
// recorded.
//
// The grace window is the actual duplicate-fire proof and is not optional.
// Returning at the first row would only show that SOMEBODY ran it; a second
// replica firing the same occurrence a moment later is exactly the defect
// under test, so the test has to keep watching after it has what it wanted.
func waitForOccurrences(t *testing.T, dbPath, scheduleID string) []*ent.ScheduleOccurrence {
	t.Helper()

	client := openSharedDB(t, dbPath)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	deadline := time.After(schedulerGateTimeout)
	for {
		rows, err := client.ScheduleOccurrence.Query().All(ctx)
		if err != nil {
			t.Fatalf("reading occurrences: %v", err)
		}
		if len(rows) > 0 {
			t.Logf("first occurrence recorded for %s; watching for a duplicate", scheduleID)
			// Long enough for another replica to complete a scan of its
			// own, since a contending fire would land within one interval.
			time.Sleep(schedule.DefaultScanInterval + 5*time.Second)
			final, err := client.ScheduleOccurrence.Query().All(ctx)
			if err != nil {
				t.Fatalf("re-reading occurrences: %v", err)
			}
			return final
		}
		select {
		case <-deadline:
			t.Fatalf("no controller ran the overdue schedule within %s. "+
				"The scheduler is either not constructed in cmd/controller, not gated on the scheduler lease, "+
				"or not reaching the shared database.", schedulerGateTimeout)
		case <-time.After(time.Second):
		}
	}
}

// startSchedulerController starts one controller subprocess against the
// shared database.
func startSchedulerController(t *testing.T, natsURL, dbPath, runbookDir, jwtSecret, masterEncryptionKey string, idx int) {
	t.Helper()

	port := freeTCPPort(t)
	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(),
		"NATS_URL="+natsURL,
		// The shared file, named through DB_DSN rather than DB_PATH only
		// because the two are mutually exclusive and this reads as the
		// deliberate choice it is.
		"DB_DSN=sqlite://"+dbPath,
		"LISTEN_ADDR=127.0.0.1:"+strconv.Itoa(port),
		"JWT_SECRET="+jwtSecret,
		"MASTER_ENCRYPTION_KEY="+masterEncryptionKey,
		"RUNBOOK_DIR="+runbookDir,
		"PLEIADES_TLS_AUTOCERT_DIR="+filepath.Join(t.TempDir(), "tls"),
	)

	// The processes are noisy and nothing here scrapes their output, so it
	// goes to the test log only on failure, via a buffer the cleanup dumps.
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting controller-%d: %v", idx, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		// cmd.Wait, not cmd.Process.Wait. Because Stdout and Stderr here
		// are a strings.Builder rather than an *os.File, os/exec runs
		// goroutines that copy the pipes into it, and only cmd.Wait waits
		// for those to finish. cmd.Process.Wait reaps the process and
		// returns immediately, leaving the copiers writing into the very
		// Builder the next line reads, which the race detector correctly
		// reports. It surfaced only when this test FAILED, because that
		// is when Fatalf runs the cleanup while the process is still
		// producing output, so the race report replaced the assertion
		// message that would have explained the failure.
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("controller-%d output:\n%s", idx, out.String())
		}
	})
	t.Logf("started controller-%d on 127.0.0.1:%d", idx, port)
}
