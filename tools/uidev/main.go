//go:build devtools

// Command uidev boots the real controller against throwaway dependencies
// and prints a ready-to-use sign-in token, so the web UI can be looked at
// without standing up a cluster.
//
// It carries a build tag for the same reason internal/ent/migrate/gen does:
// it is a developer convenience that shells out to docker and to a compiler,
// and holding it to the security posture of shipped server code would mean
// waiving half a dozen findings that are only findings because this is a
// tool. It is invoked by path (go run tools/uidev/main.go), never imported,
// and nothing in the shipped binaries can reach it.
//
// The tag is `devtools`, not `ignore`, and the change of word was a fix. A
// build-ignored file is compiled by nothing: `go build ./...`, `go vet ./...`
// and every test in the repository skip it, so no guard anywhere could see a
// string typed here. This file held its own copy of the NATS image and flags,
// the deployment moved to a different variant and gained a flag, and the copy
// here went stale under a doc comment still promising the two matched.
// Nothing was able to fail. Two things fixed that, and both are load bearing:
// the file now imports internal/testsupport and reads the one pin every other
// caller reads, and `make ci` compiles and vets this file under
// `-tags devtools`, so a break here fails a build instead of waiting for
// somebody to run `make ui-dev`. `go run` on an explicitly named file ignores
// build constraints, so the Makefile target is unchanged either way.
//
// Do not reintroduce a literal image reference or flag list below. If this
// tool needs another piece of the deployment's configuration, export it from
// internal/testsupport, where a test can compare it against
// docker-compose.yml.
//
// It runs the REAL cmd/controller binary as a subprocess rather than
// reassembling a controller-shaped thing here. That is RULE 0 applied to a
// development tool: a harness that wires its own router would let the UI
// look correct while the shipped composition root served something else,
// which is precisely the class of failure this repository has recorded
// before. Everything below only prepares the environment that binary
// reads.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"

	// The single source for the container image and server flags the
	// deployment runs. Importing an internal/ package from tools/ is legal
	// (the internal rule is scoped to the module, and this file is in it)
	// and is what internal/testsupport's own package doc asks for: "the
	// pin lives here rather than at the call site."
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "uidev:", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}

	addr := os.Getenv("PLEIADES_UI_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	// Checked first, before the build, the container and the banner.
	// Discovering this after all of that means the failure prints below a
	// token and a set of instructions that no longer work, which is a
	// worse experience than the bind error deserves.
	if err := checkPortFree(addr); err != nil {
		return err
	}

	workdir, err := os.MkdirTemp("", "pleiades-uidev-")
	if err != nil {
		return fmt.Errorf("creating scratch directory: %w", err)
	}
	fmt.Println("uidev: scratch directory", workdir)

	// A fixed secret so the printed token stays valid across restarts of
	// this tool, which matters when the whole point is reloading a page.
	const secret = "uidev-development-only-signing-secret-not-for-any-real-use"

	token, err := mintToken(secret)
	if err != nil {
		return fmt.Errorf("minting a development token: %w", err)
	}

	// A runbook directory the controller can actually open. It fails
	// closed at startup on a missing one, which is right for a server and
	// means this tool has to provide it.
	runbookDir := filepath.Join(workdir, "runbooks")
	if err := os.MkdirAll(runbookDir, 0o750); err != nil {
		return fmt.Errorf("creating runbook directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runbookDir, "ping-fleet.yaml"),
		[]byte("id: ping-fleet\ntasks:\n  - name: reach every host\n    fqcn: net.ssh.ping\n"), 0o600); err != nil {
		return fmt.Errorf("seeding a runbook: %w", err)
	}

	// The controller needs a real JetStream. Starting one here rather
	// than asking the reader to run compose first keeps this a
	// single-command tool, and the container is named so a second run
	// replaces it instead of colliding with itself.
	// The broker gets its own port too, so two instances do not fight.
	natsPort := 4222
	if addr != ":8080" {
		natsPort = 4222 + os.Getpid()%1000 + 1
	}
	stopNATS, err := startNATS(natsPort)
	if err != nil {
		return err
	}
	defer stopNATS()

	binary := filepath.Join(workdir, "controller")
	build := exec.Command("go", "build", "-o", binary, "./cmd/controller")
	build.Dir = root
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("building the controller: %w", err)
	}

	// A serving certificate, from the same generator tests/e2e and
	// `make dev-cert` use. It goes in the scratch directory, so it is
	// deleted with everything else when this tool exits, and it is
	// regenerated on every run rather than being cached anywhere.
	//
	// This is no longer optional: the controller refuses to serve plain
	// HTTP unless something in front of it terminates TLS, and nothing is
	// in front of this one.
	cert, err := testsupport.NewServingCert(filepath.Join(workdir, "tls"))
	if err != nil {
		return fmt.Errorf("generating a development certificate: %w", err)
	}

	// The environment both the bootstrap command and the server read. Built
	// once and shared, because a bootstrap that opened a different database
	// from the server would create an account nobody can sign in to, and
	// the two variable lists drifting apart is exactly how that happens.
	controllerEnv := append(os.Environ(),
		"LISTEN_ADDR="+addr,
		fmt.Sprintf("NATS_URL=nats://127.0.0.1:%d", natsPort),
		"DB_PATH="+filepath.Join(workdir, "uidev.db"),
		"JWT_SECRET="+secret,
		// 32 zero bytes, base64. A development key, and the tool says so.
		"MASTER_ENCRYPTION_KEY=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"RUNBOOK_DIR="+runbookDir,
		// Real TLS, terminated by the controller itself. This replaces
		// PLEIADES_UI_INSECURE_COOKIES=1, which used to sit here with a
		// comment claiming a browser refuses a __Host- cookie over
		// http://localhost. That claim was wrong: every current browser
		// makes an explicit exception for loopback, and so does Go's own
		// cookie jar. What was true is that the exception keys on the host
		// STRING, so it never applied to a hostname in /etc/hosts pointing
		// at 127.0.0.1, and the resulting failure was a refused sign-in
		// that reported bad credentials for a correct password.
		"TLS_CERT_FILE="+cert.CertFile,
		"TLS_KEY_FILE="+cert.KeyFile,
		// So the banner is visible while it is being reviewed. Override
		// either variable to see another level.
		"PLEIADES_BANNER_LEVEL="+getenvOr("PLEIADES_BANNER_LEVEL", "development"),
		"PLEIADES_BANNER_TEXT="+getenvOr("PLEIADES_BANNER_TEXT", "development -- throwaway database"),
	)

	// Create the development administrator BEFORE the server starts.
	//
	// Through the real `controller bootstrap-admin` subcommand rather than
	// by writing rows, for the same reason seed() below goes through the
	// real HTTP API: a development tool that wired its own account creation
	// could produce an account the shipped command cannot, and the first
	// thing anybody looking at the UI does is sign in. If the subcommand is
	// broken, this should be broken too.
	//
	// Before the server rather than after, because it opens the same SQLite
	// file and there is no reason for two processes to hold it at once.
	// OpenDatabase runs the migrations, so the file need not exist yet.
	if err := bootstrapAdmin(binary, root, controllerEnv); err != nil {
		return fmt.Errorf("creating the development administrator: %w", err)
	}

	cmd := exec.Command(binary)
	cmd.Dir = root
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	// The kernel kills the controller if this process dies for any reason,
	// including SIGKILL, a closed terminal, or an editor stopping the task.
	// Without it an orphaned controller keeps the port and keeps logging at
	// a broker that has been removed, which is exactly what happened while
	// this tool was being written.
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	cmd.Env = controllerEnv

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting the controller: %w", err)
	}

	// Seed through the real HTTP API rather than by writing rows, so what
	// the UI lists is what the platform's own create path produced. It
	// also means a failure here is a genuine defect in that path rather
	// than a fixture drifting away from it.
	if err := seed(addr, token, cert); err != nil {
		fmt.Fprintln(os.Stderr, "uidev: seeding failed (the UI still works, it is just empty):", err)
	}

	fmt.Print(banner(token, addr, cert))

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}()

	err = cmd.Wait()
	_ = os.RemoveAll(workdir)
	return err
}

