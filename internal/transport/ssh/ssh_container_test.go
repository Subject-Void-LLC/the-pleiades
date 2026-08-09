package ssh

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/goleak"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
)

// The shared container's fixed test credential. This is a throwaway
// container started fresh for this test run, never a real device, so a
// hardcoded password here is not a secret leak.
const (
	containerSSHUser     = "testuser"
	containerSSHPassword = "testpass123"
)

// Package-level shared fixture: ssh_container_test.go's own subtests and
// ssh_bench_test.go's benchmarks (same package, same test binary) all
// reuse this ONE container instance rather than each paying its own
// startup cost, per this file's plan. sharedContainerOnce guards lazy,
// exactly-once startup; TestMain below tears it down once, after every
// test and benchmark in the package has finished.
var (
	sharedContainerOnce sync.Once
	sharedContainerErr  error
	sharedContainer     testcontainers.Container
	sharedContainerHost string
	sharedContainerPort int

	// sharedContainerGoleak is a goleak.IgnoreCurrent() snapshot taken
	// right after the shared container (and testcontainers' own
	// supporting goroutines, e.g. its reaper) is up. Passing it to every
	// container-backed test's goleak.VerifyNone call means a test only
	// fails for leaks IT introduces, not for this long-lived shared
	// fixture's own goroutines.
	sharedContainerGoleak []goleak.Option
)

// TestMain lets ssh_container_test.go and ssh_bench_test.go share one
// container instance (requireSSHContainer starts it lazily, at most
// once) and still guarantees it is torn down exactly once, after every
// test and benchmark in this package has run, instead of leaking it for
// the remainder of the process.
func TestMain(m *testing.M) {
	code := m.Run()
	if sharedContainer != nil {
		_ = sharedContainer.Terminate(context.Background())
	}
	os.Exit(code)
}

// requireSSHContainer lazily starts (once per test binary, via
// sharedContainerOnce) a real lscr.io/linuxserver/openssh-server
// container with password auth enabled for
// containerSSHUser/containerSSHPassword, and returns its externally
// reachable host and mapped port. It fails the calling test immediately
// if the container cannot be started; callers are expected to have
// already skipped in short mode before calling this, per this
// repository's testing.Short()-gated integration test convention
// (internal/lock/nats_test.go).
func requireSSHContainer(tb testing.TB) (string, int) {
	tb.Helper()
	sharedContainerOnce.Do(func() {
		ctx := context.Background()
		req := testcontainers.ContainerRequest{
			Image:        "lscr.io/linuxserver/openssh-server:latest",
			ExposedPorts: []string{"2222/tcp"},
			Env: map[string]string{
				"PUID":            "1000",
				"PGID":            "1000",
				"PASSWORD_ACCESS": "true",
				"USER_NAME":       containerSSHUser,
				"USER_PASSWORD":   containerSSHPassword,
			},
			// "[ls.io-init] done." is this image's own real final
			// startup log line, confirmed against the actual image,
			// logged only once sshd is already listening. A fixed sleep
			// would be neither deterministic nor an honest readiness
			// check.
			WaitingFor: wait.ForLog("done.").WithStartupTimeout(3 * time.Minute),
		}
		container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			sharedContainerErr = fmt.Errorf("failed to start openssh-server container: %w", err)
			return
		}
		sharedContainer = container

		host, err := container.Host(ctx)
		if err != nil {
			sharedContainerErr = fmt.Errorf("failed to get container host: %w", err)
			return
		}
		mapped, err := container.MappedPort(ctx, "2222/tcp")
		if err != nil {
			sharedContainerErr = fmt.Errorf("failed to get mapped port: %w", err)
			return
		}
		sharedContainerHost = host
		sharedContainerPort = int(mapped.Num())
		sharedContainerGoleak = []goleak.Option{goleak.IgnoreCurrent()}
	})
	if sharedContainerErr != nil {
		tb.Fatalf("shared SSH container setup failed: %v", sharedContainerErr)
	}
	return sharedContainerHost, sharedContainerPort
}

// containerTarget returns a transport.Target for the shared SSH
// container, starting it first if this is the first test to need it.
func containerTarget(tb testing.TB) transport.Target {
	host, port := requireSSHContainer(tb)
	return transport.Target{Host: host, Port: port}
}

// containerCred returns the shared SSH container's real password
// credential.
func containerCred() credential.Credential {
	return credential.Credential{Username: containerSSHUser, Password: containerSSHPassword}
}

