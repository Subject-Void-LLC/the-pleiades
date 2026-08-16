// Many controllers, one certificate directory, run as real processes
// through the real start-up path.
//
// internal/tlscert has its own concurrency tests, and they are not enough on
// their own: they prove the package converges, while what an operator cares
// about is that every CONTROLLER reaches a serving state. That is a longer
// path (resolveTLS decides, prepareServingCertificate provisions, a listener
// serves the material it returned, and the container healthcheck verifies
// that listener against this deployment's own trust anchors), and every one
// of those steps used to be able to fail for a reason the certificate code
// alone would have called success.
//
// The three failures being guarded against, all of which shipped at some
// point in this feature's history:
//
//  1. A controller refusing to start because another one held, or had died
//     holding, the right to write. Nothing here waits for anything now, so
//     the test simply demands that every process exits zero.
//  2. A controller reporting itself up and then dying on
//     "tls: private key does not match public key", because it verified one
//     pair and the listener re-read another. Every child below serves the
//     material prepareServingCertificate handed it and completes a real
//     handshake against itself.
//  3. A controller failing its own healthcheck after a sibling renewed,
//     which an orchestrator answers by killing a process that was serving
//     perfectly. Every child runs the shipped healthcheck subcommand against
//     its own listener.
package main

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// racerChildVar names the environment variable that turns a re-executed test
// binary into one starting controller.
//
// A child rather than a goroutine because the claim being tested is about
// PROCESSES sharing a directory: goroutines share an address space, and a
// reviewer is entitled to ask whether a pass came from that instead of from
// the filesystem.
const racerChildVar = "RUN_CONTROLLER_CERT_RACER"

// racerLoopVar tells a child to keep provisioning until it is killed, which
// is how the SIGKILL case gets a process that is reliably mid-write.
const racerLoopVar = "RUN_CONTROLLER_CERT_RACER_LOOP"

// racerHoldVar names a directory two processes use to take turns, which is
// how a child gets to STAY UP while its parent renews underneath it.
//
// Files rather than pipes or signals: the child is a separate process started
// by exec, both ends already share a filesystem, and a marker file is the one
// mechanism that needs nothing plumbed through the test binary's flags.
const racerHoldVar = "RUN_CONTROLLER_CERT_RACER_HOLD"

// TestManyControllersShareOneCertificateDirectory is the release gate for the
// shared-directory case: 2, 4 and 16 controllers starting at the same instant,
// each of them a real process running the real start-up path, repeated so a
// pass is not one lucky interleaving.
func TestManyControllersShareOneCertificateDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("starts up to sixteen processes per round")
	}

	for _, replicas := range []int{2, 4, 16} {
		t.Run(fmt.Sprintf("%d-replicas", replicas), func(t *testing.T) {
			// Three rounds against a FRESH directory each time, because the
			// contended case is the cold start: after the first certificate
			// exists there is nothing left to race over.
			for round := 0; round < 3; round++ {
				dir := filepath.Join(t.TempDir(), "tls")
				results := startControllers(t, dir, replicas)
				for i, result := range results {
					if result.err != nil {
						t.Fatalf("round %d, controller %d did not reach a serving state (%v):\n%s",
							round, i, result.err, result.output)
					}
				}

				// And a controller starting afterwards adopts what they left
				// rather than writing again, which is what "converges" means
				// on disk.
				after := startControllers(t, dir, 1)
				if after[0].err != nil {
					t.Fatalf("round %d, a controller starting after the storm failed (%v):\n%s",
						round, after[0].err, after[0].output)
				}
				if !strings.Contains(after[0].output, `"provisioning":"reused"`) {
					t.Errorf("round %d, a controller starting after the storm provisioned a new certificate instead of reusing the published one:\n%s",
						round, after[0].output)
				}
			}
		})
	}
}

