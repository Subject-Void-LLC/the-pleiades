// The proof that any number of controllers can start against one certificate
// directory at the same instant and every one of them serves.
//
// This is FAILURE_PATTERNS.md #128 and its successor as tests. The first
// version of Ensure looped "load, generate" a fixed three times and then
// returned a fatal error WITHOUT loading again, so a directory contended by
// four starting controllers produced four processes that refused to boot. The
// second version serialized the writers behind an exclusive claim, which
// moved the refusal rather than removing it: a claim holder that was killed,
// or merely slow, made every other controller in the directory wait and then
// give up.
//
// What this design promises is deliberately weaker in one place and much
// stronger everywhere else, and the tests below are written against exactly
// that promise:
//
//   - Every racer starts. None of them waits for another, so none of them can
//     be made to fail by another one being slow or dead.
//   - Every racer serves a usable pair, in memory, that no later writer can
//     invalidate.
//   - The directory CONVERGES: whatever is published at the end is a valid
//     bundle, and every controller that starts afterwards adopts it and
//     writes nothing.
//   - It does NOT promise that all racers present the same certificate at the
//     same instant. A racer whose re-read lands before a later racer's rename
//     serves the one it read. See Ensure's own doc comment for why that is
//     the right trade against a lock.
//
// A single process with many goroutines is the honest test here, not a weaker
// stand-in for many processes: the coordination is rename(2) against a shared
// directory and there is no in-process lock anywhere in this package, so
// goroutines contend for exactly the same primitive separate processes do.
// TestEnsureAcrossRealProcesses runs the same race across real processes as
// the control that says so.
package tlscert_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// racerCounts is how many controllers start at once, run as separate
// subtests.
//
// Sixteen is far more than any plausible replica count, and it is here
// because the failures this covers get likelier with contention: a test that
// only reproduces them sometimes is a test that gets deleted as flaky.
var racerCounts = []int{2, 4, 8, 16}

// racers is the count the benchmark and the cross-process test use, kept as
// one number so both describe the same amount of contention.
const racers = 8

// startTogether runs Ensure in n goroutines released at the same instant and
// returns what each one got.
//
// The barrier is the point. Without it the scheduler tends to run the
// goroutines one after another, which is the case that never failed even
// before any of this was fixed.
func startTogether(dir string, n int, opts tlscert.Options) ([]tlscert.ServingCert, []error) {
	certs := make([]tlscert.ServingCert, n)
	errs := make([]error, n)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			certs[i], errs[i] = tlscert.Ensure(dir, opts)
		}(i)
	}
	close(start)
	wg.Wait()
	return certs, errs
}

// TestEnsureUnderConcurrentStartups is the whole promise: every racer starts,
// every racer can serve, and the directory converges.
func TestEnsureUnderConcurrentStartups(t *testing.T) {
	for _, n := range racerCounts {
		t.Run(strings.Join([]string{"racers", itoa(n)}, "-"), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tls")

			certs, errs := startTogether(dir, n, tlscert.Options{})
			for i, err := range errs {
				if err != nil {
					t.Errorf("racer %d refused to start: %v", i, err)
				}
			}
			if t.Failed() {
				t.FailNow()
			}

			for i, cert := range certs {
				// The pair each racer will actually serve, held in memory. The
				// original defect handed back a pair verified moments before
				// another racer replaced the files, and the listener then died
				// on the re-read; serving parsed material is what closes that.
				if len(cert.Pair.Certificate) == 0 || cert.Pair.PrivateKey == nil {
					t.Errorf("racer %d got no parsed pair to serve, so its listener has to re-read the files and can lose the race again", i)
					continue
				}
				if problem := tlscert.ValidityProblem(cert.Leaf); problem != "" {
					t.Errorf("racer %d is serving a certificate that is not usable: %s", i, problem)
				}
				if !cert.SelfProvisioned {
					t.Errorf("racer %d disowned a certificate this package wrote", i)
				}
			}

			// What is on disk is loadable, so a restart of any of them works.
			assertDirectoryConverged(t, dir, n)
		})
	}
}

