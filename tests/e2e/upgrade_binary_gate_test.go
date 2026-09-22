//go:build integration

// Phase 84's upgrade gate at the binary level: the previous release and this
// build, as real processes, against one real PostgreSQL and one real NATS.
//
// It is the dynamic half of the compatibility policy (internal/ent/migrate's
// compat.go): the policy says a migration must leave a schema the build
// before it can still serve, and the only honest test of that is to run the
// build before it, keep it running while this build migrates the database it
// is using, and make it do its ordinary work afterwards. The static classifier
// in migrate's tests catches the shapes it knows; this catches what it does
// not, such as a query the previous build makes that the new schema breaks.
//
// In order:
//
//  1. The previous build creates the database and is seeded through its own
//     API, including a project sync, which writes the one existing table
//     this phase's migrations change (sync_runs).
//  2. This build's `migrate --plan` reports the upgrade (exit 3) and names
//     the migrations it would apply, and its `bootstrap-admin` refuses to be
//     the thing that upgrades the database.
//  3. This build starts and migrates while the previous one runs. The
//     previous build then creates, reads, changes and deletes every kind it
//     writes, and syncs its project again; this build reads what the previous
//     one wrote and uses it (launches its template, syncs its project).
//  4. A second controller of this build starts, and the first one is stopped
//     behind a client that routes by readiness, the way a load balancer
//     does: not one request fails, which is the shutdown drain's promise.
//  5. The previous build started again is refused, and says why, which is
//     what the rollback documentation tells an operator to expect from a
//     build older than the compatibility window.
package e2e

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
)

// upgradeStack is the shared infrastructure one upgrade gate runs against.
type upgradeStack struct {
	dsn, natsURL string
	runbookDir   string
	projectRoot  string
	token        string
}

// upgradeController is one controller process of either build.
type upgradeController struct {
	name    string
	proc    *managedProc
	baseURL string
}

// controllerEnv is how every controller in this gate is configured: the
// harness's own settings, plus local project sources (a sync clones a git
// repository this test makes on disk) and a short shutdown drain.
func (s upgradeStack) controllerEnv(t *testing.T, port int) []string {
	return []string{
		"DB_DSN=" + s.dsn,
		"NATS_URL=" + s.natsURL,
		"LISTEN_ADDR=127.0.0.1:" + strconv.Itoa(port),
		"JWT_ISSUER=" + harnessJWTIssuer,
		"JWT_AUDIENCE=" + harnessJWTAudience,
		"JWT_SECRET=" + harnessJWTSecret,
		"MASTER_ENCRYPTION_KEY=" + harnessMasterKey,
		"RUNBOOK_DIR=" + s.runbookDir,
		"CONTROLLER_CREDENTIALS_DIR=" + t.TempDir(),
		"OTEL_TRACES_EXPORTER=none",
		"TLS_CERT_FILE=" + harnessCert.CertFile,
		"TLS_KEY_FILE=" + harnessCert.KeyFile,
		"PLEIADES_PROJECT_ROOT=" + s.projectRoot,
		"PLEIADES_PROJECT_ALLOW_LOCAL_SOURCE=true",
		"PLEIADES_PROJECT_ALLOW_INSECURE_SOURCE=true",
		// A build that has the drain (this one) reports not ready for this
		// long before it closes its listener: longer than the balancer
		// below takes to notice. A build that predates it ignores it.
		"SHUTDOWN_DRAIN=3s",
	}
}

// start launches a controller of the given binary without waiting for it.
func (s upgradeStack) start(t *testing.T, name, bin string) *upgradeController {
	t.Helper()
	port := freeTCPPort(t)
	return &upgradeController{
		name:    name,
		proc:    startProcess(t, name, bin, s.controllerEnv(t, port)),
		baseURL: "https://127.0.0.1:" + strconv.Itoa(port),
	}
}

// gateClient trusts the harness certificate every controller serves.
func gateClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{TLSClientConfig: harnessCert.TLSClientConfig()}}
}

// exited reports whether the process has ended.
func (c *upgradeController) exited() bool {
	select {
	case <-c.proc.done:
		return true
	default:
		return false
	}
}