// mintToken signs a development admin token.
//
// It signs directly rather than reaching for internal/auth/authtest,
// which internal/archtest forbids anything outside that package from
// importing -- a transitive check over the whole module, and this tool is
// part of the module. The claims below are exactly what
// internal/auth.jwtEvaluator validates, so a token this produces is
// accepted by the real evaluator rather than by a relaxed one.
func mintToken(secret string) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":    "developer@localhost",
		"role":   "admin",
		"scopes": []string{"inventory:read", "inventory:write", "runbook:read", "runbook:execute", "job:read"},
		"iss":    "pleiades-controller",
		"aud":    "pleiades-api",
		"iat":    now.Unix(),
		"exp":    now.Add(12 * time.Hour).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// startNATS runs the same NATS image and flags docker-compose.yml uses,
// so the broker the UI is developed against is the one it is deployed
// against. It returns a cleanup function.
//
// Both values come from internal/testsupport, which docker-compose.yml is
// tested against, so that sentence is now enforced instead of promised.
// It used to be a promise, and it stopped being true without anything
// noticing: this function ran nats:2.14.4 with -js while the deployment
// moved to nats:2.14.4-alpine with -js -m 8222. Those are different
// images, not different names for one image, and the difference is the
// whole reason compose changed. See internal/testsupport.NATSImage.
func startNATS(natsPort int) (func(), error) {
	// Named per process, not fixed. A shared name meant a second instance
	// removed the first one's broker out from under it, and the first
	// controller then spent its life logging connection failures at a
	// container that no longer existed. Two developers, or one developer
	// and a scratch instance, are a normal thing to want.
	name := fmt.Sprintf("pleiades-uidev-nats-%d", os.Getpid())

	// The image and the server flags are appended, in that order, because
	// `docker run` takes the image reference first and everything after it
	// as the container's command. -m 8222 opens the monitoring port inside
	// the container only; this tool publishes 4222 alone and probes it
	// over TCP, so the extra flag costs nothing here and keeps the broker
	// configured exactly as deployed.
	args := append([]string{"run", "-d", "--rm",
		"--name", name,
		"-p", fmt.Sprintf("%d:4222", natsPort),
		testsupport.NATSImage}, testsupport.NATSCommand()...)

	start := exec.Command("docker", args...)
	if out, err := start.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("starting nats (is docker running?): %w: %s", err, out)
	}

	fmt.Println("uidev: waiting for nats")
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", natsPort), time.Second)
		if err == nil {
			_ = conn.Close()
			return func() { _ = exec.Command("docker", "rm", "-f", name).Run() }, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	_ = exec.Command("docker", "rm", "-f", name).Run()
	return nil, fmt.Errorf("nats did not become reachable within 30s")
}

