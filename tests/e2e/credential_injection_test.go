//go:build integration

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entinventory "github.com/Subject-Void-LLC/the-pleiades/internal/ent/inventory"
)

// Phase 22's tranche-A release gate, driven end to end through the real
// binaries: a credential type created over HTTP, a credential created over
// HTTP, bound to a template over HTTP, launched over HTTP, and the value it
// injects proven to have reached a real ansible-playbook inside a real
// ephemeral container.
//
// # Why the playbook prints a hash
//
// A gate proving arrival by printing the value would put the secret in the
// job log it also has to prove is clean. So the playbook prints the SHA-256
// of what it received, and the test compares that against the SHA-256 of
// what it created. Arrival is proven and the value is never printed, which
// is the only way to hold both halves at once.
//
// cmd/runner's own injection gate is the complementary one: it proves the
// same thing about the container's argv, environment and /proc from inside
// the boundary, against a hand-composed adapter. This one proves the whole
// chain composes: the API accepts the type, the store encrypts it, the
// resolver decrypts it, the fan-out renders it, the wire carries it and the
// Runner honours it, with nothing composed by the test at all.

// The credential this gate creates. Distinctive enough that finding it
// anywhere it should not be is a genuine result.
const (
	injectionE2ESecret = "sk-live-CANARY-e2e-4b3a2c1d0e9f8a7b"
	injectionE2EURL    = "https://api.e2e.example.test"
)

// injectionE2EPlaybookID is the second playbook this gate adds to the
// shared directory both binaries read.
const injectionE2EPlaybookID = "credential-injection.yml"

// injectionE2ETaskName is the task whose reported message carries the
// hash, so the log reader knows what to wait for.
const injectionE2ETaskName = "report the injected credential by hash"

