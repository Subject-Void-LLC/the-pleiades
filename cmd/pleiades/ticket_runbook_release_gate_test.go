// Release gate for Phase 117a: the owner's ticket scenario, end to end on
// the Crawl tier, through the real binary, against a real ticket system.
//
// One runbook reads an issue from Gitea (a real open-source ticket system,
// in a container, reached as an onboarded generic_http device with its own
// credential), triages it with an approved external program that uses no
// device, collects from the switch the ticket names (a real sshd, chosen
// from the ticket's text and bounded by within:), decides with a condition,
// and writes a comment back with a body rendered from what it collected.
package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh/knownhosts"
)

// The ticket system's admin account, created in the container.
const (
	giteaUser     = "gate"
	giteaPassword = "gitea-gate-password-5Qm"
)

// ticketRunbook is the scenario: read, triage, collect, decide, write back.
const ticketRunbook = `id: ticket
tasks:
  - name: read the ticket
    http.request:
      target: tickets
      url: "/repos/gate/ops/issues/{{ vars.ticket | urlencode }}"
    register: ticket
  - name: triage it
    gate117.ticket.triage:
      title: "{{ result.ticket.json.title }}"
      body: "{{ result.ticket.json.body }}"
    register: triage
  - name: collect from the switch the ticket names
    exec.command:
      target: "{{ result.triage.ci }}"
      cmd: uname -s
    within: switches
    register: collect
  - name: write the finding back
    http.request:
      target: tickets
      url: "/repos/gate/ops/issues/{{ vars.ticket | urlencode }}/comments"
      method: POST
      headers:
        Content-Type: application/json
      body: '{"body": {{ result.collect.stdout | to_json }}}'
      status_code:
        - 201
    when_cel: "result.triage.severity == 'high'"
`

// gitea is a running Gitea: its API base and a client that authenticates
// as the admin account.
type gitea struct {
	base string
}

// startGitea starts Gitea in a container with an admin account and an ops
// repository, and returns it.
func startGitea(t *testing.T) gitea {
	t.Helper()
	ctx := context.Background()
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "gitea/gitea:1.22.6",
			ExposedPorts: []string{"3000/tcp"},
			Env: map[string]string{
				"GITEA__security__INSTALL_LOCK":        "true",
				"GITEA__database__DB_TYPE":             "sqlite3",
				"GITEA__service__DISABLE_REGISTRATION": "true",
			},
			WaitingFor: wait.ForHTTP("/api/v1/version").WithPort("3000/tcp").WithStartupTimeout(3 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("starting Gitea: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })
	code, out, err := ctr.Exec(ctx, []string{"gitea", "admin", "user", "create", "--admin",
		"--username", giteaUser, "--password", giteaPassword, "--email", "gate@example.invalid", "--must-change-password=false"},
		tcexec.WithUser("git"))
	if err != nil || code != 0 {
		body, _ := io.ReadAll(out)
		t.Fatalf("creating the Gitea admin: exit %d, %v\n%s", code, err, body)
	}
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := ctr.MappedPort(ctx, "3000/tcp")
	if err != nil {
		t.Fatal(err)
	}
	g := gitea{base: "http://" + net.JoinHostPort(host, port.Port()) + "/api/v1"}
	g.call(t, http.MethodPost, "/user/repos", map[string]any{"name": "ops"}, http.StatusCreated)
	return g
}

// call makes one API call as the admin, failing the test unless it answers
// want, and returns the decoded body.
func (g gitea) call(t *testing.T, method, path string, body any, want int) any {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, g.base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(giteaUser, giteaPassword)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s answered %d, want %d: %s", method, path, resp.StatusCode, want, raw)
	}
	var decoded any
	_ = json.Unmarshal(raw, &decoded)
	return decoded
}

// openIssue opens an issue and returns its number as text.
func (g gitea) openIssue(t *testing.T, title, body string) string {
	t.Helper()
	issue, _ := g.call(t, http.MethodPost, "/repos/gate/ops/issues", map[string]any{"title": title, "body": body}, http.StatusCreated).(map[string]any)
	number, _ := issue["number"].(float64)
	return strconv.Itoa(int(number))
}

// comments returns the bodies of an issue's comments.
func (g gitea) comments(t *testing.T, number string) []string {
	t.Helper()
	list, _ := g.call(t, http.MethodGet, "/repos/gate/ops/issues/"+number+"/comments", nil, http.StatusOK).([]any)
	var out []string
	for _, c := range list {
		if m, ok := c.(map[string]any); ok {
			body, _ := m["body"].(string)
			out = append(out, body)
		}
	}
	return out
}

// buildTriage builds the triage program into a fresh directory the loader
// accepts, and returns the directory.
func buildTriage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", filepath.Join(dir, "triage"), "./testdata/triage").CombinedOutput(); err != nil {
		t.Fatalf("building the triage program: %v\n%s", err, out)
	}
	return dir
}