// seedDevice is one fixture row, chosen to exercise more than one badge
// colour and more than one device type so the rendered list is
// representative rather than five identical lines.
type seedDevice struct {
	Name  string   `json:"name"`
	Type  string   `json:"type"`
	Tags  []string `json:"tags"`
	State string   `json:"state,omitempty"`
}

// seed waits for the controller to answer, then creates a handful of
// devices through the versioned API.
func seed(addr, token string, cert testsupport.ServingCert) error {
	base := "https://localhost" + addr

	// One client for the wait and the writes, trusting the certificate this
	// run generated and nothing else. Not InsecureSkipVerify: a development
	// tool that skipped verification would be the obvious place to copy the
	// pattern from into something that matters.
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: cert.TLSClientConfig()},
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		time.Sleep(250 * time.Millisecond)
	}

	devices := []seedDevice{
		{Name: "core-router-01", Type: "cisco_router", Tags: []string{"core", "edge"}},
		{Name: "core-router-02", Type: "cisco_router", Tags: []string{"core"}},
		{Name: "access-switch-01", Type: "cisco_switch", Tags: []string{"access"}},
		{Name: "build-server-01", Type: "linux_server", Tags: []string{"ci"}, State: "quarantined"},
		{Name: "build-server-02", Type: "linux_server", Tags: []string{"ci"}},
		{Name: "jump-host", Type: "linux_server", Tags: []string{"bastion"}, State: "onboarding"},
	}

	created := 0
	for _, d := range devices {
		body, err := json.Marshal(d)
		if err != nil {
			return err
		}
		req, err := http.NewRequest(http.MethodPost, base+"/api/v1/inventory/devices", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusCreated {
			created++
		}
	}

	fmt.Printf("uidev: seeded %d/%d devices\n", created, len(devices))
	return nil
}

