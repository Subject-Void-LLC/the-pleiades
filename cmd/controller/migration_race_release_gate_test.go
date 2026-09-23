// Phase 84's concurrency gate, on the real controller binary: N controllers
// started at the same instant against one database that has never been
// migrated, and every one of them must reach a serving state.
//
// internal/ent/migrate proves its apply loop against racing processes, and
// internal/ent proves the open path. What an operator depends on is longer
// than either: resolve the DSN, bind the listener, migrate, join the fleet,
// wire everything, and answer /readyz. Before this phase the loser of the
// migration race died at the migrate step, so a scaled deployment's first
// start (or any rolling upgrade with a migration in it) left some replicas
// crash-looping until the schema happened to be ready when they retried.
//
// The same file carries the binary-level half of the compatibility window:
// a database a newer build migrated within this build's window is served, one
// past it is refused at start, and one contracted past it while the build
// runs makes the build stop on its next heartbeat, with a non-zero exit.
package main_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/migrate"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

// gateNATS starts a real NATS server with JetStream for one test.
func gateNATS(t *testing.T) string {
	t.Helper()
	natsURL := testsupport.StartNATS(t).URL()
	return natsURL
}

// gatePostgres starts a real PostgreSQL server and returns a DSN for its
// initial database. max_connections is raised because eight controllers
// each hold a pool of up to sixteen, which is more than the default hundred:
// the documented limit an operator scaling replicas has to plan for too.
func gatePostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	container, err := testpg.Run(ctx,
		testsupport.PostgresImage,
		testpg.WithDatabase("pleiades"),
		testpg.WithUsername("pleiades"),
		testpg.WithPassword("pleiades"),
		testcontainers.WithCmdArgs("-c", "max_connections=300"),
		testpg.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("starting postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("reading the postgres DSN: %v", err)
	}
	return dsn
}

// freshPostgresDatabase creates an empty database on the server and returns
// its DSN.
func freshPostgresDatabase(t *testing.T, adminDSN, name string) string {
	t.Helper()
	admin, err := sql.Open("postgres", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	// The name is this file's own constant.
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
	parsed, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	return parsed.String()
}

// gateController is one controller process this file started.
type gateController struct {
	cmd     *exec.Cmd
	baseURL string
	tlsDir  string

	mu     sync.Mutex
	out    strings.Builder
	exited chan struct{}
	err    error
}

// output is everything the process printed so far.
func (c *gateController) output() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.out.String()
}

// Write collects the process's output for output().
func (c *gateController) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.out.Write(p)
}

// gateEnv is what every controller in these tests is started with: the
// production defaults (TLS provisioned by the controller itself), with only
// the paths a test must own pointed into its temp directory.
func gateEnv(t *testing.T, natsURL, dsn string, port int, tlsDir string) []string {
	return append(os.Environ(),
		"NATS_URL="+natsURL,
		"DB_DSN="+dsn,
		"DB_PATH=",
		"LISTEN_ADDR=127.0.0.1:"+strconv.Itoa(port),
		"JWT_SECRET=migration-gate-jwt-secret-not-a-real-credential",
		"MASTER_ENCRYPTION_KEY="+base64.StdEncoding.EncodeToString([]byte(strings.Repeat("m", 32))),
		"RUNBOOK_DIR="+t.TempDir(),
		"PLEIADES_TLS_AUTOCERT_DIR="+tlsDir,
		// A short drain, so a test that stops a controller does not wait
		// out the production default for nothing.
		"SHUTDOWN_DRAIN=100ms",
	)
}

// startGateController starts one controller and returns it without waiting
// for it to be ready.
func startGateController(t *testing.T, natsURL, dsn string) *gateController {
	t.Helper()
	port := freeTCPPort(t)
	c := &gateController{
		baseURL: "https://127.0.0.1:" + strconv.Itoa(port),
		tlsDir:  filepath.Join(t.TempDir(), "tls"),
		exited:  make(chan struct{}),
	}
	// #nosec G204 -- the binary this package built, with a fixed environment.
	c.cmd = exec.Command(binPath)
	c.cmd.Env = gateEnv(t, natsURL, dsn, port, c.tlsDir)
	c.cmd.Stdout = c
	c.cmd.Stderr = c
	if err := c.cmd.Start(); err != nil {
		t.Fatalf("starting a controller: %v", err)
	}
	go func() {
		c.err = c.cmd.Wait()
		close(c.exited)
	}()
	t.Cleanup(func() {
		_ = c.cmd.Process.Kill()
		<-c.exited
		if t.Failed() {
			t.Logf("controller on %s printed:\n%s", c.baseURL, c.output())
		}
	})
	return c
}