// TestCredentialInjection_ReachesARealPlaybookThroughTheRealBinaries is the
// gate.
func TestCredentialInjection_ReachesARealPlaybookThroughTheRealBinaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the credential injection gate in short mode")
	}

	h := startHarness(t, withAnsible())
	writeInjectionPlaybookFixture(t, h.playbookDir)

	issuer := authtest.NewWithSecret(t, harnessJWTSecret, harnessJWTIssuer, harnessJWTAudience)
	adminToken := issuer.Token(t, &auth.Identity{Subject: "e2e-admin", Role: auth.RoleAdmin})

	templateID, organizationID := h.seedInjectionTemplate(t)

	// 1. The credential type, over real HTTP. Its injectors are the shape a
	//    customer's own custom type has: an environment variable their
	//    playbook reads, and nothing this platform special-cases.
	status, body := h.doJSON(t, http.MethodPost, "/api/v1/credential-types", adminToken, map[string]any{
		"organization": organizationID,
		"name":         "E2E REST API Token",
		"kind":         "cloud",
		"namespace":    "e2e_api_token",
		"inputs": map[string]any{
			"fields": []any{
				map[string]any{"id": "api_token", "label": "API Token", "secret": true},
				map[string]any{"id": "api_url", "label": "API URL"},
			},
			"required": []any{"api_token", "api_url"},
		},
		"injectors": map[string]any{
			"env": map[string]any{
				"E2E_API_TOKEN": "{{ api_token }}",
				"E2E_API_URL":   "{{ api_url }}",
			},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("creating the credential type returned %d, want 201. Body: %s\n%s", status, body, h.controller.output())
	}
	typeID := requireIntField(t, body, "id")

	// 2. The credential itself, carrying the real secret. This is the one
	//    request in the gate that puts a secret on the wire, which is the
	//    asymmetry the whole credential design rests on: writing one is
	//    ordinary, reading one back is not possible.
	status, body = h.doJSON(t, http.MethodPost, "/api/v1/credentials", adminToken, map[string]any{
		"organization":    organizationID,
		"credential_type": typeID,
		"name":            "e2e prod api",
		"inputs":          map[string]any{"api_token": injectionE2ESecret, "api_url": injectionE2EURL},
	})
	if status != http.StatusCreated {
		t.Fatalf("creating the credential returned %d, want 201. Body: %s\n%s", status, body, h.controller.output())
	}
	credentialID := requireIntField(t, body, "id")

	// The creation response is the first place a leak could happen, and it
	// is checked against the raw bytes rather than a decoded struct.
	if bytes.Contains(body, []byte(injectionE2ESecret)) {
		t.Fatalf("the credential creation response carries the secret: %s", body)
	}

	// 3. The binding, which is what decides what the template runs AS.
	status, body = h.doJSON(t, http.MethodPut,
		"/api/v1/templates/"+strconv.Itoa(templateID)+"/credentials", adminToken,
		map[string]any{"credentials": []any{credentialID}})
	if status != http.StatusOK {
		t.Fatalf("binding the credential returned %d, want 200. Body: %s\n%s", status, body, h.controller.output())
	}

	// 4. The launch, and the run.
	status, body = h.launchTemplate(t, adminToken, templateID)
	if status != http.StatusAccepted {
		t.Fatalf("the launch returned %d, want 202. Body: %s\n%s", status, body, h.controller.output())
	}
	jobID := requireStringField(t, body, "job_id")

	job := h.pollJobUntilTerminal(t, adminToken, jobID)
	if job.State != "completed" {
		t.Fatalf("the job ended %q, want completed\n%s", job.State, h.controller.output())
	}

	// ARRIVAL, proven by hash. The playbook printed the SHA-256 of the
	// environment variable it received; this is the SHA-256 of what was
	// created. Nothing printed the value itself.
	logs := h.collectJobLogs(t, jobID, injectionE2ETaskName)
	sum := sha256.Sum256([]byte(injectionE2ESecret))
	want := "token_sha256=" + hex.EncodeToString(sum[:])
	if !strings.Contains(logs, want) {
		t.Fatalf("the playbook did not receive the injected credential.\nwant %s\nlogs:\n%s", want, logs)
	}

	// ABSENCE, in every place the value could plausibly surface.
	h.assertNoInjectedSecretAnywhere(t, adminToken, jobID, templateID, logs)
}

// assertNoInjectedSecretAnywhere sweeps every surface the secret could
// reach, and each one is a place it really could have.
func (h *harness) assertNoInjectedSecretAnywhere(t *testing.T, bearer, jobID string, templateID int, logs string) {
	t.Helper()

	// The job log stream, which is what a UI renders live.
	if strings.Contains(logs, injectionE2ESecret) {
		t.Errorf("the job log stream carries the secret:\n%s", logs)
	}

	// The job resource, which anybody with job:read can fetch.
	if _, body := h.do(t, http.MethodGet, "/api/v1/jobs/"+jobID, bearer); bytes.Contains(body, []byte(injectionE2ESecret)) {
		t.Errorf("GET /jobs/%s carries the secret: %s", jobID, body)
	}

	// The template's saved launch configurations, which is where a
	// prompted value would wrongly land if the never-persist rule failed.
	if _, body := h.do(t, http.MethodGet,
		"/api/v1/templates/"+strconv.Itoa(templateID)+"/configs", bearer); bytes.Contains(body, []byte(injectionE2ESecret)) {
		t.Errorf("the template's saved configurations carry the secret: %s", body)
	}

	// The credential read back, which is the no-plaintext-read boundary
	// asserted through real HTTP rather than through a type.
	if _, body := h.do(t, http.MethodGet, "/api/v1/credentials", bearer); bytes.Contains(body, []byte(injectionE2ESecret)) {
		t.Errorf("listing credentials carries the secret: %s", body)
	}

	// Both production binaries' own captured output. The Controller
	// resolved and rendered the secret and the Runner handled it, so an
	// unmasked log line in either is a real leak.
	if strings.Contains(h.controller.output(), injectionE2ESecret) {
		t.Error("the controller's own output carries the secret")
	}
	if strings.Contains(h.runner.output(), injectionE2ESecret) {
		t.Error("the runner's own output carries the secret")
	}

	// The Runner process's own environment. PLAN.md Section 17.5 forbids a
	// secret there, and Section 29.4's exception is scoped to the ephemeral
	// container, never to the Runner host itself.
	h.assertRunnerEnvironmentIsClean(t)
}

// assertRunnerEnvironmentIsClean reads the Runner subprocess's own
// /proc/<pid>/environ.
//
// This is the assertion that distinguishes Section 29.4's exception from a
// general permission: a secret may enter the ephemeral container's
// environment because that container is destroyed with the job. The Runner
// is a long-lived process on a real host, and a secret in ITS environment
// would be readable by anything that can stat the process, for as long as
// it runs.
func (h *harness) assertRunnerEnvironmentIsClean(t *testing.T) {
	t.Helper()

	pid := h.runner.pid()
	if pid == 0 {
		t.Fatal("the runner subprocess has no pid to inspect")
	}
	environ, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		// Not a skip: this gate runs on Linux, and a missing procfs is a
		// change in the environment the gate's own claim depends on.
		t.Fatalf("reading the runner's own environment: %v", err)
	}
	if bytes.Contains(environ, []byte(injectionE2ESecret)) {
		t.Error("the runner process's own environment carries the injected secret")
	}
}