// TestControllersSurviveASiblingKilledMidWrite is the SIGKILL case.
//
// The design this replaced took an exclusive claim on the right to write, so
// a process killed while holding it left every other controller in that
// directory refusing to start until the claim went stale two minutes later.
// There is nothing to hold now, so a killed sibling must cost the survivors
// nothing at all.
func TestControllersSurviveASiblingKilledMidWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("starts several processes")
	}

	dir := filepath.Join(t.TempDir(), "tls")
	self, err := os.Executable()
	if err != nil {
		t.Skipf("this platform cannot name the running test binary: %v", err)
	}

	// #nosec G204 -- the command is this test binary's own path from
	// os.Executable and a fixed flag; nothing here comes from input.
	victim := exec.Command(self, "-test.run=TestControllerCertRacerChild", "-test.v")
	victim.Env = append(controllerRacerEnv(dir), racerLoopVar+"=1")
	if err := victim.Start(); err != nil {
		t.Fatalf("starting the victim: %v", err)
	}
	// Long enough to be somewhere inside a provisioning cycle, short enough
	// to keep the test quick. Nothing below depends on exactly where it was.
	time.Sleep(200 * time.Millisecond)
	if err := victim.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("killing the victim: %v", err)
	}
	_ = victim.Wait()

	for i, result := range startControllers(t, dir, 4) {
		if result.err != nil {
			t.Fatalf("controller %d refused to start after a sibling was killed mid-write (%v):\n%s",
				i, result.err, result.output)
		}
	}
}

// TestControllersServeThroughARenewalStorm is the renewal case: every replica
// decides at the same instant that the stored certificate is due for
// replacement.
//
// Two things have to hold. Every controller reaches a serving state, and
// every one of them passes its OWN healthcheck, which is the half that used
// to be silently broken: a straggler serving a certificate the directory had
// moved past would fail its probe and be restarted by the orchestrator while
// serving perfectly.
func TestControllersServeThroughARenewalStorm(t *testing.T) {
	if testing.Short() {
		t.Skip("starts several processes")
	}

	dir := filepath.Join(t.TempDir(), "tls")

	// A first certificate, then a storm of controllers that all want to
	// replace it because it does not carry a name they were configured with.
	// A required name is how a real renewal is forced without waiting thirty
	// days for one.
	if first := startControllers(t, dir, 1); first[0].err != nil {
		t.Fatalf("the first controller failed (%v):\n%s", first[0].err, first[0].output)
	}

	renewing := startControllersWith(t, dir, 8, "PLEIADES_TLS_AUTOCERT_HOSTS=controller.example.test")
	for i, result := range renewing {
		if result.err != nil {
			t.Fatalf("controller %d failed during a renewal storm (%v):\n%s", i, result.err, result.output)
		}
	}

	// And the storm settles: the next start reuses, which is the difference
	// between a renewal and a churn loop.
	after := startControllersWith(t, dir, 1, "PLEIADES_TLS_AUTOCERT_HOSTS=controller.example.test")
	if after[0].err != nil {
		t.Fatalf("a controller starting after the renewal storm failed (%v):\n%s", after[0].err, after[0].output)
	}
	if !strings.Contains(after[0].output, `"provisioning":"reused"`) {
		t.Errorf("a renewal left the directory still due for renewal, which rewrites the certificate on every start forever:\n%s", after[0].output)
	}
}

