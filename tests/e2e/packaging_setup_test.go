//go:build integration

// Phase 83's Release Gate, Docker Compose half.
//
// The claim: on a machine with no prior state, the setup wizard followed by
// `docker compose up` reaches an authenticated UI with no value constructed
// by hand at any step, and re-running it refuses rather than replacing,
// naming the file it would have destroyed.
//
// Every step runs the real thing. Setup runs as the compose `setup` service,
// exactly the command `make setup` runs; the stack starts from the .env that
// setup wrote, read by docker compose itself through --env-file; the
// administrator is the one setup created; and the data the refusals count is
// a credential created through the real API with a token signed by the JWT
// secret setup generated.
//
// WHAT THIS DOES TO THE MACHINE, as in the Phase 20 half: it takes the
// compose stack down with its volumes, first and last. It writes nothing
// into the checkout: setup writes into a temporary directory named by
// PLEIADES_SETUP_DIR, and the test checks that the checkout's own .env, if a
// developer has one, is byte for byte what it was.
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// setupGateUser is the --user `make setup` passes: this user, so .env is
// ours to read back.
func setupGateUser() string {
	return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
}

// readSetupEnvFile returns the key and JWT secret in the .env setup wrote.
func readSetupEnvFile(t *testing.T, path string) ([]byte, string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("setup did not write %s: %v", path, err)
	}
	f, err := setup.ParseEnvFile(data)
	if err != nil {
		t.Fatalf("setup wrote a .env its own reader refuses: %v", err)
	}
	raw, _ := f.Get(setup.VarMasterKey)
	key, err := crypto.DecodeKey(raw, path)
	if err != nil {
		t.Fatalf("the key setup wrote does not decode: %v", err)
	}
	jwt, _ := f.Get(setup.VarJWTSecret)
	return key, jwt
}

// TestSetupReleaseGate_ComposeFromNothingToASignIn is the gate.
func TestSetupReleaseGate_ComposeFromNothingToASignIn(t *testing.T) {
	requireDockerDaemon(t)
	root := ensurePleiadesImages(t)
	t.Cleanup(func() { composeDown(t, root) })
	composeDown(t, root)

	checkoutEnv := filepath.Join(root, ".env")
	checkoutBefore, checkoutErrBefore := os.ReadFile(checkoutEnv)

	dir := t.TempDir()
	file := filepath.Join(dir, ".env")
	env := []string{"PLEIADES_SETUP_DIR=" + dir}
	runSetup := func(stdin string, args ...string) (string, error) {
		return runPackagingTool(t, root, env, stdin, "docker",
			append([]string{"compose", "run", "--rm", "-T", "--user", setupGateUser(), "setup"}, args...)...)
	}

	// 1. The wizard, as `make setup` runs it without a terminal.
	out, err := runSetup(composeGatePassword+"\n",
		"--non-interactive", "--admin-email", composeGateEmail, "--password-stdin")
	if err != nil {
		t.Fatalf("setup failed: %v\n%s", err, out)
	}
	key, jwt := readSetupEnvFile(t, file)
	for name, secret := range map[string]string{"key": crypto.EncodeKey(key), "JWT secret": jwt, "password": composeGatePassword} {
		if strings.Contains(out, secret) {
			t.Fatalf("setup printed the %s", name)
		}
	}
	if !strings.Contains(out, file) || !strings.Contains(out, "activity trail records encryption key") {
		t.Fatalf("setup's output does not name the file on the host and the recorded key:\n%s", out)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Fatalf(".env was written at %v, want 0600", info.Mode().Perm())
	}
	checkoutAfter, checkoutErrAfter := os.ReadFile(checkoutEnv)
	if (checkoutErrBefore == nil) != (checkoutErrAfter == nil) || !bytes.Equal(checkoutBefore, checkoutAfter) {
		t.Fatal("the gate changed the checkout's own .env")
	}

	// 2. docker compose resolves the wizard's file, and not a developer's.
	assertComposeResolvesKey(t, root, file, key, jwt)

	// 3. The stack, from that file, to a signed-in session.
	mustRunPackagingTool(t, root, nil, "", "docker", "compose", "--env-file", file, "up", "-d", "--wait")
	assertFourHealthyServices(t, root)
	signInOverTLS(t, composeTLSClient(t, root))

	// 4. Something encrypted for the refusals to count, through the API,
	// with a token signed by the secret setup generated.
	createCredentialThroughAPI(t, composeTLSClient(t, root), jwt)

	// 5. Re-running refuses and names the file.
	out, err = runSetup("", "--non-interactive")
	if err == nil || !strings.Contains(out, file) || !strings.Contains(out, "irreversible") {
		t.Fatalf("a re-run did not refuse naming %s (err %v):\n%s", file, err, out)
	}

	// 6. The destructive flag refuses too, counting what the key protects.
	out, err = runSetup("", "--non-interactive", "--destroy-existing-encryption-key")
	if err == nil || !strings.Contains(out, "1 credential") || !strings.Contains(out, "does not override this") {
		t.Fatalf("replacing a key that protects a credential was not refused with the count (err %v):\n%s", err, out)
	}
	if again, _ := readSetupEnvFile(t, file); !bytes.Equal(again, key) {
		t.Fatal("a refused replacement changed the key")
	}

	// 7. The case a surviving volume presents: the file is gone and the data
	// is not. A new key must not be written over it.
	if err := os.Remove(file); err != nil {
		t.Fatalf("removing .env: %v", err)
	}
	out, err = runSetup("", "--non-interactive")
	if err == nil || !strings.Contains(out, "already holds encrypted data") || !strings.Contains(out, "1 credential") {
		t.Fatalf("a first write over a database holding a credential was not refused (err %v):\n%s", err, out)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("the refused first write created .env")
	}
}

