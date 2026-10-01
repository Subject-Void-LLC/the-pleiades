// Release gate for Phase 117a against the ServiceNow Table API: the
// owner's scenario, with the ticket system ServiceNow speaks, through the
// real binary.
//
// By default "ServiceNow" is ServiceNo!, the repository's local mock of the
// Table API (tests/serviceno/serviceno.py), run in a python:3-alpine
// container over TLS with a certificate this test issues, so the device is
// reached the way a real instance is, over HTTPS with a verified chain.
// Setting PLEIADES_SNOW_INSTANCE (https://<instance>.service-now.com),
// PLEIADES_SNOW_USER and PLEIADES_SNOW_PASSWORD runs the same test against
// a real instance instead: every call is the same Table API call, and the
// records it creates carry a random suffix and are deleted afterwards.
package main_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh/knownhosts"
)

// serviceNoPassword is the mock instance's admin password.
const serviceNoPassword = "serviceno-gate-password-8Kx"

// snowRunbook reads the incident, collects from the configuration item it
// names (bounded by within:), and writes the finding to the incident's work
// notes when its priority is critical. The condition reads the incident as
// result.incident, the register one device wrote, the same root the
// rendered params read.
const snowRunbook = `id: snow
tasks:
  - name: read the incident
    http.request:
      target: snow
      url: "/api/now/table/incident?sysparm_query=number%3D{{ vars.ticket | urlencode }}&sysparm_limit=1&sysparm_display_value=true&sysparm_exclude_reference_link=true"
    register: incident
  - name: collect from the configuration item
    exec.command:
      target: "{{ result.incident.json.result[0].cmdb_ci }}"
      cmd: uname -s
    within: switches
    register: collect
  - name: write the finding to the work notes
    http.request:
      target: snow
      url: "/api/now/table/incident/{{ result.incident.json.result[0].sys_id | urlencode }}"
      method: PATCH
      headers:
        Content-Type: application/json
      body: '{"work_notes": {{ result.collect.stdout | to_json }}}'
    when_cel: "result.incident.json.result[0].priority == '1 - Critical'"
`

// snowInstance is a Table API endpoint: ServiceNo! or a real instance.
type snowInstance struct {
	base, user, password string
	// caFile is the CA the CLI must trust for base, or "" for the system
	// store (a real instance).
	caFile string
	client *http.Client
}

// startServiceNo runs ServiceNo! in a container over TLS and returns it.
func startServiceNo(t *testing.T) snowInstance {
	t.Helper()
	caFile, cert := gateIssue(t)
	files := t.TempDir()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	keyDER, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	for name, body := range map[string][]byte{"cert.pem": certPEM, "key.pem": keyPEM} {
		if err := os.WriteFile(filepath.Join(files, name), body, 0o644); err != nil { // #nosec G306 -- a throwaway test key the container user must read
			t.Fatal(err)
		}
	}
	script, err := filepath.Abs("../../tests/serviceno/serviceno.py")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "python:3.12-alpine3.20",
			ExposedPorts: []string{"8443/tcp"},
			Files: []testcontainers.ContainerFile{
				{HostFilePath: script, ContainerFilePath: "/serviceno/serviceno.py", FileMode: 0o644},
				{HostFilePath: filepath.Join(files, "cert.pem"), ContainerFilePath: "/serviceno/cert.pem", FileMode: 0o644},
				{HostFilePath: filepath.Join(files, "key.pem"), ContainerFilePath: "/serviceno/key.pem", FileMode: 0o644},
			},
			Cmd: []string{"python3", "/serviceno/serviceno.py", "--port", "8443", "--password", serviceNoPassword,
				"--tls-cert", "/serviceno/cert.pem", "--tls-key", "/serviceno/key.pem"},
			// Any HTTP answer through the mapped port will do (the mock
			// refuses an unauthenticated request); the log line alone is
			// said inside the container (FAILURE_PATTERNS 408).
			WaitingFor: wait.ForAll(
				wait.ForLog("ServiceNo! listening").WithStartupTimeout(2*time.Minute),
				wait.ForHTTP("/").WithPort("8443/tcp").WithTLS(true).WithAllowInsecure(true).
					WithStatusCodeMatcher(func(int) bool { return true }).
					WithStartupTimeout(2*time.Minute),
			),
		},
		Started: true,
	})
	if err != nil {
		_ = testcontainers.TerminateContainer(ctr) // a failed start still returns its container
		t.Fatalf("starting ServiceNo!: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })
	port, err := ctr.MappedPort(ctx, "8443/tcp")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	caPEM, err := os.ReadFile(caFile) // #nosec G304 -- a path under this test's own t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	pool.AppendCertsFromPEM(caPEM)
	return snowInstance{
		// The certificate names 127.0.0.1, and the mapped port listens there.
		base: "https://" + net.JoinHostPort("127.0.0.1", port.Port()), user: "admin", password: serviceNoPassword, caFile: caFile,
		client: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}},
	}
}

// snowUnderTest returns a real instance when the environment names one,
// and ServiceNo! otherwise.
func snowUnderTest(t *testing.T) snowInstance {
	t.Helper()
	if base := os.Getenv("PLEIADES_SNOW_INSTANCE"); base != "" {
		return snowInstance{base: strings.TrimRight(base, "/"), user: os.Getenv("PLEIADES_SNOW_USER"), password: os.Getenv("PLEIADES_SNOW_PASSWORD"), client: http.DefaultClient}
	}
	return startServiceNo(t)
}

