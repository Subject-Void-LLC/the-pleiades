//go:build integration

// This file stands up the whole mesh as real processes against real
// infrastructure, which is what makes the Grand Integration Test a
// integration test rather than a controller test with a helper goroutine.
//
// Nothing here is a stand-in. The controller and the runner are the real
// built binaries, executed as real operating system processes and
// configured exactly the way a deployment configures them, through
// environment variables. The database is a real PostgreSQL server and the
// broker is a real NATS server, both in containers. The only thing the
// test process does itself is seed inventory, hold the observer
// subscriptions, and speak HTTP to the controller over a real socket.
//
// Every wait in this file names the thing it is waiting for. There is no
// sleep used as a synchronization primitive anywhere, because a fixed
// sleep either wastes time once the work has finished or, worse, is too
// short under CI load and fails a test despite correct behavior.
//
// One sharp edge, worth knowing before it wastes an afternoon. This
// package reaches cmd/controller and cmd/runner by building them as
// subprocesses, not by importing them, so Go's test cache sees no
// dependency edge to either binary's source. Editing the controller and
// re-running this test will happily replay a cached PASS from before the
// edit. Always pass -count=1 when changing anything the two binaries
// compile from:
//
//	go test -tags integration -count=1 ./tests/e2e/
//
// make test-integration does this for the same reason.
package e2e

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/native"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"go.uber.org/goleak"
)

// Fixed, non-secret configuration shared by the test process and the
// subprocesses it launches.
//
// The encryption key and the JWT secret are deliberately single constants
// rather than per-call values. The seeder and the controller must agree
// on the encryption key byte for byte, and if they ever drifted the
// symptom would be badly misleading: every device would be recorded
// skipped with "has no host property", which reads like a group targeting
// bug rather than a key mismatch. One constant makes that drift
// impossible.
const (
	// harnessJWTSecret is the HS256 signing secret. It must be at least
	// 32 bytes for auth.NewStaticKeyProvider, and it must be a plain
	// string because the controller reads it back as
	// []byte(os.Getenv("JWT_SECRET")). Not a real credential.
	harnessJWTSecret = "pleiades-e2e-signing-secret-not-a-real-credential"

	// harnessMasterKey is base64 of exactly 32 bytes, which is what
	// MASTER_ENCRYPTION_KEY requires. Not a real credential.
	harnessMasterKey = "a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s="

	// The issuer and audience the controller pins tokens against. These
	// are the controller's own documented defaults, used explicitly so
	// the test cannot pass by accident if a default changes.
	harnessJWTIssuer   = "pleiades-controller"
	harnessJWTAudience = "pleiades-api"

	// harnessRunbookID is the runbook every dispatch in this package
	// names.
	harnessRunbookID = "ping"

	// harnessPlaybookID names the playbook the withAnsible harness seeds
	// and its gate launches. A project-relative PATH in a subdirectory,
	// deliberately: that is the shape a production Ascender template
	// stores ("tripplite_python/tripplite_config.yml"), and a fixture at
	// the tree root would pass against a resolver that cannot descend.
	harnessPlaybookID = "network_automation/hello_mesh.yml"

	// harnessPlaybookTaskName is the task the fixture playbook runs,
	// asserted against the job's log events: seeing it there proves a
	// real ansible-playbook parsed and executed this exact file inside
	// the legacy adapter's sandbox, through the production binaries.
	harnessPlaybookTaskName = "prove the legacy adapter ran ansible"
)

// Binaries built once per test binary by TestMain.
var (
	controllerBinPath string
	runnerBinPath     string

	// harnessCert is the serving certificate every controller this package
	// starts presents, and the trust pool every client in it dials with.
	//
	// One per test binary rather than one per harness, and that is load
	// bearing rather than a saving: TestUI_SessionCookieWorksAcrossControllers
	// starts a SECOND controller and reuses the first one's client against
	// it, which can only verify if both processes present the same
	// certificate.
	harnessCert testsupport.ServingCert
)