// waitReady waits for /readyz to answer 200, failing if the process exits.
func (c *upgradeController) waitReady(t *testing.T) {
	t.Helper()
	client := gateClient(5 * time.Second)
	deadline := time.Now().Add(2 * time.Minute * raceTimeScale)
	for time.Now().Before(deadline) {
		if c.exited() {
			t.Fatalf("%s exited before it was ready\n%s", c.name, c.proc.output())
		}
		if resp, err := client.Get(c.baseURL + "/readyz"); err == nil {
			ready := resp.StatusCode == http.StatusOK
			resp.Body.Close()
			if ready {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s was not ready in time\n%s", c.name, c.proc.output())
}

// call makes one authenticated API request and returns its status and body.
func (c *upgradeController) call(t *testing.T, token, method, path string, body any) (int, []byte) {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, c.baseURL+"/api/v1"+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := gateClient(30 * time.Second).Do(req)
	if err != nil {
		t.Fatalf("%s %s on %s: %v", method, path, c.name, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

// must makes a request and fails unless it answered 2xx.
func (c *upgradeController) must(t *testing.T, token, method, path string, body any) []byte {
	t.Helper()
	status, out := c.call(t, token, method, path, body)
	if status < 200 || status > 299 {
		t.Fatalf("%s %s on %s = %d: %s\n%s", method, path, c.name, status, out, c.proc.output())
	}
	return out
}

// idOf reads "id" from a JSON object, as a string: most ids are numbers and a
// schedule's is a UUID, and a path needs neither distinction.
func idOf(t *testing.T, raw []byte) string {
	t.Helper()
	var v struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || len(v.ID) == 0 {
		t.Fatalf("no id in %s (%v)", raw, err)
	}
	return strings.Trim(string(v.ID), `"`)
}

// seeded is what one build created, so the other build can find it.
type seeded struct {
	org, inventory, credential, project, template, schedule string
	device                                                  string
}

// gitRepository makes a one-commit git repository a project can sync from.
func gitRepository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		// #nosec G204 -- git, with arguments this function chooses.
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=gate", "GIT_AUTHOR_EMAIL=gate@example.com",
			"GIT_COMMITTER_NAME=gate", "GIT_COMMITTER_EMAIL=gate@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "site.yml"), []byte("- hosts: all\n  tasks: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "site.yml")
	run("commit", "-q", "-m", "a playbook")
	return "file://" + dir
}

// atoi turns an id back into the number a request body carries.
func atoi(t *testing.T, id string) int {
	t.Helper()
	n, err := strconv.Atoi(id)
	if err != nil {
		t.Fatalf("id %q is not a number: %v", id, err)
	}
	return n
}

// sshCredentialType is the id of the shipped machine credential type.
func sshCredentialType(t *testing.T, c *upgradeController, token string) int {
	t.Helper()
	out := c.must(t, token, http.MethodGet, "/credential-types", nil)
	var list struct {
		Types []struct {
			ID        int    `json:"id"`
			Namespace string `json:"namespace"`
		} `json:"credential_types"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		t.Fatalf("reading credential types: %v\n%s", err, out)
	}
	for _, ct := range list.Types {
		if ct.Namespace == "ssh" {
			return ct.ID
		}
	}
	t.Fatalf("no ssh credential type in %s", out)
	return 0
}

// syncAndWait syncs a project and waits for the attempt to succeed.
func syncAndWait(t *testing.T, c *upgradeController, token string, project string) {
	t.Helper()
	c.must(t, token, http.MethodPost, fmt.Sprintf("/projects/%s/sync", project), nil)
	deadline := time.Now().Add(time.Minute * raceTimeScale)
	for time.Now().Before(deadline) {
		out := c.must(t, token, http.MethodGet, fmt.Sprintf("/projects/%s", project), nil)
		var p struct {
			Status string `json:"sync_status"`
			Error  string `json:"sync_error"`
		}
		if err := json.Unmarshal(out, &p); err != nil {
			t.Fatal(err)
		}
		switch p.Status {
		case "succeeded":
			return
		case "failed":
			t.Fatalf("%s's sync of project %s failed: %s", c.name, project, p.Error)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s's sync of project %s never finished", c.name, project)
}

// seedEveryKind creates, through c's API, one of every kind this gate
// exercises, and returns them.
func seedEveryKind(t *testing.T, c *upgradeController, token, label, repo string) seeded {
	t.Helper()
	var s seeded
	s.org = idOf(t, c.must(t, token, http.MethodPost, "/organizations", map[string]any{"name": label + "-org"}))
	s.inventory = idOf(t, c.must(t, token, http.MethodPost, "/inventories", map[string]any{"name": label + "-inventory", "organization": atoi(t, s.org)}))
	s.device = label + "-device"
	c.must(t, token, http.MethodPost, "/inventory/devices", map[string]any{"name": s.device, "type": "linux_server"})
	s.credential = idOf(t, c.must(t, token, http.MethodPost, "/credentials", map[string]any{
		"name": label + "-credential", "credential_type": sshCredentialType(t, c, token), "organization": atoi(t, s.org),
		"inputs": map[string]any{"username": "gate", "password": "a-secret-only-the-key-opens"},
	}))
	s.project = idOf(t, c.must(t, token, http.MethodPost, "/projects", map[string]any{
		"name": label + "-project", "organization": atoi(t, s.org), "scm_type": "git", "scm_url": repo, "scm_branch": "main",
	}))
	syncAndWait(t, c, token, s.project)
	s.template = idOf(t, c.must(t, token, http.MethodPost, "/templates", map[string]any{
		"name": label + "-template", "kind": "runbook", "definition": harnessRunbookID, "inventory": atoi(t, s.inventory),
	}))
	s.schedule = idOf(t, c.must(t, token, http.MethodPost, "/schedules", map[string]any{
		"name": label + "-schedule", "template": atoi(t, s.template), "rrule": "FREQ=DAILY", "dtstart": "2031-01-01T09:00:00Z",
	}))
	return s
}

// changeAndDelete updates and then deletes each kind in s through c, and
// launches its template first, so every write path a build takes on these
// tables runs.
func changeAndDelete(t *testing.T, c *upgradeController, token string, s seeded) {
	t.Helper()
	c.must(t, token, http.MethodPost, fmt.Sprintf("/templates/%s/launch", s.template), map[string]any{})
	c.must(t, token, http.MethodPatch, fmt.Sprintf("/organizations/%s", s.org), map[string]any{"name": "renamed-" + s.org})
	c.must(t, token, http.MethodPatch, fmt.Sprintf("/credentials/%s", s.credential), map[string]any{"name": "renamed-" + s.credential})
	c.must(t, token, http.MethodPatch, fmt.Sprintf("/schedules/%s", s.schedule), map[string]any{"enabled": false})
	c.must(t, token, http.MethodDelete, fmt.Sprintf("/schedules/%s", s.schedule), nil)
	c.must(t, token, http.MethodDelete, fmt.Sprintf("/credentials/%s", s.credential), nil)
	c.must(t, token, http.MethodDelete, "/inventory/devices/"+s.device, nil)
	c.must(t, token, http.MethodDelete, fmt.Sprintf("/projects/%s", s.project), nil)
}

// readAll reads each kind in s through c.
func readAll(t *testing.T, c *upgradeController, token string, s seeded) {
	t.Helper()
	for _, path := range []string{
		fmt.Sprintf("/organizations/%s", s.org),
		fmt.Sprintf("/inventories/%s", s.inventory),
		fmt.Sprintf("/credentials/%s", s.credential),
		fmt.Sprintf("/projects/%s", s.project),
		fmt.Sprintf("/templates/%s", s.template),
		fmt.Sprintf("/schedules/%s", s.schedule),
	} {
		c.must(t, token, http.MethodGet, path, nil)
	}
}

// runPlan runs this build's `controller migrate --plan --json` against the
// stack's database and returns its exit code and the verdict it reported.
func (s upgradeStack) runPlan(t *testing.T) (int, string, []string) {
	t.Helper()
	// #nosec G204 -- the binary this package built.
	cmd := exec.Command(controllerBinPath, "migrate", "--plan", "--json")
	cmd.Env = append(os.Environ(), "DB_DSN="+s.dsn, "DB_PATH=")
	out, err := cmd.Output()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("running migrate --plan: %v", err)
	}
	var report struct {
		Plan struct {
			Verdict string `json:"verdict"`
			Pending []struct {
				Name string `json:"name"`
			} `json:"pending"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("migrate --plan printed no plan: %v\n%s", err, out)
	}
	var pending []string
	for _, p := range report.Plan.Pending {
		pending = append(pending, p.Name)
	}
	return code, report.Plan.Verdict, pending
}

// TestUpgradeGate_ThePreviousBuildKeepsServingWhileThisOneMigrates is the
// gate; see this file's header for its five steps.
func TestUpgradeGate_ThePreviousBuildKeepsServingWhileThisOneMigrates(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the previous release and starts postgres, nats and four controllers")
	}
	prev := requirePreviousBuild(t)
	t.Logf("previous release %s; this build adds %d migrations: %v", prev.ref, len(prev.crossed), prev.crossed)

	// This gate's whole subject is the previous build serving WHILE THIS ONE
	// MIGRATES, and the mechanism below depends on there being a migration to
	// hold: it takes a SHARE lock on schema_migrations to stall this build at
	// its first claim of whatever the previous release lacks. When the two
	// builds share a schema there is no such claim, nothing stalls, the
	// controller becomes ready in milliseconds, and requireMigrating fails on
	// a 200 that is the correct answer.
	//
	// So this is a skip rather than a pass or a failure. The third instance of
	// this shape found in one session (FAILURE_PATTERNS.md 300 and this one),
	// and the reason all three hid is the same: Phase 84's own branch added a
	// migration, so no run from it could reach the empty case.
	if len(prev.crossed) == 0 {
		t.Skipf("this build adds no migration over %s, so there is no migration phase to observe; the overlap this gate measures cannot exist without one", prev.ref)
	}

	stack := upgradeStack{
		dsn:         startPostgres(t, t.Context()),
		natsURL:     startNATS(t, t.Context()),
		runbookDir:  t.TempDir(),
		projectRoot: t.TempDir(),
	}
	writeRunbookFixture(t, stack.runbookDir)
	stack.token = authtest.NewWithSecret(t, harnessJWTSecret, harnessJWTIssuer, harnessJWTAudience).
		Token(t, &auth.Identity{Subject: "upgrade-gate", Role: auth.RoleAdmin})
	repo := gitRepository(t)

	// 1. The previous build creates the database and holds data.
	previousCtl := stack.start(t, "previous", prev.controller)
	previousCtl.waitReady(t)
	before := seedEveryKind(t, previousCtl, stack.token, "before", repo)

	// 2. Asked first, this build says what it would do, and an admin
	// command from it will not be what does it.
	if len(prev.crossed) > 0 {
		code, verdict, pending := stack.runPlan(t)
		if code != 3 || verdict != "pending" || strings.Join(pending, ",") != strings.Join(prev.crossed, ",") {
			t.Fatalf("migrate --plan = exit %d, %s, %v; want exit 3, pending, %v", code, verdict, pending, prev.crossed)
		}
		// #nosec G204 -- the binary this package built.
		admin := exec.Command(controllerBinPath, "bootstrap-admin", "--email", "gate@example.com", "--password-stdin")
		admin.Env = append(os.Environ(), stack.controllerEnv(t, freeTCPPort(t))...)
		admin.Stdin = strings.NewReader("a-password-long-enough-for-the-policy\n")
		out, err := admin.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "does not upgrade a database") {
			t.Fatalf("bootstrap-admin against a database this build would upgrade = %v:\n%s\nwant a refusal", err, out)
		}
		if code, _, _ := stack.runPlan(t); code != 3 {
			t.Fatalf("the refused admin command changed the database: migrate --plan now exits %d", code)
		}
	}

	// 3. This build migrates while the previous one is running, and the
	// previous one is asked something the whole time, through a client that
	// routes by readiness and never retries. A request that fails, or finds
	// the previous build not ready, while this build starts and migrates is
	// one a user of the old build saw. The gate used to look at the previous
	// build only after this one was ready, so it never tested the property
	// its name claims (FAILURE_PATTERNS.md #287).
	//
	// The migrations this build adds apply in milliseconds on an empty
	// database: the first run of this check saw 3 requests in the whole
	// overlap. A real migration on real data takes seconds, so the gate makes
	// this one take seconds too. It holds a SHARE lock on schema_migrations,
	// which stops any migration at its first claim (whatever the previous
	// release lacks), and a claim's wait is unbounded by design, so the window
	// lasts exactly as long as the lock. SHARE leaves reads alone, and nothing
	// the previous build serves touches that table. While it is held, this
	// build must be up and not ready, which is the migration phase observed
	// directly rather than inferred.
	releaseMigrations := holdMigrations(t, stack.dsn)
	overlap := startBalancer(t, stack.token, previousCtl)
	current := stack.start(t, "current", controllerBinPath)
	current.requireMigrating(t)
	time.Sleep(migrationHold)
	// Still not ready at the end of the hold: the window above was this
	// build's migration, not only a sleep. Were the lock not holding it, the
	// migration would have finished in milliseconds and this would read 200.
	current.requireMigrating(t)
	releaseMigrations()
	current.waitReady(t)
	probes, failures := overlap.finish()
	if probes < 40 {
		t.Fatalf("only %d requests reached the previous build while this one started and migrated; the overlap was not exercised", probes)
	}
	if len(failures) > 0 {
		t.Fatalf("%d of %d requests to the previous build failed while this build migrated: %v", len(failures), probes, failures)
	}
	t.Logf("%d requests to the previous build while this one started and migrated, none failed", probes)
	if code, verdict, _ := stack.runPlan(t); code != 0 || verdict != "current" {
		t.Fatalf("after this build started, migrate --plan = exit %d, %s; want 0, current", code, verdict)
	}

	// The previous build keeps doing its ordinary work on the new schema.
	previousCtl.waitReady(t)
	readAll(t, previousCtl, stack.token, before)
	syncAndWait(t, previousCtl, stack.token, before.project)
	after := seedEveryKind(t, previousCtl, stack.token, "after", repo)
	changeAndDelete(t, previousCtl, stack.token, after)

	// And this build reads and uses what the previous one wrote.
	readAll(t, current, stack.token, before)
	syncAndWait(t, current, stack.token, before.project)
	current.must(t, stack.token, http.MethodPost, fmt.Sprintf("/templates/%s/launch", before.template), map[string]any{})

	// 4. The previous build goes, a second controller of this build comes,
	// and the first of this build drains behind a readiness-routed client.
	previousCtl.proc.stop(t)
	second := stack.start(t, "current-2", controllerBinPath)
	second.waitReady(t)
	lb := startBalancer(t, stack.token, current, second)
	// Halfway between two readiness polls, not on one. The balancer and
	// this sleep start together, so stopping at a whole number of polls
	// stops at the very moment the balancer looks, and a controller that
	// did not drain would then never be sent a request it could fail: the
	// control run with the drain switched off passed that way.
	time.Sleep(2*readinessPoll + readinessPoll/2)
	current.proc.stop(t)
	time.Sleep(2 * readinessPoll)
	probes, failures = lb.finish()
	if probes < 20 {
		t.Fatalf("the balancer sent only %d requests; the drain was not exercised", probes)
	}
	if len(failures) > 0 {
		t.Fatalf("%d of %d requests failed while a controller drained and stopped: %v", len(failures), probes, failures)
	}
	t.Logf("%d requests through a drain and a stop, none failed", probes)

	// 5. The previous build, started again, is refused.
	again := stack.start(t, "previous-again", prev.controller)
	select {
	case <-again.proc.done:
	case <-time.After(time.Minute * raceTimeScale):
		t.Fatalf("the previous build started against a database migrated past it and kept running\n%s", again.proc.output())
	}
	if len(prev.crossed) > 0 && !strings.Contains(again.proc.output(), "recognize") {
		t.Fatalf("the previous build's refusal does not say why:\n%s", again.proc.output())
	}
}

// migrationHold is how long the gate holds this build's migration at its
// first claim, so the previous build is observed serving through a migration
// of realistic length rather than one that finishes in milliseconds.
const migrationHold = 3 * time.Second

// holdMigrations takes a SHARE lock on schema_migrations in a transaction of
// its own and returns the function that ends it. A migration's claim inserts
// into that table, which a SHARE lock blocks, so every migration waits at its
// first claim until the lock goes. The release also runs at cleanup, so a
// failed gate never leaves the lock behind.
func holdMigrations(t *testing.T, dsn string) func() {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("opening the database to hold its migrations: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		_ = db.Close()
		t.Fatalf("beginning the transaction that holds the migrations: %v", err)
	}
	if _, err := tx.Exec(`LOCK TABLE schema_migrations IN SHARE MODE`); err != nil {
		_ = tx.Rollback()
		_ = db.Close()
		t.Fatalf("locking schema_migrations: %v", err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			_ = tx.Rollback()
			_ = db.Close()
		})
	}
	t.Cleanup(release)
	return release
}

// requireMigrating waits for this controller's listener and requires it to
// answer as not ready: the port is bound before the schema is migrated, so a
// 503 from /readyz is the migration phase itself, seen from outside.
func (c *upgradeController) requireMigrating(t *testing.T) {
	t.Helper()
	client := gateClient(time.Second)
	deadline := time.Now().Add(30 * time.Second * raceTimeScale)
	for time.Now().Before(deadline) {
		resp, err := client.Get(c.baseURL + "/readyz")
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("while its migration was held, %s answered /readyz with %d; want 503, up and migrating", c.name, resp.StatusCode)
		}
		return
	}
	t.Fatalf("%s never bound its listener while its migration was held\n%s", c.name, c.proc.output())
}