// assertComposeResolvesKey proves `docker compose --env-file` hands the
// controller exactly the key and JWT secret setup wrote. The values are
// compared in memory and never printed.
func assertComposeResolvesKey(t *testing.T, root, file string, key []byte, jwt string) {
	t.Helper()
	cmd := packagingCommand(t, root, nil, "docker", "compose", "--env-file", file, "config", "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker compose config: %v", err)
	}
	var cfg struct {
		Services map[string]struct {
			Environment map[string]string `json:"environment"`
		} `json:"services"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("decoding docker compose config: %v", err)
	}
	ctl := cfg.Services["controller"].Environment
	if ctl[setup.VarMasterKey] != crypto.EncodeKey(key) || ctl[setup.VarJWTSecret] != jwt {
		t.Fatal("docker compose did not resolve the key and JWT secret from the file setup wrote")
	}
}

// createCredentialThroughAPI creates one credential in the bootstrap
// organization, over the real API, as an administrator whose token is signed
// with jwt.
func createCredentialThroughAPI(t *testing.T, client *http.Client, jwt string) {
	t.Helper()
	token := authtest.NewWithSecret(t, jwt, "pleiades-controller", "pleiades-api").
		Token(t, &auth.Identity{Subject: "setup-gate", Role: auth.RoleAdmin})
	call := func(method, path string, body any) (int, []byte) {
		var reader *bytes.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = bytes.NewReader(raw)
		} else {
			reader = bytes.NewReader(nil)
		}
		req, err := http.NewRequest(method, "https://localhost:8080"+path, reader)
		if err != nil {
			t.Fatalf("building %s %s: %v", method, path, err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return resp.StatusCode, buf.Bytes()
	}

	status, body := call(http.MethodGet, "/api/v1/organizations", nil)
	if status != http.StatusOK {
		t.Fatalf("listing organizations with a token signed by setup's JWT secret returned %d: %s", status, body)
	}
	var orgs struct {
		Organizations []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"organizations"`
	}
	_ = json.Unmarshal(body, &orgs)
	orgID := 0
	for _, o := range orgs.Organizations {
		if o.Name == "bootstrap" {
			orgID = o.ID
		}
	}
	if orgID == 0 {
		t.Fatalf("no bootstrap organization, which setup's administrator should have created: %s", body)
	}

	status, body = call(http.MethodPost, "/api/v1/credential-types", map[string]any{
		"organization": orgID, "name": "Setup Gate Token", "kind": "cloud", "namespace": "setup_gate",
		"inputs": map[string]any{
			"fields":   []any{map[string]any{"id": "token", "label": "Token", "secret": true}},
			"required": []any{"token"},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("creating a credential type returned %d: %s", status, body)
	}
	var created struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(body, &created)
	status, body = call(http.MethodPost, "/api/v1/credentials", map[string]any{
		"organization": orgID, "credential_type": created.ID, "name": "setup gate",
		"inputs": map[string]any{"token": "a-secret-the-key-protects"},
	})
	if status != http.StatusCreated {
		t.Fatalf("creating a credential returned %d: %s", status, body)
	}
}

// TestSetupReleaseGate_ComposeSetupAtATerminal runs the setup service
// through docker compose on a real pseudo terminal, which is how `make setup`
// runs it for a person. It proves the terminal reaches setup through a
// container whose log driver is off, that the key is shown once and typed
// back, and that the container keeps no log of it.
func TestSetupReleaseGate_ComposeSetupAtATerminal(t *testing.T) {
	requireDockerDaemon(t)
	root := ensurePleiadesImages(t)
	t.Cleanup(func() { composeDown(t, root) })
	composeDown(t, root)

	dir := t.TempDir()
	cmd := packagingCommand(t, root, []string{"PLEIADES_SETUP_DIR=" + dir},
		"docker", "compose", "run", "--rm", "--user", setupGateUser(), "setup")
	terminal, err := pty.Start(cmd)
	if err != nil {
		t.Skipf("no pseudo terminal available here: %v", err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	var mu sync.Mutex
	var shown bytes.Buffer
	go func() {
		chunk := make([]byte, 4096)
		for {
			n, err := terminal.Read(chunk)
			mu.Lock()
			shown.Write(chunk[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	text := func() string { mu.Lock(); defer mu.Unlock(); return shown.String() }
	waitFor := func(want string) {
		deadline := time.Now().Add(3 * time.Minute)
		for !strings.Contains(text(), want) {
			if time.Now().After(deadline) {
				t.Fatalf("the terminal never showed %q:\n%q", want, text())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	typeLine := func(line string) {
		if _, err := terminal.Write([]byte(line + "\n")); err != nil {
			t.Fatalf("typing: %v", err)
		}
	}

	waitFor("longest link outage")
	typeLine("")
	waitFor("Press Enter when you have stored the key")
	match := regexp.MustCompile(`\n {4}([A-Za-z0-9+/]{43}=)\r?\n`).FindStringSubmatch(text())
	if match == nil {
		t.Fatalf("no key on the possession screen:\n%q", text())
	}
	assertSetupContainerKeepsNoLog(t, root)
	typeLine("")
	waitFor("Type or paste the key you stored")
	// Echo is off inside the container's terminal by now; a short pause
	// covers the switch, since the parent end of a docker-attached terminal
	// does not report the container's settings.
	time.Sleep(500 * time.Millisecond)
	typeLine(match[1])
	waitFor("matches, byte for byte")
	waitFor("Email address for the first administrator")
	typeLine("")

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("setup exited %d:\n%q", exit.ExitCode(), text())
		}
	case <-time.After(2 * time.Minute):
		t.Fatalf("setup did not exit:\n%q", text())
	}
	key, _ := readSetupEnvFile(t, filepath.Join(dir, ".env"))
	if crypto.EncodeKey(key) != match[1] {
		t.Fatal("the key written is not the key shown")
	}
	if strings.Count(text(), match[1]) != 1 {
		t.Fatalf("the key reached the terminal %d times", strings.Count(text(), match[1]))
	}
}

// assertSetupContainerKeepsNoLog inspects the running setup container and
// requires its log driver to be none, while the key is on its screen.
func assertSetupContainerKeepsNoLog(t *testing.T, root string) {
	t.Helper()
	ids := strings.Fields(mustRunPackagingTool(t, root, nil, "",
		"docker", "ps", "-q", "--filter", "label=com.docker.compose.project=pleiades",
		"--filter", "label=com.docker.compose.service=setup"))
	if len(ids) != 1 {
		t.Fatalf("found %d running setup containers, want 1", len(ids))
	}
	driver := strings.TrimSpace(mustRunPackagingTool(t, root, nil, "",
		"docker", "inspect", "--format", "{{.HostConfig.LogConfig.Type}}", ids[0]))
	if driver != "none" {
		t.Fatalf("the setup container logs through %q while the key is on its screen", driver)
	}
}
