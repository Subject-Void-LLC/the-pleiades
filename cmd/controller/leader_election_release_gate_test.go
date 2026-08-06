// Package main_test drives the real built controller binary through
// actual OS subprocesses, the same way a real deployment runs it. RULE 0
// (AGENTS.md) requires this: an in-process, 3-goroutine election test
// (internal/election's own election_test.go already has one) proves the
// election mechanism itself works, but this phase's own Release Gate
// asks for something stronger -- "Three controller instances are spun up
// locally... When killed, another takes over within 3 seconds" -- which
// only real, independent OS processes and a real signal can prove.
package main_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/goleak"
)

// acquiredLeaseMessage is the exact literal slog message
// cmd/controller/main.go emits when its own LeaderElector wins the
// scheduler lease. This test log-scrapes it: the Release Gate's own
// text ("Only one prints 'Acquired Scheduler Lease.'") is entirely about
// observable output, never an HTTP call (see startController's own doc
// comment on why no authenticated request is made here).
const acquiredLeaseMessage = "Acquired Scheduler Lease"

var binPath string

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "controller-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	binPath = filepath.Join(tmpDir, "controller")
	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build controller binary: %v\n%s\n", err, out)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// acquisitionEvent records that the process at idx printed
// acquiredLeaseMessage, and when this test observed it.
type acquisitionEvent struct {
	idx int
	at  time.Time
}

// controllerProc is one real, independent controller subprocess this
// test started and is watching.
type controllerProc struct {
	name string
	cmd  *exec.Cmd
	done chan struct{} // closed once this process's own stdout-draining goroutine (and its own cmd.Wait) has fully returned
}

// freeTCPPort asks the OS for a currently-unused TCP port by binding to
// port 0 and immediately releasing it. This has the same inherent,
// accepted TOCTOU race every tool using this trick has (including
// net/http/httptest.Server internally): nothing stops another process
// from binding the same port between this call returning and the real
// subprocess starting. No stronger guarantee is warranted for a local
// test allocating 3 ports for its own exclusive use.
func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate a free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// startController launches one real controller subprocess against
// natsURL, with its own isolated LISTEN_ADDR and DB_PATH, and starts a
// goroutine that continuously drains its stdout, forwarding every
// acquiredLeaseMessage line it sees to acquisitions.
//
// Stdout, not stderr: main installs a JSON slog handler over os.Stdout
// and makes it the default, per PATTERNS.md's Sidecar entry ("structured
// logs go to stdout for the platform to scrape rather than being shipped
// by a co-located agent"). This test used to read stderr, which is where
// log/slog's own built-in default handler writes, and would silently see
// nothing at all if that ever diverged again.
//
// No HTTP request is ever made against this process's own LISTEN_ADDR:
// the Release Gate this test proves is entirely about log output and
// failover timing, never an authenticated call, so jwtSecret and
// masterEncryptionKey only need to be non-empty/well-formed for main() to
// start at all.
func startController(t *testing.T, natsURL, jwtSecret, masterEncryptionKey string, idx int, acquisitions chan<- acquisitionEvent) *controllerProc {
	t.Helper()

	port := freeTCPPort(t)
	dbPath := filepath.Join(t.TempDir(), fmt.Sprintf("controller-%d.db", idx))
	name := fmt.Sprintf("controller-%d", idx)

	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(),
		"NATS_URL="+natsURL,
		"DB_PATH="+dbPath,
		"LISTEN_ADDR=127.0.0.1:"+strconv.Itoa(port),
		"JWT_SECRET="+jwtSecret,
		"MASTER_ENCRYPTION_KEY="+masterEncryptionKey,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to open stdout pipe for %s: %v", name, err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start %s: %v", name, err)
	}

	p := &controllerProc{name: name, cmd: cmd, done: make(chan struct{})}

	go func() {
		defer close(p.done)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := sc.Text()
			if strings.Contains(line, acquiredLeaseMessage) {
				acquisitions <- acquisitionEvent{idx: idx, at: time.Now()}
			}
		}
		// The scan loop only ends once this process's own stdout fd is
		// closed (process exit), so it is now safe to Wait: calling Wait
		// while a read is still in flight can race with Wait's own pipe
		// close and turn a clean EOF into a "file already closed" error
		// (documented on exec.Cmd.StdoutPipe).
		_ = cmd.Wait()
	}()

	// Registered per process (not once for all three) so every process
	// is reliably killed and reaped, and this goroutine has fully
	// finished, before the goleak check (registered once, first, in the
	// real test below) runs: t.Cleanup functions run in last-added,
	// first-called order, so cleanups registered here (later) run before
	// the earlier-registered goleak check.
	t.Cleanup(func() {
		_ = p.cmd.Process.Kill()
		<-p.done
	})

	return p
}

