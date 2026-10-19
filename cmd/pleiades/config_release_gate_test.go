// Release gate for file.json, file.yaml and http.check through the real
// binary: a device that is a real SSH server (in this process, its
// commands run by this machine's /bin/sh) and a real HTTPS server. A check
// writes nothing; the run sets one key in each file keeping the rest; a
// second run changes nothing; and the health check passes on the body it
// read, which is in no output.
package main_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// TestCLI_ConfigEditsKeepTheRestAndCheckTheApp is the release gate.
func TestCLI_ConfigEditsKeepTheRestAndCheckTheApp(t *testing.T) {
	need.POSIXShell(t)
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(knownhosts.Line([]string{net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))}, srv.HostKey)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{remoteexec.KnownHostsEnv: knownHosts}
	const bodySentinel = "HEALTH-BODY-9917"
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","build":"` + bodySentinel + `"}`))
	}))
	defer app.Close()

	files := t.TempDir()
	jsonPath, yamlPath := filepath.Join(files, "config.json"), filepath.Join(files, "workers.yaml")
	jsonIn := "{\n  \"id\": 12345678901234567890,\n  \"server\": {\n    \"port\": 80\n  },\n  \"features\": {\n    \"legacy.mode\": true,\n    \"fast\": true\n  }\n}\n"
	yamlIn := "# the worker pool\npool:\n  size: 2  # raised for the sale\n  queue: default\n"
	if err := os.WriteFile(jsonPath, []byte(jsonIn), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yamlPath, []byte(yamlIn), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "examples", "config_lab", "pleiades"))); err != nil {
		t.Fatal(err)
	}
	rbPath := filepath.Join(dir, "runbooks", "config.yaml")
	rb, err := os.ReadFile(rbPath)
	if err != nil {
		t.Fatal(err)
	}
	pointed := strings.NewReplacer("/etc/app/config.json", jsonPath, "/etc/app/workers.yaml", yamlPath, "https://app.example.com/healthz", app.URL+"/healthz").Replace(string(rb))
	if err := os.WriteFile(rbPath, []byte(pointed), 0o644); err != nil {
		t.Fatal(err)
	}
	inv := filepath.Join(dir, "inventory.yaml")
	data, _ := os.ReadFile(inv)
	data = []byte(strings.NewReplacer("host: 127.0.0.1", "host: "+srv.Host, "port: 2222", "port: "+strconv.Itoa(srv.Port)).Replace(string(data)))
	if err := os.WriteFile(inv, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiadesWithEnv(t, dir, env, "add-credential", "app-1", "--username", srv.Username, "--password", srv.Password); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithEnv(t, dir, env, "validate", "runbooks/config.yaml"); err != nil || !strings.Contains(out, "no issues found") {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	check, err := runPleiadesWithEnv(t, dir, env, "run", "runbooks/config.yaml", "--mode", "check", "--json")
	if err != nil || strings.Count(check, `"would_change"`) != 3 {
		t.Fatalf("check: %v\n%s", err, check)
	}
	if got, _ := os.ReadFile(jsonPath); string(got) != jsonIn {
		t.Fatal("a check wrote the JSON file")
	}
	if got, _ := os.ReadFile(yamlPath); string(got) != yamlIn {
		t.Fatal("a check wrote the YAML file")
	}
	out, err := runPleiadesWithEnv(t, dir, env, "run", "runbooks/config.yaml", "--json")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	wantJSON := "{\n  \"id\": 12345678901234567890,\n  \"server\": {\n    \"port\": 8443\n  },\n  \"features\": {\n    \"fast\": true\n  }\n}\n"
	if got, _ := os.ReadFile(jsonPath); string(got) != wantJSON {
		t.Errorf("JSON file\n%s\nwant\n%s", got, wantJSON)
	}
	if got, _ := os.ReadFile(yamlPath); !strings.Contains(string(got), "size: 4 # raised for the sale") || !strings.Contains(string(got), "# the worker pool") {
		t.Errorf("YAML file\n%s", got)
	}
	again, err := runPleiadesWithEnv(t, dir, env, "run", "runbooks/config.yaml", "--json")
	if err != nil || strings.Contains(again, `"changed"`) {
		t.Errorf("a second run: %v\n%s", err, again)
	}
	for _, o := range []string{check, out, again} {
		if strings.Contains(o, bodySentinel) {
			t.Fatal("the health check's body is in an output")
		}
	}
}