// awaitReady waits until the controller answers /readyz with 200, and fails
// the test if it exits first.
func (c *gateController) awaitReady(t *testing.T, within time.Duration) {
	t.Helper()
	client := trustControllerCertificate(t, c.tlsDir)
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		select {
		case <-c.exited:
			t.Fatalf("the controller on %s exited (%v) before it was ready", c.baseURL, c.err)
		default:
		}
		resp, err := client.Get(c.baseURL + "/readyz")
		if err == nil {
			ready := resp.StatusCode == http.StatusOK
			resp.Body.Close()
			if ready {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the controller on %s was not ready within %s", c.baseURL, within)
}

// TestControllersStartedTogetherOnAnUnmigratedDatabaseAllServe is the gate.
func TestControllersStartedTogetherOnAnUnmigratedDatabaseAllServe(t *testing.T) {
	if testing.Short() {
		t.Skip("starts postgres, nats and up to eight controllers")
	}
	natsURL := gateNATS(t)
	adminDSN := gatePostgres(t)

	for _, n := range []int{2, 4, 8} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			dsn := freshPostgresDatabase(t, adminDSN, fmt.Sprintf("race_%d", n))

			// Start them all at once: every exec is launched from its own
			// goroutine, released together, so the processes reach the
			// migrate step within milliseconds of each other.
			controllers := make([]*gateController, n)
			var wg sync.WaitGroup
			release := make(chan struct{})
			var mu sync.Mutex
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-release
					c := startGateController(t, natsURL, dsn)
					mu.Lock()
					controllers[i] = c
					mu.Unlock()
				}(i)
			}
			close(release)
			wg.Wait()

			for _, c := range controllers {
				c.awaitReady(t, 2*time.Minute)
			}

			// And the database is what one clean migration leaves: every
			// migration recorded exactly once, and one heartbeat per
			// controller.
			db, err := sql.Open("postgres", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			// Compared with this build's own migrations, not with itself:
			// version is the table's primary key, so counting its rows against
			// its distinct versions could never differ, and a history missing
			// a migration, or holding one this build does not have, passed.
			fresh, err := migrate.PlanForNewDatabase("postgres")
			if err != nil {
				t.Fatal(err)
			}
			want := make([]string, 0, len(fresh.Pending))
			for _, m := range fresh.Pending {
				want = append(want, m.Name)
			}
			recorded, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for recorded.Next() {
				var version string
				if err := recorded.Scan(&version); err != nil {
					t.Fatal(err)
				}
				got = append(got, version)
			}
			if err := recorded.Err(); err != nil {
				t.Fatal(err)
			}
			recorded.Close()
			if len(want) == 0 || strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("schema_migrations records %v; want exactly this build's migrations, %v", got, want)
			}
			var beats int
			if err := db.QueryRow(`SELECT count(*) FROM controller_instances`).Scan(&beats); err != nil {
				t.Fatal(err)
			}
			if beats != n {
				t.Errorf("controller_instances holds %d heartbeats; want one per controller, %d", beats, n)
			}
		})
	}
}

// TestControllerServesWithinItsWindowAndStopsPastIt drives the compatibility
// window through the real binary, on SQLite.
func TestControllerServesWithinItsWindowAndStopsPastIt(t *testing.T) {
	if testing.Short() {
		t.Skip("starts nats and several controllers")
	}
	natsURL := gateNATS(t)
	dbPath := filepath.Join(t.TempDir(), "controller.db")
	dsn := "sqlite://" + dbPath

	// A first start migrates the database.
	first := startGateController(t, natsURL, dsn)
	first.awaitReady(t, time.Minute)
	stopController(t, first)

	raw, err := sql.Open("sqlite3", "file:"+dbPath+"?_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var head string
	if err := raw.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&head); err != nil {
		t.Fatal(err)
	}
	var n int
	if _, err := fmt.Sscanf(head, "%04d_", &n); err != nil {
		t.Fatalf("reading %q: %v", head, err)
	}
	newer := fmt.Sprintf("%04d_from_a_newer_build.sql", n+1)

	// A newer build migrated it, within this build's window: served.
	if _, err := raw.Exec(`INSERT INTO schema_migrations (version, applied_at, compatible_from) VALUES (?, ?, ?)`, newer, time.Now().UTC(), head); err != nil {
		t.Fatal(err)
	}
	within := startGateController(t, natsURL, dsn)
	within.awaitReady(t, time.Minute)

	// Then that build contracts the schema past this one while it runs:
	// the next heartbeat stops it, non-zero, saying why.
	if _, err := raw.Exec(`UPDATE schema_migrations SET compatible_from = version WHERE version = ?`, newer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-within.exited:
	case <-time.After(45 * time.Second):
		t.Fatal("the controller kept serving a database contracted past it for longer than a heartbeat")
	}
	if within.err == nil || !strings.Contains(within.output(), "moved past this build") {
		t.Errorf("the controller stopped with %v; want a non-zero exit naming the reason", within.err)
	}

	// And a fresh start is refused, with the reason and what to do.
	refused := startGateController(t, natsURL, dsn)
	select {
	case <-refused.exited:
	case <-time.After(time.Minute):
		t.Fatal("a controller started against a database past its window kept running")
	}
	if out := refused.output(); refused.err == nil || !strings.Contains(out, newer) || !strings.Contains(out, "restore the backup taken before the upgrade") {
		t.Errorf("the refusal (%v) does not name the migration and the way back:\n%s", refused.err, out)
	}
}

// stopController sends SIGTERM and waits for a clean exit.
func stopController(t *testing.T, c *gateController) {
	t.Helper()
	if err := c.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signalling the controller: %v", err)
	}
	select {
	case <-c.exited:
	case <-time.After(30 * time.Second):
		t.Fatal("the controller did not stop within 30s of a signal")
	}
	if c.err != nil {
		t.Fatalf("the controller exited with %v after a signal; want a clean stop", c.err)
	}
}