// assertDirectoryConverged proves the state left behind is a single usable
// certificate that every later start adopts without writing anything.
//
// This is the property that replaces "exactly one racer generated". Several
// racers writing is fine and costs a few milliseconds each; a directory that
// never settles is not, because it would mean every restart forever asks
// operators to trust a new certificate.
func assertDirectoryConverged(t *testing.T, dir string, n int) {
	t.Helper()

	bundle := filepath.Join(dir, tlscert.BundleFileName)
	published, err := tls.LoadX509KeyPair(bundle, bundle)
	if err != nil {
		t.Fatalf("the serving bundle left in %s does not load: %v", dir, err)
	}

	// A second round, started together exactly like the first. Every one of
	// them must reuse, and all of them must agree, because there is now
	// something servable to agree on.
	certs, errs := startTogether(dir, n, tlscert.Options{})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d refused to start against a directory that already has a certificate: %v", i, err)
		}
		if certs[i].Generated {
			t.Errorf("racer %d wrote a new certificate over a perfectly good one, which is the churn loop this design has to not have", i)
		}
		if !bytes.Equal(certs[i].Leaf.Raw, published.Certificate[0]) {
			t.Errorf("racer %d did not converge on the published certificate", i)
		}
	}

	// And nothing moved on disk in that second round.
	after, err := tls.LoadX509KeyPair(bundle, bundle)
	if err != nil {
		t.Fatalf("the serving bundle stopped loading after a second round of starts: %v", err)
	}
	if !bytes.Equal(after.Certificate[0], published.Certificate[0]) {
		t.Error("a round of starts replaced a certificate that was already good")
	}
}

// TestEnsureConvergesAfterARenewalStorm is defect 4: every replica renewing
// at the same instant.
//
// The old design split the fleet in a way that healed nowhere, because the
// slow claim holder published after everyone else had given up waiting. What
// has to hold now is that a renewal storm ends with one certificate published,
// that it is newer than the one it replaced, and that the NEXT round of starts
// renews nothing at all. A renewal that leaves the directory due for renewal
// again is a churn loop, and it would rewrite the certificate on every start
// forever.
func TestEnsureConvergesAfterARenewalStorm(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")

	// A certificate deep inside its renewal window, written the way a real one
	// gets there: an hour of life against the default thirty-day window.
	stale, err := tlscert.Ensure(dir, tlscert.Options{TTL: time.Hour, RenewBefore: time.Minute})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	certs, errs := startTogether(dir, racers, tlscert.Options{})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d refused to start during a renewal: %v", i, err)
		}
		if certs[i].Leaf.Equal(stale.Leaf) {
			t.Errorf("racer %d kept serving a certificate inside its renewal window", i)
		}
		if !certs[i].Leaf.NotAfter.After(stale.Leaf.NotAfter) {
			t.Errorf("racer %d renewed onto a certificate that expires no later than the one it replaced", i)
		}
	}

	assertDirectoryConverged(t, dir, racers)
}

// TestEnsureSurvivesARacerKilledMidWrite is defect 1 without the claim to
// leak: a controller killed at the worst possible moment must cost the
// directory nothing.
//
// The old design's answer was a two-minute staleness window during which
// every other controller refused to start. This design has nothing for the
// killed process to be holding, so the only thing it can leave behind is a
// temporary file, and the only correct outcome is that the survivors do not
// notice.
func TestEnsureSurvivesARacerKilledMidWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGKILL is not available on Windows")
	}

	self, err := os.Executable()
	if err != nil {
		t.Skipf("this platform cannot name the running test binary: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "tls")

	// A child that provisions into the same directory in a tight loop, killed
	// while it is in there. Killing a child mid-write is the only way to
	// produce the state a SIGKILLed controller leaves, since nothing this
	// process can do to itself is as abrupt.
	// #nosec G204 -- the command is this test binary's own path from
	// os.Executable and a fixed flag; nothing here comes from input.
	victim := exec.Command(self, "-test.run=TestEnsureRacerChild", "-test.v")
	victim.Env = append(os.Environ(), "RUN_ENSURE_RACER="+dir, "RUN_ENSURE_RACER_LOOP=1")
	if err := victim.Start(); err != nil {
		t.Fatalf("starting the victim: %v", err)
	}
	// Long enough for the child to be somewhere inside a write, short enough
	// that the test stays quick. The assertions below do not depend on where
	// exactly it was killed, only that the survivors are unaffected.
	time.Sleep(120 * time.Millisecond)
	if err := victim.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("killing the victim: %v", err)
	}
	_ = victim.Wait()

	certs, errs := startTogether(dir, racers, tlscert.Options{})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d refused to start after another process was killed mid-write: %v", i, err)
		}
		if problem := tlscert.ValidityProblem(certs[i].Leaf); problem != "" {
			t.Errorf("racer %d is serving an unusable certificate after a kill: %s", i, problem)
		}
	}
	assertDirectoryConverged(t, dir, racers)
}

