//go:build integration

// The backup phase's compose release gate: the make targets an operator
// types, against the real stack, from nothing to a restore onto a machine
// with no key.
package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// backupGate is one run of the gate: its .env directory, its backup
// directory, and the environment every make target gets.
type backupGate struct {
	root, setupDir, backupDir string
}

// env is the child environment: the gate's own .env and backup directory,
// and COMPOSE_ENV_FILES so that the `docker compose up` inside `make up`
// reads the gate's .env rather than the checkout's.
func (g backupGate) env() []string {
	return []string{
		"PLEIADES_SETUP_DIR=" + g.setupDir,
		"COMPOSE_ENV_FILES=" + filepath.Join(g.setupDir, ".env"),
	}
}

// make runs a make target with stdin, returning its output and error.
func (g backupGate) make(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	return runPackagingTool(t, g.root, g.env(), stdin, "make", append([]string{"--no-print-directory", "BACKUP_DIR=" + g.backupDir}, args...)...)
}

// mustMake is make that fails the test on an error.
func (g backupGate) mustMake(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	out, err := g.make(t, stdin, args...)
	if err != nil {
		t.Fatalf("make %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// emptyEnvFile creates the gate's .env empty. COMPOSE_ENV_FILES requires
// the file to exist, where compose's own default .env does not; setup adds
// its keys to an existing file the same way it writes a new one.
func (g backupGate) emptyEnvFile(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(g.setupDir, ".env"), nil, 0o600); err != nil {
		t.Fatalf("creating .env: %v", err)
	}
}

// gateAPI calls the controller's API with a token signed by the JWT secret
// in the gate's .env.
func (g backupGate) api(t *testing.T) func(method, path string, body any) (int, []byte) {
	t.Helper()
	_, jwt := readSetupEnvFile(t, filepath.Join(g.setupDir, ".env"))
	client := composeTLSClient(t, g.root)
	token := authtest.NewWithSecret(t, jwt, "pleiades-controller", "pleiades-api").
		Token(t, &auth.Identity{Subject: "backup-gate", Role: auth.RoleAdmin})
	return func(method, path string, body any) (int, []byte) {
		raw, _ := json.Marshal(body)
		if body == nil {
			raw = nil
		}
		req, err := http.NewRequest(method, "https://localhost:8080"+path, bytes.NewReader(raw))
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
}

// bootstrapOrg is the id of the organization setup's administrator made.
func bootstrapOrg(t *testing.T, call func(method, path string, body any) (int, []byte)) int {
	t.Helper()
	status, body := call(http.MethodGet, "/api/v1/organizations", nil)
	var orgs struct {
		Organizations []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"organizations"`
	}
	_ = json.Unmarshal(body, &orgs)
	for _, o := range orgs.Organizations {
		if o.Name == "bootstrap" {
			return o.ID
		}
	}
	t.Fatalf("no bootstrap organization (%d): %s", status, body)
	return 0
}

// addCredential stores a credential named name through the API, creating
// its type when typeID is zero, and returns the type's id.
func (g backupGate) addCredential(t *testing.T, name string, typeID int) int {
	t.Helper()
	call := g.api(t)
	orgID := bootstrapOrg(t, call)
	if typeID == 0 {
		status, body := call(http.MethodPost, "/api/v1/credential-types", map[string]any{
			"organization": orgID, "name": "Backup Gate Token", "kind": "cloud", "namespace": "backup_gate",
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
		typeID = created.ID
	}
	status, body := call(http.MethodPost, "/api/v1/credentials", map[string]any{
		"organization": orgID, "credential_type": typeID, "name": name,
		"inputs": map[string]any{"token": "a-secret-the-key-protects"},
	})
	if status != http.StatusCreated {
		t.Fatalf("creating credential %q returned %d: %s", name, status, body)
	}
	return typeID
}

// credentialNames lists the bootstrap organization's credentials, as the
// API returns them.
func (g backupGate) credentialNames(t *testing.T) string {
	t.Helper()
	call := g.api(t)
	status, body := call(http.MethodGet, "/api/v1/credentials?organization="+strconv.Itoa(bootstrapOrg(t, call)), nil)
	if status != http.StatusOK {
		t.Fatalf("listing credentials returned %d: %s", status, body)
	}
	return string(body)
}

// volumes lists the gate project's volumes.
func (g backupGate) volumes(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("docker", "volume", "ls", "-q", "--filter", "label=com.docker.compose.project="+gateComposeProject).Output()
	if err != nil {
		t.Fatalf("listing volumes: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// backups lists the backup directory's file names.
func (g backupGate) backups(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(g.backupDir)
	if err != nil {
		t.Fatalf("reading the backup directory: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestBackupReleaseGate_ComposeFromBackupToACleanMachine is the gate: every
// make target, in the order an operator meets them, against the real stack
// and the real backup image.
func TestBackupReleaseGate_ComposeFromBackupToACleanMachine(t *testing.T) {
	requireDockerDaemon(t)
	requirePackagingTool(t, "make", "run the make targets an operator types")
	root := ensurePleiadesImages(t)
	freshComposeStack(t, root)
	g := backupGate{root: root, setupDir: t.TempDir(), backupDir: t.TempDir()}
	adminSetup := "SETUP_FLAGS=--non-interactive --admin-email " + composeGateEmail + " --password-stdin"

	// 1. make up, from nothing: setup, then the stack.
	g.emptyEnvFile(t)
	g.mustMake(t, composeGatePassword+"\n", "up", adminSetup)
	signInOverTLS(t, composeTLSClient(t, root))
	key, _ := readSetupEnvFile(t, filepath.Join(g.setupDir, ".env"))
	typeID := g.addCredential(t, "gate before the backup", 0)

	// 2. make backup, with the stack running.
	out := g.mustMake(t, "", "backup")
	files := g.backups(t)
	if len(files) != 1 || !strings.Contains(files[0], crypto.Fingerprint(key)[:8]) {
		t.Fatalf("make backup left %v; want one backup named for the key", files)
	}
	backupFile := filepath.Join(g.backupDir, files[0])
	if info, _ := os.Stat(backupFile); info.Mode().Perm() != 0o600 {
		t.Fatalf("the backup is at mode %v, want 0600", info.Mode().Perm())
	}
	if !strings.Contains(out, "1 credential") || !strings.Contains(out, "DOES NOT HOLD THE KEY") || strings.Contains(out, crypto.EncodeKey(key)) {
		t.Fatalf("make backup's report:\n%s", out)
	}

	// 3. A change after the backup, then make restore: the change is gone,
	// the stack is back, and the broker was reset.
	g.addCredential(t, "gate after the backup", typeID)
	natsBefore, _ := exec.Command("docker", "volume", "inspect", "--format", "{{.CreatedAt}}", gateComposeProject+"_nats-data").Output()
	out = g.mustMake(t, "", "restore", "BACKUP="+backupFile)
	for _, want := range []string{"opens under the key in", "Restored the database pleiades from", "before-restore"} {
		if !strings.Contains(out, want) {
			t.Errorf("make restore's report does not say %q:\n%s", want, out)
		}
	}
	names := g.credentialNames(t)
	if !strings.Contains(names, "gate before the backup") || strings.Contains(names, "gate after the backup") {
		t.Fatalf("after the restore the credentials are %s", names)
	}
	natsAfter, _ := exec.Command("docker", "volume", "inspect", "--format", "{{.CreatedAt}}", gateComposeProject+"_nats-data").Output()
	if bytes.Equal(natsBefore, natsAfter) {
		t.Error("the broker's volume survived the restore; its queued messages belong to the replaced database")
	}
	signInOverTLS(t, composeTLSClient(t, root))

	// 4. make down keeps everything.
	g.mustMake(t, "", "down")
	if !strings.Contains(g.volumes(t), "postgres-data") {
		t.Fatal("make down removed the database volume")
	}

	// 5. make decom: refused without a terminal or the flag, then done.
	if out, err := g.make(t, "", "decom"); err == nil || !strings.Contains(out, "--destroy-deployment") {
		t.Fatalf("make decom with no terminal and no flag: %v\n%s", err, out)
	}
	if !strings.Contains(g.volumes(t), "postgres-data") {
		t.Fatal("a refused decom removed the database volume")
	}
	out = g.mustMake(t, "", "decom", "DECOM_FLAGS=--destroy-deployment")
	if !strings.Contains(out, "2 of those backups need key") {
		t.Errorf("make decom did not say which key the backups need:\n%s", out)
	}
	if v := g.volumes(t); v != "" {
		t.Fatalf("volumes left after decom: %s", v)
	}
	if _, err := os.Stat(filepath.Join(g.setupDir, ".env")); !os.IsNotExist(err) {
		t.Fatal("decom left .env, and the key in it")
	}
	if len(g.backups(t)) != 2 {
		t.Fatalf("decom changed the backup directory: %v", g.backups(t))
	}

	// 6. A fresh install under a new key refuses the old backup, and keeps
	// what it has.
	g.emptyEnvFile(t)
	g.mustMake(t, composeGatePassword+"\n", "up", adminSetup)
	out, err := g.make(t, "", "restore", "BACKUP="+backupFile)
	if err == nil || !strings.Contains(out, "under no key in .env") || !strings.Contains(out, "nothing was changed") {
		t.Fatalf("restoring under another key: %v\n%s", err, out)
	}
	g.mustMake(t, "", "decom", "DECOM_FLAGS=--destroy-deployment")

	// 7. The clean machine: the key on standard input, then make up adds
	// the rest, and the original administrator signs in.
	g.emptyEnvFile(t)
	out = g.mustMake(t, crypto.EncodeKey(key)+"\n", "restore", "BACKUP="+backupFile,
		"RESTORE_FLAGS=--key-stdin", "SETUP_FLAGS=--non-interactive")
	if strings.Contains(out, crypto.EncodeKey(key)) {
		t.Fatal("make restore printed the key it was given")
	}
	restored, jwt := readSetupEnvFile(t, filepath.Join(g.setupDir, ".env"))
	if !bytes.Equal(restored, key) || jwt == "" {
		t.Fatal("after a clean-machine restore .env does not hold the backup's key and a JWT secret")
	}
	signInOverTLS(t, composeTLSClient(t, root))
	if names := g.credentialNames(t); !strings.Contains(names, "gate before the backup") {
		t.Fatalf("after the clean-machine restore the credentials are %s", names)
	}
	// And the restored values open under the restored key, counted again by
	// a new backup in the production image.
	out = g.mustMake(t, "", "backup")
	if !strings.Contains(out, "They open under key") {
		t.Fatalf("a backup after the clean-machine restore does not find every value under the key:\n%s", out)
	}
}