// TestMain builds the two real binaries this package drives, once, and
// removes them afterwards.
//
// The re-exec guard comes first, before anything else. The runbook
// fixture here uses the builtin "noop" action, which never spawns a
// Collection subprocess, so strictly this is unnecessary today. It costs
// two lines and it is the difference between a future fixture that names
// a real Collection method working, and this test binary silently
// re-running its own entire suite as a child process.
//
// The binaries are built without -race deliberately. They are separate
// processes, so the race detector in this test binary cannot see into
// them either way, and instrumenting them would roughly double an
// already slow build. What that costs is stated plainly: this test proves
// the mesh's behavior, not the subprocesses' internal race freedom, which
// their own packages' -race tests cover.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == native.InternalCollectionRunnerArg {
		os.Exit(native.RunCollectionChild(context.Background()))
	}

	tmpDir, err := os.MkdirTemp("", "pleiades-e2e-bin")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating the binary temp directory: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	// Built by import path, not by file, so the working directory does
	// not matter and so a package growing a second file keeps building.
	for _, target := range []struct {
		importPath string
		dest       *string
		name       string
	}{
		{"github.com/Subject-Void-LLC/the-pleiades/cmd/controller", &controllerBinPath, "controller"},
		{"github.com/Subject-Void-LLC/the-pleiades/cmd/runner", &runnerBinPath, "runner"},
	} {
		path := filepath.Join(tmpDir, target.name)
		build := exec.Command("go", "build", "-o", path, target.importPath)
		if out, err := build.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "building %s: %v\n%s\n", target.importPath, err, out)
			os.Exit(1)
		}
		*target.dest = path
	}

	// The certificate the controllers below serve with. Generated here, in
	// the same throwaway directory the binaries live in, because the
	// controller now refuses to serve plain HTTP unless an ingress in front
	// of it terminates TLS, and there is no ingress here.
	cert, err := testsupport.NewServingCert(filepath.Join(tmpDir, "tls"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "generating the harness serving certificate: %v\n", err)
		os.Exit(1)
	}
	harnessCert = cert

	code := m.Run()
	// The previous release's extracted tree and binary, when an upgrade
	// gate built one (previous_build_test.go).
	removePreviousBuilds()
	os.Exit(code)
}

// managedProc is one subprocess plus the goroutine draining its output.
type managedProc struct {
	name string
	cmd  *exec.Cmd

	// done closes once the output drain has finished and the process has
	// been reaped, so a caller can wait for full termination rather than
	// just for the kill signal to be delivered.
	done chan struct{}

	// mu guards lines, which both the drain goroutine and a failing test
	// touch.
	mu    sync.Mutex
	lines []string
}

// harness is one fully wired mesh: two containers, two subprocesses, and
// the test process's own connections to the broker.
type harness struct {
	dsn        string
	natsURL    string
	baseURL    string
	runbookDir string

	// natsCert is the certificate presented by whatever terminates TLS in
	// front of the broker, when a harness put something there, and
	// therefore also the authority every client in that mesh verifies it
	// against: internal/tlscert writes a self-signed certificate with
	// IsCA set, so cert.pem is both the leaf and the root.
	//
	// The zero value is the ordinary case and means "the broker is dialed
	// directly, in plaintext", which is every harness in this package but
	// the wss:// traversal gate. Both seams that read it key off CertFile
	// being empty, so a harness that does not set it produces byte for
	// byte the dial options and the subprocess environment it produced
	// before the field existed.
	natsCert testsupport.ServingCert

	nc *nats.Conn
	js jetstream.JetStream

	// devices is the seeded inventory, carrying the identifiers the
	// database generated, which the wire assertions compare against.
	devices []*seededDevice

	// templateID is the seeded template every launch in this suite runs.
	// A template rather than a group name, because a launch names a saved
	// definition now and that is what gives its job a tenant.
	templateID int

	// emptyTemplateID names an inventory that selects nothing, which is
	// the fail-closed control: it must dispatch to nothing rather than to
	// everything.
	emptyTemplateID int

	// The Ansible half, present only when the harness was started
	// withAnsible: the shared playbook directory, the runner image, and a
	// template whose kind routes to the legacy adapter. Empty otherwise,
	// so the other tests do not pay for a docker build they never use.
	playbookDir        string
	ansibleImage       string
	playbookTemplateID int

	controller *managedProc
	runner     *managedProc
}