// seedInjectionTemplate creates a playbook template for this gate's own
// playbook and returns it with the organization it belongs to.
//
// Seeded through ent rather than the API deliberately: the template is
// scaffolding, and what this gate is about is the credential path, which is
// driven entirely over real HTTP.
func (h *harness) seedInjectionTemplate(tb testing.TB) (templateID, organizationID int) {
	tb.Helper()
	ctx := context.Background()

	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: h.dsn})
	if err != nil {
		tb.Fatalf("opening the database to seed the injection template: %v", err)
	}
	defer client.Close()

	set, err := client.Inventory.Query().Where(entinventory.NameEQ("edge routers")).Only(ctx)
	if err != nil {
		tb.Fatalf("finding the seeded inventory: %v", err)
	}
	org, err := set.QueryOrganization().Only(ctx)
	if err != nil {
		tb.Fatalf("finding the seeded organization: %v", err)
	}

	template := client.Template.Create().
		SetName("the credential injection gate").
		SetKind("playbook").
		SetDefinition(injectionE2EPlaybookID).
		SetOrganization(org).
		SetInventory(set).
		SaveX(ctx)
	return template.ID, org.ID
}

// writeInjectionPlaybookFixture writes the playbook that reports the
// injected credential by hash.
//
// connection: local, matching the harness's own fixture: this gate is about
// whether the credential reached the ansible-playbook process, not about
// reaching a remote host, and a real SSH target is cmd/runner's own gate's
// job.
func writeInjectionPlaybookFixture(tb testing.TB, dir string) {
	tb.Helper()

	content := `---
- hosts: all
  gather_facts: false
  connection: local
  tasks:
    - name: ` + injectionE2ETaskName + `
      ansible.builtin.debug:
        msg: "token_sha256={{ lookup('env', 'E2E_API_TOKEN') | hash('sha256') }} url={{ lookup('env', 'E2E_API_URL') }}"
`
	abs := filepath.Join(dir, filepath.FromSlash(injectionE2EPlaybookID))
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		tb.Fatalf("making the injection playbook directory: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
		tb.Fatalf("writing the injection playbook: %v", err)
	}
}

// doJSON issues a request carrying a JSON body, which the harness's own do
// helper deliberately does not: every request it was built for is a GET or
// a bodyless POST.
func (h *harness) doJSON(tb testing.TB, method, path, bearer string, body any) (int, []byte) {
	tb.Helper()

	encoded, err := json.Marshal(body)
	if err != nil {
		tb.Fatalf("encoding the %s %s request body: %v", method, path, err)
	}

	req, err := http.NewRequest(method, h.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		tb.Fatalf("building the %s %s request: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		tb.Fatalf("issuing %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		tb.Fatalf("reading the %s %s response body: %v", method, path, err)
	}
	return resp.StatusCode, out
}

// requireIntField reads a numeric field out of a JSON response, failing
// with the whole body when it is absent.
func requireIntField(tb testing.TB, body []byte, field string) int {
	tb.Helper()

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		tb.Fatalf("decoding the response: %v. Body: %s", err, body)
	}
	value, ok := decoded[field].(float64)
	if !ok {
		tb.Fatalf("the response carries no numeric %q field. Body: %s", field, body)
	}
	return int(value)
}