// balancer sends requests to whichever controllers last answered /readyz
// with 200, round robin, the way a load balancer routes by readiness. It
// never retries a failed request, so every failure is one a client saw.
type balancer struct {
	token   string
	targets []*upgradeController

	mu       sync.Mutex
	ready    map[int]bool
	probes   int
	failures []string

	stop chan struct{}
	done sync.WaitGroup
}

// startBalancer starts routing requests across targets.
func startBalancer(t *testing.T, token string, targets ...*upgradeController) *balancer {
	b := &balancer{token: token, targets: targets, ready: map[int]bool{}, stop: make(chan struct{})}
	for i := range targets {
		b.ready[i] = true
	}
	b.done.Add(2)
	go b.watchReadiness()
	go b.send(t)
	return b
}

// readinessPoll is how often the balancer rechecks readiness. A real load
// balancer is slower still (the chart's readiness probe runs every five
// seconds, and an endpoint change then has to propagate), and a faster poll
// would let a controller that stops without draining vanish between two
// requests unnoticed, which is exactly what this gate's first version did: it
// passed with the drain switched off. At one second, switching it off fails.
const readinessPoll = time.Second

// watchReadiness marks each target ready or not every readinessPoll.
func (b *balancer) watchReadiness() {
	defer b.done.Done()
	client := gateClient(time.Second)
	for {
		select {
		case <-b.stop:
			return
		case <-time.After(readinessPoll):
		}
		for i, target := range b.targets {
			ready := false
			if resp, err := client.Get(target.baseURL + "/readyz"); err == nil {
				ready = resp.StatusCode == http.StatusOK
				resp.Body.Close()
			}
			b.mu.Lock()
			b.ready[i] = ready
			b.mu.Unlock()
		}
	}
}