// harnessOption configures optional harness surface before any container
// or binary starts.
type harnessOption func(tb testing.TB, h *harness)

// withAnsible equips the mesh to run the playbook kind for real: the
// repository's own ansible-runner image, a playbook directory both
// binaries read (the same two-binary PLAYBOOK_DIR convention RUNBOOK_DIR
// follows), and a seeded playbook template against the same inventory the
// runbook template targets.
func withAnsible() harnessOption {
	return func(tb testing.TB, h *harness) {
		h.ansibleImage = testsupport.BuildAnsibleRunnerImage(tb)
		h.playbookDir = tb.TempDir()
		writePlaybookFixture(tb, h.playbookDir)
	}
}

// startHarness brings up the full mesh and returns it ready to drive.
//
// The ordering is load bearing and is called out at each step.
func startHarness(tb testing.TB, opts ...harnessOption) *harness {
	tb.Helper()

	// Registered first so that, under tb.Cleanup's last-added-first-called
	// order, it runs last: after every subprocess has been signalled,
	// reaped and drained, and after every container has been terminated.
	// A goleak check that ran before those would report their in-flight
	// goroutines as leaks.
	//
	// What this proves is scoped honestly: goleak observes this test
	// process only. It asserts that the connections, pools and scanner
	// goroutines this test itself creates are released. The subprocesses'
	// own goroutine hygiene is a separate claim, carried by their
	// packages' tests and by the fact that both must exit cleanly on
	// SIGTERM here.
	//
	// Tests only, deliberately. Under a benchmark the framework keeps its
	// own goroutine parked in testing.(*B).run1 for the whole run, which
	// goleak correctly reports and which no amount of cleanup here can
	// retire. Leak detection is a claim about the code this harness
	// drives, and the tests already make it against the identical code
	// path, so asserting it a second time from a benchmark would only buy
	// a false failure.
	if t, ok := tb.(*testing.T); ok {
		t.Cleanup(func() { goleak.VerifyNone(t) })
	}

	ctx := context.Background()
	h := &harness{}

	// Options run before the containers so an option that must skip (no
	// docker for the ansible image build) skips before anything is paid
	// for.
	for _, opt := range opts {
		opt(tb, h)
	}

	// 1. The two containers. Started before anything that needs them, and
	// with an explicit startup timeout rather than testcontainers' own
	// default, which is the reason internal/testsupport centralizes these
	// values at all.
	h.dsn = startPostgres(tb, ctx)
	// Only when an option did not already stand one up. The wss://
	// traversal gate starts its own broker behind a terminating proxy,
	// and options run before this line precisely so that it can: the
	// alternative is a second copy of startHarness, which is what
	// startChaosHarness had to write and what cost that file its goleak
	// check and its option loop.
	if h.natsURL == "" {
		h.natsURL = startNATS(tb, ctx)
	}

	h.wire(tb)
	return h
}