// TestARunningControllerSurvivesASiblingsRenewal is the case the other three
// cannot reach, because every child in them probes itself and exits.
//
// A real replica keeps running. It serves the material it loaded at start-up,
// from memory, for as long as its process lives, while the directory
// underneath it moves on: a sibling renews, publishes a different
// certificate, and rewrites the copy beside it. The probe this binary ships
// builds its trust anchors out of that directory, so the anchor set has to
// admit what this replica is legitimately still presenting. When it did not,
// a perfectly healthy controller reported itself unhealthy on every probe for
// the rest of its life, and an orchestrator answers that by killing it.
//
// The child below provisions, serves, passes its probe, waits while its
// parent runs two renewing siblings, and then probes AGAIN. The second probe
// is the new one, and it is an end-to-end guard rather than the discriminator
// for any single fix: the two mechanisms that make it hold (every replica
// records what it is about to serve, and no record is evicted while its
// certificate is still valid) are each pinned in internal/tlscert against the
// behavior they replaced. What this proves is the property itself, through
// the shipped binary, the shipped subcommand and two real processes.
func TestARunningControllerSurvivesASiblingsRenewal(t *testing.T) {
	if testing.Short() {
		t.Skip("starts several processes")
	}

	dir := filepath.Join(t.TempDir(), "tls")
	hold := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Skipf("this platform cannot name the running test binary: %v", err)
	}

	// #nosec G204 -- the command is this test binary's own path from
	// os.Executable and a fixed flag; nothing here comes from input.
	child := exec.Command(self, "-test.run=TestControllerCertRacerChild", "-test.v")
	child.Env = append(controllerRacerEnv(dir), racerHoldVar+"="+hold)
	var childOutput strings.Builder
	child.Stdout = &childOutput
	child.Stderr = &childOutput
	if err := child.Start(); err != nil {
		t.Fatalf("starting the long-lived controller: %v", err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})

	waitForMarker(t, filepath.Join(hold, "serving"), 60*time.Second)

	// Two renewals, so the directory is certainly two certificates past the
	// one the child is holding in memory.
	for i, name := range []string{"first.example.test", "second.example.test"} {
		results := startControllersWith(t, dir, 1, "PLEIADES_TLS_AUTOCERT_HOSTS="+name)
		if results[0].err != nil {
			t.Fatalf("renewing sibling %d failed (%v):\n%s", i, results[0].err, results[0].output)
		}
	}

	if err := os.WriteFile(filepath.Join(hold, "renewed"), nil, 0o600); err != nil {
		t.Fatalf("releasing the long-lived controller: %v", err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("a controller that was serving perfectly failed its own healthcheck after a sibling renewed (%v):\n%s",
			err, childOutput.String())
	}
}

// waitForMarker blocks until path exists, and fails the test rather than
// hanging forever if it never does.
func waitForMarker(t *testing.T, path string, within time.Duration) {
	t.Helper()

	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared, so the two processes never got to take turns", path)
}

// racerResult is one child controller's outcome.
type racerResult struct {
	err    error
	output string
}

// startControllers runs n real controller start-ups against dir, released at
// the same instant.
func startControllers(t *testing.T, dir string, n int) []racerResult {
	t.Helper()
	return startControllersWith(t, dir, n)
}

// startControllersWith is startControllers with extra environment for each
// child, which is how a renewal is provoked.
func startControllersWith(t *testing.T, dir string, n int, extra ...string) []racerResult {
	t.Helper()

	self, err := os.Executable()
	if err != nil {
		t.Skipf("this platform cannot name the running test binary: %v", err)
	}

	results := make([]racerResult, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// #nosec G204 -- the command is this test binary's own path from
			// os.Executable and a fixed flag; nothing here comes from input.
			cmd := exec.Command(self, "-test.run=TestControllerCertRacerChild", "-test.v")
			cmd.Env = append(controllerRacerEnv(dir), extra...)
			<-start
			out, err := cmd.CombinedOutput()
			results[i] = racerResult{err: err, output: string(out)}
		}(i)
	}
	close(start)
	wg.Wait()
	return results
}

// controllerRacerEnv is the environment a child controller starts with: this
// process's own, with every TLS variable cleared and the shared directory
// named, which is the compose stack's configuration exactly.
func controllerRacerEnv(dir string) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		switch {
		case strings.HasPrefix(entry, "TLS_CERT_FILE="),
			strings.HasPrefix(entry, "TLS_KEY_FILE="),
			strings.HasPrefix(entry, "PLEIADES_TLS_"),
			strings.HasPrefix(entry, "LISTEN_ADDR="),
			strings.HasPrefix(entry, racerChildVar+"="),
			strings.HasPrefix(entry, racerLoopVar+"="),
			// Cleared for the same reason as the other two: a renewing
			// sibling started by a held child's parent must not inherit the
			// hold and start waiting for a marker of its own.
			strings.HasPrefix(entry, racerHoldVar+"="):
			continue
		}
		env = append(env, entry)
	}
	return append(env, racerChildVar+"="+dir)
}