// TestControllerLeaderElection_ReleaseGate is Phase 4's own Release
// Gate: three real controller subprocesses, sharing one real NATS
// JetStream KV store, contend for the scheduler lease; exactly one ever
// prints acquiredLeaseMessage; SIGKILLing it (not SIGTERM -- see below)
// proves a surviving replica takes over within 3 seconds.
//
// SIGKILL, not SIGTERM: cmd/controller already handles SIGTERM
// gracefully (cancel() -> election.LeaderElector.Run's own ctx.Done()
// release path), and that graceful path is already proven by
// internal/election's own in-process TestLeaderElection_ThreeReplicas_
// OnlyOneLeaderAndGracefulHandover. SIGKILL bypasses the Go runtime
// entirely -- no deferred cleanup, no signal handler, no Release call
// ever executes -- which is the only way to actually exercise the
// electionTTL-expiry fallback path this Release Gate's own text is
// worried about ("When killed, another takes over within 3 seconds").
func TestControllerLeaderElection_ReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Registered first, so (per t.Cleanup's last-added-first-called
	// order) it runs last, after every process below has been killed,
	// reaped, and its own draining goroutine has fully returned.
	t.Cleanup(func() { goleak.VerifyNone(t) })

	ctx := context.Background()
	natsContainer, err := nats.RunContainer(ctx,
		testcontainers.WithImage("nats:2.11"),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready")),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	t.Cleanup(func() { _ = natsContainer.Terminate(context.Background()) })

	natsURL, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	const jwtSecret = "release-gate-jwt-secret-not-a-real-credential"
	// A 32-byte key, base64-encoded exactly as an operator would set
	// MASTER_ENCRYPTION_KEY: not a real credential, but must be
	// well-formed since main() now decodes and length-checks it before
	// starting (this phase's own composition-root wiring).
	masterEncryptionKey := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	acquisitions := make(chan acquisitionEvent, 32)

	procs := make([]*controllerProc, 3)
	for i := range procs {
		procs[i] = startController(t, natsURL, jwtSecret, masterEncryptionKey, i, acquisitions)
	}

	winner := -1
	select {
	case ev := <-acquisitions:
		winner = ev.idx
	case <-time.After(15 * time.Second):
		t.Fatal("no process printed the acquired-lease message within 15s")
	}
	t.Logf("%s acquired the scheduler lease first", procs[winner].name)

	// Grace window: keep draining every process's own stream and confirm
	// no OTHER process also claims the lease. This is the actual "only
	// one" proof, not just "first one wins."
	graceDeadline := time.After(2 * time.Second)
grace:
	for {
		select {
		case ev := <-acquisitions:
			if ev.idx != winner {
				t.Fatalf("expected only %s to hold the lease, but %s also printed the acquired-lease message. SPLIT BRAIN DETECTED!",
					procs[winner].name, procs[ev.idx].name)
			}
		case <-graceDeadline:
			break grace
		}
	}

	killTime := time.Now()
	if err := procs[winner].cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("failed to SIGKILL %s: %v", procs[winner].name, err)
	}

	select {
	case ev := <-acquisitions:
		if ev.idx == winner {
			t.Fatalf("received another acquisition from %s, which was just SIGKILLed; expected a surviving process", procs[winner].name)
		}
		failoverElapsed := ev.at.Sub(killTime)
		t.Logf("%s acquired the scheduler lease %v after %s was SIGKILLed", procs[ev.idx].name, failoverElapsed, procs[winner].name)
		if failoverElapsed >= 3*time.Second {
			t.Fatalf("failover took %v, expected under 3 seconds", failoverElapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("no surviving process acquired the lease within 10s of SIGKILLing %s", procs[winner].name)
	}
}