// wire is everything startHarness does once it knows where the database
// and the broker are: connect, seed, and launch the two binaries.
//
// It is separate from startHarness so the chaos variant, which reaches
// the same two servers through a pair of Toxiproxy proxies rather than
// directly, can reuse this half unchanged. That matters for the honesty
// of the chaos test: it exercises the identical wiring, differing only in
// the address the subprocesses dial.
func (h *harness) wire(tb testing.TB) {
	tb.Helper()
	ctx := context.Background()

	// 2. The test process's own broker connection, and the single stream
	// every subject in this platform lives under. Created here so the
	// observers below exist before any publisher process is running.
	nc, err := nats.Connect(h.natsURL, h.natsDialOptions()...)
	if err != nil {
		tb.Fatalf("connecting to nats: %v", err)
	}
	tb.Cleanup(nc.Close)
	h.nc = nc

	js, err := jetstream.New(nc)
	if err != nil {
		tb.Fatalf("creating the jetstream context: %v", err)
	}
	h.js = js

	if _, _, err := topology.ProvisionStream(ctx, js, topology.DefaultOutageBudget, false); err != nil {
		tb.Fatalf("ensuring the pleiades stream: %v", err)
	}

	// 3. The shared runbook directory, populated before either binary
	// starts: the controller resolves it at startup and exits if it is
	// missing, and both binaries must see the same content, which is the
	// single-RUNBOOK_DIR convention production uses.
	h.runbookDir = tb.TempDir()
	writeRunbookFixture(tb, h.runbookDir)

	// 4. Seed inventory, through the same open seam and the same
	// versioned migrations the controller itself uses, then close the
	// client before the controller starts.
	h.devices, h.templateID, h.emptyTemplateID = seedInventory(tb, h.dsn)
	if h.playbookDir != "" {
		h.playbookTemplateID = seedPlaybookTemplate(tb, h.dsn)
	}

	// 5. The controller, then the runner.
	h.startController(tb)
	h.startRunner(tb)
}

// startPostgres starts the real database this mesh runs on and returns a
// DSN a host-side process can reach it by.
func startPostgres(tb testing.TB, ctx context.Context) string {
	tb.Helper()
	container, err := testpg.Run(ctx,
		testsupport.PostgresImage,
		testpg.WithDatabase("pleiades"),
		testpg.WithUsername("pleiades"),
		testpg.WithPassword("pleiades"),
		testsupport.PostgresReady(),
	)
	if err != nil {
		tb.Fatalf("starting the postgres container: %v", err)
	}
	tb.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		tb.Fatalf("reading the postgres connection string: %v", err)
	}
	return dsn
}

// startNATS starts the real broker this mesh dispatches over.
func startNATS(tb testing.TB, ctx context.Context) string {
	tb.Helper()
	return testsupport.StartNATS(tb).URL()
}

// startController launches the real controller binary and waits until it
// is genuinely serving.
func (h *harness) startController(tb testing.TB) {
	tb.Helper()

	port := freeTCPPort(tb)
	// https, because the controller terminates TLS itself here exactly as
	// it does in docker-compose.yml. Every client in this package goes
	// through h.httpClient, which trusts harnessCert and nothing else.
	h.baseURL = "https://127.0.0.1:" + strconv.Itoa(port)

	h.controller = startProcess(tb, "controller", controllerBinPath, append([]string{
		"DB_DSN=" + h.dsn,
		"NATS_URL=" + h.natsURL,
		"LISTEN_ADDR=127.0.0.1:" + strconv.Itoa(port),
		"JWT_ISSUER=" + harnessJWTIssuer,
		"JWT_AUDIENCE=" + harnessJWTAudience,
		"JWT_SECRET=" + harnessJWTSecret,
		"MASTER_ENCRYPTION_KEY=" + harnessMasterKey,
		"RUNBOOK_DIR=" + h.runbookDir,
		// Defaulted to ".", the subprocess working directory. A stray
		// credential file there would be resolved onto the wire and would
		// break this package's no-secrets-on-the-wire assertion in a way
		// that looks like a leak rather than like contamination.
		"CONTROLLER_CREDENTIALS_DIR=" + tb.TempDir(),
		// cmd.Env inherits the developer's environment, and telemetry
		// implies the otlp exporter whenever OTEL_EXPORTER_OTLP_ENDPOINT
		// is set, so a local collector configuration must not be able to
		// change what this test exercises.
		"OTEL_TRACES_EXPORTER=none",
		// Real TLS, from the same generator `make dev-cert` and
		// `make ui-dev` use. This replaces PLEIADES_UI_INSECURE_COOKIES=1,
		// whose comment here used to say a __Host- cookie would be unusable
		// against a loopback port and that asserting on the UI would
		// therefore become a test of TLS. Both halves were wrong. Browsers
		// and Go's cookie jar both make an explicit exception for loopback,
		// so the cookie was always usable; and the UI assertions are
		// stronger for running over the transport a deployment actually
		// serves, which is what lets this suite assert Secure and the
		// __Host- prefix on the real cookie rather than around them.
		"TLS_CERT_FILE=" + harnessCert.CertFile,
		"TLS_KEY_FILE=" + harnessCert.KeyFile,
	}, h.subprocessEnv(false)...))

	// /healthz proves the socket is bound. /readyz is the stronger claim
	// and the one worth waiting on: it runs a real NATS connectivity
	// check and a real database query from inside the controller, so a
	// 200 means the controller genuinely reached both containers.
	h.waitForHTTPStatus(tb, "/healthz", 200, "the controller to bind its socket")
	h.waitForHTTPStatus(tb, "/readyz", 200, "the controller to reach postgres and nats")
}