// call makes one Table API call, failing the test unless it answers want,
// and returns the response's result.
func (s snowInstance) call(t *testing.T, method, path string, body any, want int) any {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, s.base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(s.user, s.password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s answered %d, want %d: %s", method, path, resp.StatusCode, want, raw)
	}
	var envelope struct {
		Result any `json:"result"`
	}
	_ = json.Unmarshal(raw, &envelope)
	return envelope.Result
}

// create inserts a record into table, deletes it when the test ends, and
// returns its sys_id and, for a numbered table, its number.
func (s snowInstance) create(t *testing.T, table string, fields map[string]any) (sysID, number string) {
	t.Helper()
	record, _ := s.call(t, http.MethodPost, "/api/now/table/"+table, fields, http.StatusCreated).(map[string]any)
	sysID, _ = record["sys_id"].(string)
	number, _ = record["number"].(string)
	t.Cleanup(func() {
		req, _ := http.NewRequest(http.MethodDelete, s.base+"/api/now/table/"+table+"/"+sysID, nil)
		req.SetBasicAuth(s.user, s.password)
		if resp, err := s.client.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	})
	return sysID, number
}

// workNotes returns the work notes written to one incident, from
// sys_journal_field, where the Table API keeps them.
func (s snowInstance) workNotes(t *testing.T, sysID string) []string {
	t.Helper()
	rows, _ := s.call(t, http.MethodGet, "/api/now/table/sys_journal_field?sysparm_query=element_id%3D"+sysID+"%5Eelement%3Dwork_notes&sysparm_fields=value", nil, http.StatusOK).([]any)
	var out []string
	for _, row := range rows {
		if m, ok := row.(map[string]any); ok {
			v, _ := m["value"].(string)
			out = append(out, v)
		}
	}
	return out
}

// TestServiceNowRunbookReleaseGate runs the ticket runbook against the
// ServiceNow Table API: a critical incident whose configuration item is a
// switch gets that switch's finding in its work notes; a moderate one is
// collected and left alone; one naming a device outside the bound is
// refused before any device is reached.
func TestServiceNowRunbookReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the ServiceNow runbook release gate container test in short mode")
	}
	snow := snowUnderTest(t)
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	sw, dc := "sw-gate-"+hex.EncodeToString(suffix), "dc-gate-"+hex.EncodeToString(suffix)
	swID, _ := snow.create(t, "cmdb_ci", map[string]any{"name": sw})
	dcID, _ := snow.create(t, "cmdb_ci", map[string]any{"name": dc})

	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home}
	if snow.caFile != "" {
		env["SSL_CERT_FILE"], env["SSL_CERT_DIR"] = snow.caFile, filepath.Dir(snow.caFile)
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "snow", "--type", "generic_http", "--set", "base_url=" + snow.base, "--set", "http_auth=basic"},
		{"add-credential", "snow", "--username", snow.user, "--password", snow.password},
		{"add-host", sw, "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port), "--tags", "switches"},
		{"add-credential", sw, "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
		{"add-host", dc, "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port), "--tags", "servers"},
		{"add-credential", dc, "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	onboardJSON(t, dir, env, "snow")
	writeFile(t, dir, "runbooks/snow.yaml", snowRunbook)
	run := func(number string) (string, error) {
		t.Helper()
		return runPleiadesWithEnv(t, dir, env, "run", "runbooks/snow.yaml", "-e", "ticket="+number)
	}

	critical, criticalNumber := snow.create(t, "incident", map[string]any{"short_description": "Interface down on the core switch", "cmdb_ci": swID, "priority": "1"})
	out, err := run(criticalNumber)
	if err != nil {
		t.Fatalf("the runbook failed on a critical incident: %v\n%s", err, out)
	}
	if notes := snow.workNotes(t, critical); len(notes) != 1 || strings.TrimSpace(notes[0]) != "Linux" {
		t.Fatalf("the critical incident's work notes are %q, want the finding collected from %s (Linux)\n%s", notes, sw, out)
	}
	if strings.Contains(out, snow.password) || strings.Contains(out, releaseGateSSHPassword) {
		t.Error("the run printed a credential")
	}

	moderate, moderateNumber := snow.create(t, "incident", map[string]any{"short_description": "Question about the core switch", "cmdb_ci": swID, "priority": "3"})
	if out, err := run(moderateNumber); err != nil {
		t.Fatalf("the runbook failed on a moderate incident: %v\n%s", err, out)
	}
	if notes := snow.workNotes(t, moderate); len(notes) != 0 {
		t.Errorf("a moderate incident was written to: %q", notes)
	}

	outside, outsideNumber := snow.create(t, "incident", map[string]any{"short_description": "Server down", "cmdb_ci": dcID, "priority": "1"})
	out, err = run(outsideNumber)
	if err == nil || !strings.Contains(out, "which is not a device within \"switches\"") {
		t.Fatalf("an incident naming a device outside the bound: err = %v\n%s", err, out)
	}
	if notes := snow.workNotes(t, outside); len(notes) != 0 {
		t.Errorf("a refused run wrote to the incident: %q", notes)
	}
}