// checkPortFree refuses to start when something already holds the address,
// naming the most likely cause rather than leaving the reader to work out
// what "address already in use" means here.
func checkPortFree(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf(
			"%s is already in use.\n\n"+
				"  Something is already listening there -- most often a previous\n"+
				"  `make ui-dev` that is still running, or a controller started by\n"+
				"  `docker compose up`.\n\n"+
				"  Stop it, or run on another port:\n"+
				"      PLEIADES_UI_ADDR=:8081 make ui-dev",
			addr)
	}
	return ln.Close()
}

// The development administrator's credentials.
//
// Fixed rather than generated, for the same reason the signing secret above
// is fixed: the point of this tool is reloading a page, and a password that
// changed on every restart would be retyped on every restart. They are
// published in the source and printed on the terminal, which is the honest
// posture for a throwaway database that also accepts a token anybody can
// mint from the secret three lines up.
//
// Long enough to satisfy the real policy the real store enforces. A
// development password that bypassed the length floor would mean this tool
// exercised a path production does not have.
const (
	devEmail    = "dev@pleiades.test"
	devPassword = "development-password"
)

// bootstrapAdmin runs the shipped `controller bootstrap-admin` subcommand
// against the same database the server will open.
//
// --password-stdin rather than a prompt, because this tool has no terminal
// to prompt on, and never a flag value, because that is the one thing the
// subcommand refuses on every path: a flag is visible in the process
// argument list to every other user on the machine.
func bootstrapAdmin(binary, root string, env []string) error {
	cmd := exec.Command(binary, "bootstrap-admin", "--email", devEmail, "--password-stdin")
	cmd.Dir = root
	cmd.Env = env
	cmd.Stdin = strings.NewReader(devPassword + "\n")

	// Output captured rather than inherited: on success this prints two
	// lines telling the operator to sign in, which the banner below says
	// better, and on failure the captured text is what the error carries.
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func banner(token, addr string, cert testsupport.ServingCert) string {
	host := "https://localhost" + addr
	return fmt.Sprintf(`
================================================================
  The Pleiades UI  ->  %[1]s/ui
================================================================

  Sign in at  %[1]s/ui/login

  Email     %[3]s
  Password  %[4]s

  That account was created by the real `+"`controller bootstrap-admin`"+`
  subcommand, so this is the same path an operator uses on a
  clean machine. It holds system-scope admin.

  ----------------------------------------------------------

  Or paste this token, which is the break-glass route:

%[2]s

  Both are development credentials. The token is signed with a
  fixed, published secret and is valid for 12 hours; the password
  is published in tools/uidev/main.go. Both are worthless
  anywhere but this throwaway database.

  ----------------------------------------------------------

  This is HTTPS, on a certificate generated for this run alone.
  Nothing signed it, so the browser warns once and you accept it
  once. A command-line client needs:

      --cacert %[5]s

  Plain HTTP is not offered, because the controller refuses to
  serve it unattended. Loopback is the one origin where a browser
  would have accepted the Secure, __Host- session cookie over
  http anyway; every other origin refuses it silently, and this
  tool serving the exception would only teach a habit that breaks
  the moment the address is not localhost.

  Ctrl-C stops the controller and deletes its scratch directory.

================================================================

`, host, token, devEmail, devPassword, cert.CertFile)
}

func getenvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s", dir)
		}
		dir = parent
	}
}