// httpClient returns a client that trusts this harness's controller and
// nothing else.
//
// Every request in this package goes through one of these. A bare
// http.DefaultClient stopped working the moment the controller began
// terminating TLS, and the replacement is deliberately not
// InsecureSkipVerify: a suite that skipped verification would keep passing
// on the day the controller served a certificate it was never configured
// with, which is exactly the class of failure an end-to-end test exists to
// catch.
func (h *harness) httpClient() *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second * raceTimeScale,
		Transport: &http.Transport{TLSClientConfig: harnessCert.TLSClientConfig()},
	}
}

// startRunner launches the real runner binary and waits until it has
// joined the dispatch consumer group.
func (h *harness) startRunner(tb testing.TB) {
	tb.Helper()

	h.runner = startProcess(tb, "runner", runnerBinPath, append([]string{
		"NATS_URL=" + h.natsURL,
		"RUNBOOK_DIR=" + h.runbookDir,
		"RUNNER_WAL_DIR=" + tb.TempDir(),
		"OTEL_TRACES_EXPORTER=none",
	}, h.subprocessEnv(true)...))

	// Readiness is the durable consumer existing, not a log line: that
	// consumer is the exact resource a dispatch has to land on, so
	// waiting for it waits for the thing that actually matters.
	//
	// The dispatch consumer uses DeliverAllPolicy, so a dispatch
	// published before the runner joined would still be delivered. This
	// wait is therefore not needed for correctness; it exists so that a
	// later failure reads as a real failure rather than as a
	// several-second silence.
	deadline := time.Now().Add(30 * time.Second * raceTimeScale)
	for {
		_, err := h.js.Consumer(context.Background(), topology.StreamName, topology.DispatchDurableName)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			tb.Fatalf("the runner did not create the %q consumer in time; last error: %v\n%s",
				topology.DispatchDurableName, err, h.runner.output())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// natsDialOptions is the TLS half of this harness's own broker
// connection, and it is empty unless a harness put something terminating
// TLS in front of the broker.
//
// Empty matters more than it looks: nats.Connect(url) and
// nats.Connect(url, nil...) are the same call, so no existing harness
// changes behavior at all. The alternative considered and rejected was
// routing this through topology.Connect, which would be more
// representative and would ALSO change the connection name, the reconnect
// policy and the post-connect wait for every test in this package at
// once. That is a separate change owing its own evidence.
//
// nats.Secure with a configuration from the generator, rather than a
// tls.Config written here: that generator is the one place this module
// writes one, which is where InsecureSkipVerify would otherwise get
// typed. internal/archtest's TestOnlyTlscertBuildsAMeshTLSConfig enforces
// the same rule for shipping code.
func (h *harness) natsDialOptions() []nats.Option {
	if h.natsCert.CertFile == "" {
		return nil
	}
	return []nats.Option{nats.Secure(h.natsCert.TLSClientConfig())}
}

// subprocessEnv is every optional variable a harness option added, for
// one of the two binaries.
//
// It exists so there is exactly ONE append seam rather than an arbitrary
// one, and that is the property that matters: both binaries must be given
// the same broker configuration, and two independent appends at two call
// sites is how one of them ends up without NATS_CA_FILE while the failure
// reads as a broker problem. playbookEnv keeps its own function because
// its pair travels together for a reason of its own; this composes them.
func (h *harness) subprocessEnv(runner bool) []string {
	return append(h.playbookEnv(runner), h.natsTLSEnv()...)
}

// natsTLSEnv is the broker-TLS half of both binaries' environment.
//
// NATS_CA_FILE names the certificate the proxy presents, which is also
// the root that signed it, because the generator writes a self-signed CA
// and one file is therefore both. Nothing here sets NATS_URL: that is
// already carried by h.natsURL, and this pair is exactly what
// topology.TLSFromEnv refuses when the two contradict each other, which
// the refusal gates in cmd/ assert separately.
func (h *harness) natsTLSEnv() []string {
	if h.natsCert.CertFile == "" {
		return nil
	}
	return []string{"NATS_CA_FILE=" + h.natsCert.CertFile}
}

// playbookEnv is the Ansible half's environment, empty when the harness
// was started without it. Both binaries read PLAYBOOK_DIR (the controller
// for its catalog and fan-out verification, the runner for execution);
// only the runner needs the image, and it refuses the directory without
// the image by design, which is why the pair travels together.
func (h *harness) playbookEnv(runner bool) []string {
	if h.playbookDir == "" {
		return nil
	}
	env := []string{"PLAYBOOK_DIR=" + h.playbookDir}
	if runner {
		env = append(env, "ANSIBLE_RUNNER_IMAGE="+h.ansibleImage)
	}
	return env
}

// startProcess launches one binary with the given environment and drains
// its output into a ring the test can print on failure.
//
// A plain function rather than a method: it never read anything off the
// harness, and a gate that needs one process without the rest of the mesh
// (runner_heartbeat_release_gate_test.go starts a runner and no
// controller) should not have to invent a harness to reach it.
func startProcess(tb testing.TB, name, bin string, env []string) *managedProc {
	tb.Helper()

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), env...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		tb.Fatalf("piping %s stdout: %v", name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		tb.Fatalf("piping %s stderr: %v", name, err)
	}

	proc := &managedProc{name: name, cmd: cmd, done: make(chan struct{})}

	if err := cmd.Start(); err != nil {
		tb.Fatalf("starting %s: %v", name, err)
	}

	// Both streams are drained. The controller installs a JSON slog
	// handler over stdout and calls SetDefault, so its startup failures
	// land there; the runner leaves slog at its default, which writes
	// text to stderr. Draining both means neither binary can fail
	// silently, and it also prevents a full pipe buffer from blocking a
	// subprocess that logs a lot.
	var drained sync.WaitGroup
	drained.Add(2)
	for _, stream := range []io.Reader{stdout, stderr} {
		go func(r io.Reader) {
			defer drained.Done()
			scanner := bufio.NewScanner(r)
			for scanner.Scan() {
				proc.record(scanner.Text())
			}
		}(stream)
	}
	go func() {
		drained.Wait()
		_ = cmd.Wait()
		close(proc.done)
	}()

	// Registered after the container cleanups, so it runs before them:
	// the processes go away first, then the infrastructure they were
	// talking to.
	tb.Cleanup(func() { proc.stop(tb) })
	return proc
}

// record appends one output line, keeping only the most recent ones.
// pid returns the running process's own operating-system id, or zero if it
// has not started or has already been reaped.
//
// It exists for the credential injection gate, which reads the Runner's own
// /proc/<pid>/environ to prove PLAN.md Section 17.5's rule holds for the
// Runner host: Section 29.4's exception permits a secret in the EPHEMERAL
// CONTAINER's environment, never in the long-lived process that started it.
func (p *managedProc) pid() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *managedProc) record(line string) {
	const keep = 60
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lines = append(p.lines, line)
	if len(p.lines) > keep {
		p.lines = p.lines[len(p.lines)-keep:]
	}
}