// TestEnsureAcrossRealProcesses runs the race between real operating system
// processes.
//
// It exists because the coordination is a filesystem primitive, and a reviewer
// is entitled to ask whether the goroutine tests above are exercising it or
// are passing on something incidental to sharing an address space. Separate
// processes share no memory at all, so a pass here can only come from the
// rename.
//
// It re-executes this test binary, which is the cheapest real process this
// package can start: RUN_ENSURE_RACER is what tells a child to be a racer
// instead of running the suite.
func TestEnsureAcrossRealProcesses(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")

	self, err := os.Executable()
	if err != nil {
		t.Skipf("this platform cannot name the running test binary: %v", err)
	}

	var wg sync.WaitGroup
	outputs := make([]string, racers)
	failures := make([]error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// #nosec G204 -- the command is this test binary's own path from
			// os.Executable and a fixed flag; nothing here comes from input.
			// -test.v because the child reports its fingerprint through
			// t.Logf, which go test only prints in verbose mode.
			cmd := exec.Command(self, "-test.run=TestEnsureRacerChild", "-test.v")
			cmd.Env = append(os.Environ(), "RUN_ENSURE_RACER="+dir)
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			<-start
			failures[i] = cmd.Run()
			outputs[i] = out.String()
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range failures {
		if err != nil {
			t.Errorf("racer process %d refused to start (%v):\n%s", i, err, outputs[i])
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	// Every fingerprint a child reported has to be one this deployment
	// provisioned, or a replica would fail its own healthcheck. Divergence
	// between children is allowed (see this file's header); a certificate no
	// record covers is not.
	anchors, err := tlscert.AnchorsForDir(dir)
	if err != nil {
		t.Fatalf("AnchorsForDir: %v", err)
	}
	for i, output := range outputs {
		leaf := certificateFrom(t, fingerprintFrom(t, output))
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: anchors.Roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			t.Errorf("racer process %d serves a certificate this deployment's own healthcheck would reject: %v", i, err)
		}
	}

	// And one more start, after the storm, converges on the published bundle.
	assertDirectoryConverged(t, dir, 2)
}

// TestEnsureRacerChild is one racer process, and does nothing at all when it
// is not being used as one.
//
// A test function rather than a TestMain hook, so that running the package
// normally never touches it and `go test -run` still behaves.
func TestEnsureRacerChild(t *testing.T) {
	dir := os.Getenv("RUN_ENSURE_RACER")
	if dir == "" {
		t.Skip("not a racer child; see TestEnsureAcrossRealProcesses")
	}

	if os.Getenv("RUN_ENSURE_RACER_LOOP") != "" {
		// The victim of TestEnsureSurvivesARacerKilledMidWrite: it provisions
		// over and over into a fresh directory each time so that it is always
		// mid-write, and waits to be killed.
		for i := 0; ; i++ {
			if _, err := tlscert.Ensure(filepath.Join(dir, "victim", strconv.Itoa(i)), tlscert.Options{}); err != nil {
				t.Fatalf("Ensure: %v", err)
			}
			if _, err := tlscert.Ensure(dir, tlscert.Options{ExtraNames: []string{"racer" + strconv.Itoa(i) + ".example.test"}}); err != nil {
				t.Fatalf("Ensure: %v", err)
			}
		}
	}

	cert, err := tlscert.Ensure(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// Printed rather than asserted, because the assertion belongs in the
	// parent: what matters is what the whole set of children reports.
	t.Logf("fingerprint=%x", cert.Leaf.Raw)
}

// fingerprintFrom pulls the child's reported certificate out of its output.
func fingerprintFrom(t *testing.T, output string) string {
	t.Helper()
	const marker = "fingerprint="
	index := strings.Index(output, marker)
	if index < 0 {
		t.Fatalf("a racer child reported no fingerprint:\n%s", output)
	}
	rest := output[index+len(marker):]
	if end := strings.IndexAny(rest, "\r\n"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

// certificateFrom turns a child's hex-encoded DER back into a certificate.
func certificateFrom(t *testing.T, hexDER string) *x509.Certificate {
	t.Helper()
	der, err := hex.DecodeString(hexDER)
	if err != nil {
		t.Fatalf("a racer child reported an unreadable fingerprint: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("a racer child reported bytes that are not a certificate: %v", err)
	}
	return cert
}

// itoa is strconv.Itoa under a shorter name, used only to build subtest names.
func itoa(n int) string { return strconv.Itoa(n) }