// TestTicketRunbookReleaseGate is Phase 117a's release gate.
func TestTicketRunbookReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the ticket runbook release gate container test in short mode")
	}
	tickets := startGitea(t)
	collections := buildTriage(t)
	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "PLEIADES_COLLECTIONS_DIR": collections}

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "tickets", "--type", "generic_http", "--set", "base_url=" + tickets.base, "--set", "http_auth=basic", "--set", "http_allow_plaintext_credentials=true"},
		{"add-credential", "tickets", "--username", giteaUser, "--password", giteaPassword},
		// Two names for the one sshd: one inside the switches bound, and
		// one outside it, so a ticket naming the wrong device is refused
		// for the bound and not for the address.
		{"add-host", "sw-gate", "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port), "--tags", "switches"},
		{"add-credential", "sw-gate", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
		{"add-host", "dc-gate", "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port), "--tags", "servers"},
		{"add-credential", "dc-gate", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
		{"collection", "approve", "triage", "--yes"},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	onboardJSON(t, dir, env, "tickets")
	writeFile(t, dir, "runbooks/ticket.yaml", ticketRunbook)

	run := func(ticket string) (string, error) {
		t.Helper()
		return runPleiadesWithEnv(t, dir, env, "run", "runbooks/ticket.yaml", "--extra-vars", "ticket="+ticket)
	}

	// The scenario: a ticket that names a switch and says something is down.
	number := tickets.openIssue(t, "Interface down on the core switch", "Reported by monitoring.\nci: sw-gate\n")
	out, err := run(number)
	if err != nil {
		t.Fatalf("the ticket runbook failed: %v\n%s", err, out)
	}
	got := tickets.comments(t, number)
	if len(got) != 1 || strings.TrimSpace(got[0]) != "Linux" {
		t.Fatalf("the ticket's comments are %q, want the one finding collected from the switch (Linux)\n%s", got, out)
	}
	if strings.Contains(out, giteaPassword) || strings.Contains(out, releaseGateSSHPassword) {
		t.Error("the run printed a credential")
	}

	// Decide: a ticket that says nothing is down is collected and left alone.
	quiet := tickets.openIssue(t, "Question about the core switch", "ci: sw-gate\n")
	if out, err := run(quiet); err != nil {
		t.Fatalf("the low-severity ticket's run failed: %v\n%s", err, out)
	}
	if c := tickets.comments(t, quiet); len(c) != 0 {
		t.Errorf("a low-severity ticket was written back to: %q", c)
	}

	// Negative controls. Each fails before anything reaches a device it
	// should not, and writes nothing back.
	for _, tc := range []struct {
		name, title, body, want string
	}{
		{"a device outside the bound", "Server down", "ci: dc-gate\n", `"dc-gate", which is not a device within "switches"`},
		{"the bound's own tag", "Everything down", "ci: switches\n", `"switches", which is not a device within "switches"`},
		{"a command smuggled into the name", "Link down", "ci: sw-gate; reboot\n", `"sw-gate; reboot", which is not a device within "switches"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := tickets.openIssue(t, tc.title, tc.body)
			out, err := run(n)
			if err == nil || !strings.Contains(out, tc.want) {
				t.Fatalf("err = %v, want a refusal saying %s\n%s", err, tc.want, out)
			}
			if c := tickets.comments(t, n); len(c) != 0 {
				t.Errorf("a refused run wrote back: %q", c)
			}
		})
	}

	// A ticket number carrying URL syntax stays one path segment: it reaches
	// no other issue and writes nowhere.
	t.Run("url syntax in the ticket number", func(t *testing.T) {
		out, err := run(number + "/comments?x=1&y=\n")
		if err == nil {
			t.Fatalf("a ticket number with URL syntax ran:\n%s", out)
		}
		if c := tickets.comments(t, number); len(c) != 1 {
			t.Errorf("the real ticket gained a comment: %q", c)
		}
	})

	// A runbook that lets data choose the host is refused before it runs.
	t.Run("a url template steering the host", func(t *testing.T) {
		writeFile(t, dir, "runbooks/steer.yaml", "id: steer\ntasks:\n  - name: call wherever the ticket says\n    http.request:\n      url: \"http://{{ vars.host }}/api\"\n")
		out, err := runPleiadesWithEnv(t, dir, env, "validate", "runbooks/steer.yaml")
		if err == nil || !strings.Contains(out, "is a URL") {
			t.Fatalf("validate did not refuse a host chosen by data: %v\n%s", err, out)
		}
	})

	// The run's report says what happened in terms an operator reads.
	if !strings.Contains(out, "write the finding back") {
		t.Errorf("the run's text does not name the write-back task:\n%s", out)
	}
}