// verifyNoLeaks runs goleak.VerifyNone against the shared container's
// baseline (sharedContainerGoleak), read at call time rather than
// defer-argument-evaluation time: the very first container-backed test
// to run is what actually populates sharedContainerGoleak (via
// requireSSHContainer's sync.Once), so a plain
// `defer goleak.VerifyNone(t, sharedContainerGoleak...)` in that same
// test would capture a still-nil baseline. tb must be a *testing.T; it
// is accepted as testing.TB only so callers can pass it through
// helpers uniformly.
func verifyNoLeaks(t *testing.T) {
	t.Helper()
	goleak.VerifyNone(t, sharedContainerGoleak...)
}

// TestSSHContainer_ExecSuccess proves Exec with a correct password
// credential and a real command succeeds against a real, independent
// sshd implementation (not this package's own test harness), with the
// expected stdout and a zero exit code.
func TestSSHContainer_ExecSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH container integration test in short mode")
	}
	target := containerTarget(t)
	defer verifyNoLeaks(t)

	tr := New(Options{InsecureSkipHostKeyVerify: true, DialTimeout: 5 * time.Second})

	result, err := tr.Exec(context.Background(), target, containerCred(), "echo hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "hello") {
		t.Errorf("expected stdout to contain %q, got %q", "hello", result.Stdout)
	}
}

// TestSSHContainer_NonZeroExitCode proves a command that exits non-zero
// on a real remote server is reported via Result.ExitCode with a nil
// error, matching transport.Transport's documented contract.
func TestSSHContainer_NonZeroExitCode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH container integration test in short mode")
	}
	target := containerTarget(t)
	defer verifyNoLeaks(t)

	tr := New(Options{InsecureSkipHostKeyVerify: true, DialTimeout: 5 * time.Second})

	result, err := tr.Exec(context.Background(), target, containerCred(), "exit 7")
	if err != nil {
		t.Fatalf("expected no error for a non-zero remote exit code, got: %v", err)
	}
	if result.ExitCode != 7 {
		t.Errorf("expected exit code 7, got %d", result.ExitCode)
	}
}

// TestSSHContainer_WrongPasswordFails proves a wrong password is
// rejected by the real server with a genuine auth error, never a false
// success. MaxRetries is set to 1: an auth rejection is not a transient
// condition retrying would fix, so this keeps the test fast rather than
// waiting out the default retry budget for an outcome that cannot
// change.
func TestSSHContainer_WrongPasswordFails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH container integration test in short mode")
	}
	target := containerTarget(t)
	defer verifyNoLeaks(t)

	tr := New(Options{InsecureSkipHostKeyVerify: true, DialTimeout: 5 * time.Second, MaxRetries: 1})

	badCred := credential.Credential{Username: containerSSHUser, Password: "definitely-the-wrong-password"}
	_, err := tr.Exec(context.Background(), target, badCred, "echo hello")
	if err == nil {
		t.Fatal("expected a wrong password to fail with a real auth error, got a false success")
	}
}

// TestSSHContainer_HostKeyVerification is the container-level MITM
// proof (alongside known_hosts_test.go's synthetic version, both worth
// having per RULE 0: synthetic proves the logic in isolation fast,
// container proves it against a real, independent sshd, not just this
// package's own assumptions about how knownhosts behaves). It captures
// the container's real host key via a bootstrap dial, proves Exec
// succeeds when that real key is in known_hosts, then proves Exec is
// REJECTED when a different (forged) key is recorded for the same host.
func TestSSHContainer_HostKeyVerification(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH container integration test in short mode")
	}
	target := containerTarget(t)
	defer verifyNoLeaks(t)

	addr := net.JoinHostPort(target.Host, strconv.Itoa(target.Port))

	// Bootstrap dial: capture the container's REAL host key using a
	// callback that only records it rather than verifying it, the same
	// role ssh-keyscan (or a first-ever connection under
	// StrictHostKeyChecking=ask) plays in discovering a new host's key.
	var capturedKey ssh.PublicKey
	recordingConfig := &ssh.ClientConfig{
		User: containerSSHUser,
		Auth: []ssh.AuthMethod{ssh.Password(containerSSHPassword)},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			capturedKey = key
			return nil
		},
		Timeout: 5 * time.Second,
	}
	bootstrapClient, err := realDial(context.Background(), addr, recordingConfig)
	if err != nil {
		t.Fatalf("bootstrap dial to capture the real host key failed: %v", err)
	}
	bootstrapClient.Close()
	if capturedKey == nil {
		t.Fatal("expected to capture a real host key from the container")
	}

	// A known_hosts file with the REAL captured key: Exec through
	// knownhosts-based verification must succeed.
	goodPath := writeKnownHosts(t, addr, capturedKey)
	trGood := New(Options{KnownHostsPath: goodPath, DialTimeout: 5 * time.Second})
	if _, err := trGood.Exec(context.Background(), target, containerCred(), "echo hello"); err != nil {
		t.Fatalf("expected Exec to succeed against a known_hosts file with the real host key: %v", err)
	}

	// A DIFFERENT (forged) key recorded for the SAME host, simulating a
	// MITM presenting its own key: Exec must be rejected.
	forgedKey := generateTestHostKey(t)
	badPath := writeKnownHosts(t, addr, forgedKey.PublicKey())
	trBad := New(Options{KnownHostsPath: badPath, DialTimeout: 5 * time.Second, MaxRetries: 1})
	if _, err := trBad.Exec(context.Background(), target, containerCred(), "echo hello"); err == nil {
		t.Fatal("expected Exec to be rejected against a known_hosts file with a forged host key")
	}
}