// send makes one authenticated request every 25ms to the next ready target.
func (b *balancer) send(t *testing.T) {
	defer b.done.Done()
	client := gateClient(5 * time.Second)
	next := 0
	for {
		select {
		case <-b.stop:
			return
		case <-time.After(25 * time.Millisecond):
		}
		b.mu.Lock()
		target := -1
		for i := 0; i < len(b.targets); i++ {
			candidate := (next + i) % len(b.targets)
			if b.ready[candidate] {
				target = candidate
				break
			}
		}
		next++
		b.mu.Unlock()

		failure := ""
		if target < 0 {
			failure = "no controller was ready"
		} else {
			req, err := http.NewRequest(http.MethodGet, b.targets[target].baseURL+"/api/v1/organizations", nil)
			if err != nil {
				t.Error(err)
				return
			}
			req.Header.Set("Authorization", "Bearer "+b.token)
			resp, err := client.Do(req)
			switch {
			case err != nil:
				failure = b.targets[target].name + ": " + err.Error()
			case resp.StatusCode != http.StatusOK:
				failure = fmt.Sprintf("%s: %d", b.targets[target].name, resp.StatusCode)
			}
			if resp != nil {
				resp.Body.Close()
			}
		}
		b.mu.Lock()
		b.probes++
		if failure != "" {
			b.failures = append(b.failures, failure)
		}
		b.mu.Unlock()
	}
}

// finish stops the balancer and reports what it saw.
func (b *balancer) finish() (int, []string) {
	close(b.stop)
	b.done.Wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.probes, b.failures
}