// output renders the retained lines for a failure message. A readiness
// timeout with no output is close to undebuggable, which is the whole
// reason this ring exists.
func (p *managedProc) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.name + " output:\n"
	for _, line := range p.lines {
		out += "  " + line + "\n"
	}
	return out
}

// stop signals the process to shut down gracefully and waits for it.
//
// SIGTERM rather than Kill, because both binaries have real graceful
// shutdown paths and exercising them is itself worth something: a
// shutdown that hangs or panics is a genuine finding, and Kill would hide
// it. Kill remains the escalation if graceful shutdown does not finish.
func (p *managedProc) stop(tb testing.TB) {
	tb.Helper()
	if p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(os.Interrupt)

	select {
	case <-p.done:
	case <-time.After(20 * time.Second * raceTimeScale):
		tb.Errorf("%s did not shut down within its budget after SIGTERM; killing it\n%s", p.name, p.output())
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

// freeTCPPort returns a port nothing is currently listening on.
//
// This carries the same small, accepted race every other release gate in
// this repository accepts: the port is released before the subprocess
// binds it, so another process could in principle take it in between.
// There is no portable way to hand an already-bound listener to a child
// process, and the alternative, a fixed port, fails far more often.
func freeTCPPort(tb testing.TB) int {
	tb.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("allocating a free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// writeRunbookFixture writes the one runbook every dispatch here names.
//
// The action is the builtin "noop": it needs no capability and no
// transport, so it exercises the full dispatch and execution path without
// also depending on a real device being reachable. Reaching a real device
// over real SSH is a claim cmd/runner's own SSH release gate already
// carries, and duplicating it here would slow this test without proving
// anything new.
//
// The runbook declares no metadata block, so it resolves as
// interruptible, which the dispatch payload assertions rely on.
func writeRunbookFixture(tb testing.TB, dir string) {
	tb.Helper()
	content := "id: " + harnessRunbookID + "\ntasks:\n  - name: step\n    fqcn: noop\n"
	if err := os.WriteFile(filepath.Join(dir, harnessRunbookID+".yaml"), []byte(content), 0o600); err != nil {
		tb.Fatalf("writing the runbook fixture: %v", err)
	}
}

// writePlaybookFixture writes the playbook the withAnsible harness runs.
//
// connection: local, deliberately. The legacy adapter's ansible container
// runs on Docker's own network, while this harness's devices advertise
// loopback host-mapped ports only the test host can reach, so an SSH
// connection from inside that container would be testing Docker's
// topology rather than this platform's wiring. Reaching a real device
// over the network from the real container is cmd/runner's Ansible
// release gate, which attaches both containers to one network; what THIS
// fixture proves is the half that gate cannot: that the production
// binaries route a playbook-kind job into that adapter at all.
func writePlaybookFixture(tb testing.TB, dir string) {
	tb.Helper()
	content := `---
- hosts: all
  gather_facts: false
  connection: local
  tasks:
    - name: ` + harnessPlaybookTaskName + `
      ansible.builtin.command: /bin/true
`
	abs := filepath.Join(dir, filepath.FromSlash(harnessPlaybookID))
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		tb.Fatalf("making the playbook fixture directory: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
		tb.Fatalf("writing the playbook fixture: %v", err)
	}
}
