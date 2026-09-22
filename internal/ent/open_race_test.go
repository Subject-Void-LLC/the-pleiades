// Many processes opening one brand new database at the same instant, through
// the real OpenDatabase path every controller and admin command takes.
//
// internal/ent/migrate proves its apply loop against racing processes, but it
// starts them against a database that already exists, because creating a file
// is not its job. This is the test of the part before that: a controller and
// an admin command, or several replicas, pointed at a database nobody has
// opened yet. For SQLite that used to fail one of them outright, inside the
// driver's own Open, before a single migration ran (FAILURE_PATTERNS.md #277).
package ent_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// openRacerVar turns a re-executed test binary into one process opening the
// database.
const openRacerVar = "RUN_OPEN_RACER"

// TestOpenDatabase_ManyProcessesOpenOneNewDatabase starts sixteen processes
// against a database that does not exist yet, releases them together, and
// requires every one of them to open it, migrate it and query it.
func TestOpenDatabase_ManyProcessesOpenOneNewDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("starts sixteen processes per backend")
	}
	self, err := os.Executable()
	if err != nil {
		t.Skipf("this platform cannot name the running test binary: %v", err)
	}
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			// Several rounds, because the SQLite failure was a race that
			// did not show in every round.
			for round := 0; round < 3; round++ {
				dsn := backend.newDSN(t)
				const racers = 16
				gate := t.TempDir()

				outputs := make([]string, racers)
				failures := make([]error, racers)
				var wg sync.WaitGroup
				for i := 0; i < racers; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						// #nosec G204 -- this test binary's own path and a fixed flag.
						cmd := exec.Command(self, "-test.run=^TestOpenRacerChild$")
						cmd.Env = append(os.Environ(),
							openRacerVar+"=1",
							"OPEN_RACER_DSN="+dsn,
							"OPEN_RACER_GATE="+gate,
							fmt.Sprintf("OPEN_RACER_ID=%d", i),
						)
						var out bytes.Buffer
						cmd.Stdout = &out
						cmd.Stderr = &out
						failures[i] = cmd.Run()
						outputs[i] = out.String()
					}(i)
				}
				releaseWhenReady(t, gate, racers)
				wg.Wait()

				for i := range failures {
					if failures[i] != nil {
						t.Errorf("round %d: process %d of %d could not open a new database (%v):\n%s", round, i, racers, failures[i], outputs[i])
					}
				}
				if t.Failed() {
					return
				}
			}
		})
	}
}

// releaseWhenReady waits for n processes to announce themselves in gate, then
// releases them all at once.
func releaseWhenReady(t *testing.T, gate string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		ready, _ := filepath.Glob(filepath.Join(gate, "ready-*"))
		if len(ready) == n {
			break
		}
		if time.Now().After(deadline) {
			// Release whoever is waiting, so their errors are reported
			// rather than a timeout.
			_ = os.WriteFile(filepath.Join(gate, "go"), nil, 0o600)
			t.Fatalf("only %d of %d processes announced themselves", len(ready), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(gate, "go"), nil, 0o600); err != nil {
		t.Fatalf("releasing the processes: %v", err)
	}
}

// TestOpenRacerChild is one process opening the database, and does nothing
// when it is not being used as one.
func TestOpenRacerChild(t *testing.T) {
	if os.Getenv(openRacerVar) == "" {
		t.Skip("not a racer child; see TestOpenDatabase_ManyProcessesOpenOneNewDatabase")
	}
	gate := os.Getenv("OPEN_RACER_GATE")
	if err := os.WriteFile(filepath.Join(gate, "ready-"+os.Getenv("OPEN_RACER_ID")), nil, 0o600); err != nil {
		t.Fatalf("announcing readiness: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(gate, "go")); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}

	ctx := context.Background()
	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: os.Getenv("OPEN_RACER_DSN")})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer client.Close()
	if _, err := client.Device.Query().Count(ctx); err != nil {
		t.Fatalf("querying the migrated database: %v", err)
	}
}