// TestControllerCertRacerChild is one starting controller, and does nothing
// at all when it is not being used as one.
//
// It runs the shipped path in the shipped order: resolve, provision, serve
// the returned material, then probe itself with the same subcommand the
// container healthcheck runs. Anything less would prove the certificate code
// converges without proving a controller ever answers a request.
func TestControllerCertRacerChild(t *testing.T) {
	dir := os.Getenv(racerChildVar)
	if dir == "" {
		t.Skip("not a racer child; see TestManyControllersShareOneCertificateDirectory")
	}
	t.Setenv("PLEIADES_TLS_AUTOCERT_DIR", dir)

	if os.Getenv(racerLoopVar) != "" {
		// The victim of TestControllersSurviveASiblingKilledMidWrite: it
		// provisions over and over so that a kill lands inside a write, and
		// waits to be killed.
		for i := 0; ; i++ {
			t.Setenv("PLEIADES_TLS_AUTOCERT_HOSTS", fmt.Sprintf("racer%d.example.test", i))
			cfg, err := resolveTLS()
			if err != nil {
				t.Fatalf("resolveTLS(): %v", err)
			}
			if _, err := prepareServingCertificate(cfg, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
				t.Fatalf("prepareServingCertificate: %v", err)
			}
		}
	}

	cfg, err := resolveTLS()
	if err != nil {
		t.Fatalf("resolveTLS(): %v", err)
	}
	if cfg.Mode != tlsModeSelfProvisioned {
		t.Fatalf("resolveTLS() mode = %v, want tlsModeSelfProvisioned", cfg.Mode)
	}

	// The real provisioning step, logging the way main() does, so the parent
	// can read "generated" or "reused" out of the child's output.
	logged := &strings.Builder{}
	pair, err := prepareServingCertificate(cfg, slog.New(slog.NewJSONHandler(logged, nil)))
	if err != nil {
		t.Fatalf("prepareServingCertificate: %v", err)
	}
	t.Log(logged.String())
	if pair == nil || len(pair.Certificate) == 0 {
		t.Fatal("no certificate material came back for the listener to serve")
	}

	// A real listener serving exactly that material, and a real handshake
	// against it through the shipped healthcheck.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on loopback: %v", err)
	}
	srv := &http.Server{
		Handler:           newReadinessRouter(t, func(context.Context) error { return nil }),
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{*pair},
		},
	}
	go func() { _ = srv.ServeTLS(listener, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })

	t.Setenv("LISTEN_ADDR", listener.Addr().String())
	if code := runHealthcheck([]string{healthcheckCommand}); code != 0 {
		t.Fatalf("this controller's own healthcheck against its own listener = %d, want 0", code)
	}

	t.Logf("fingerprint=%s", hex.EncodeToString(pair.Certificate[0]))

	// The long-lived case: stay up, still serving the material loaded above,
	// while the parent renews the directory underneath, then probe again.
	// See TestARunningControllerSurvivesASiblingsRenewal.
	if hold := os.Getenv(racerHoldVar); hold != "" {
		if err := os.WriteFile(filepath.Join(hold, "serving"), nil, 0o600); err != nil {
			t.Fatalf("announcing that this controller is serving: %v", err)
		}
		waitForMarker(t, filepath.Join(hold, "renewed"), 60*time.Second)
		if code := runHealthcheck([]string{healthcheckCommand}); code != 0 {
			t.Fatalf("this controller's own healthcheck failed after a sibling renewed = %d, want 0; an orchestrator answers that by killing a process that is serving perfectly", code)
		}
	}
}