// TestSSHContainer_StoppedContainerRetriesThenBreakerOpens proves,
// against a real, deliberately severed TCP connection (not simulated),
// that a subsequent Exec call retries per MaxRetries/backoff and then
// fails cleanly once retries are exhausted, AND that the circuit
// breaker opens: a following Exec call fails fast, well under the
// budget the retried failure took, proving the breaker actually
// short-circuited rather than dialing again.
//
// This test starts and stops its OWN dedicated container rather than
// the package's shared one: stopping the shared container would break
// every other subtest that still needs it.
func TestSSHContainer_StoppedContainerRetriesThenBreakerOpens(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH container integration test in short mode")
	}
	defer verifyNoLeaks(t)

	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "lscr.io/linuxserver/openssh-server:latest",
		ExposedPorts: []string{"2222/tcp"},
		Env: map[string]string{
			"PUID":            "1000",
			"PGID":            "1000",
			"PASSWORD_ACCESS": "true",
			"USER_NAME":       containerSSHUser,
			"USER_PASSWORD":   containerSSHPassword,
		},
		WaitingFor: wait.ForLog("done.").WithStartupTimeout(3 * time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start dedicated openssh-server container: %v", err)
	}
	defer container.Terminate(ctx) // idempotent alongside the deliberate Stop below

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get container host: %v", err)
	}
	mapped, err := container.MappedPort(ctx, "2222/tcp")
	if err != nil {
		t.Fatalf("failed to get mapped port: %v", err)
	}
	target := transport.Target{Host: host, Port: int(mapped.Num())}

	tr := New(Options{
		InsecureSkipHostKeyVerify: true,
		DialTimeout:               2 * time.Second,
		MaxRetries:                3,
		BreakerThreshold:          3,
		BreakerCooldown:           time.Minute,
	})

	// Sanity check: prove the container genuinely works before severing
	// it, so the failure proven below is caused by the stop, not a
	// pre-existing misconfiguration.
	if _, err := tr.Exec(ctx, target, containerCred(), "echo hello"); err != nil {
		t.Fatalf("expected the running container to succeed before being stopped: %v", err)
	}

	// Real severed TCP: stop the container out from under the next Exec
	// call, no simulation involved.
	if err := container.Stop(ctx, nil); err != nil {
		t.Fatalf("failed to stop container: %v", err)
	}

	start := time.Now()
	_, err = tr.Exec(ctx, target, containerCred(), "echo hello")
	retriedElapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected Exec against a stopped container to fail once retries are exhausted")
	}
	t.Logf("post-stop Exec failed after %v (MaxRetries=3 with backoff)", retriedElapsed)

	// BreakerThreshold == MaxRetries == 3, so the breaker should now be
	// open. A following call must fail fast: no dial, no backoff wait,
	// well under the time the retried failure above took.
	start = time.Now()
	_, err = tr.Exec(ctx, target, containerCred(), "echo hello")
	fastElapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected the open breaker to reject this call")
	}
	if !strings.Contains(err.Error(), "circuit open") {
		t.Errorf("expected a clear circuit-open error, got: %v", err)
	}
	if fastElapsed >= retriedElapsed {
		t.Errorf("expected the breaker fast-fail (%v) to be faster than the retried failure (%v)", fastElapsed, retriedElapsed)
	}
	if fastElapsed > 250*time.Millisecond {
		t.Errorf("expected the breaker to fail fast with no dial attempted, took %v", fastElapsed)
	}
}
