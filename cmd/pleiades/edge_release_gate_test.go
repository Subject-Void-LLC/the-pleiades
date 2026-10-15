// Release gate for the edge collection, through the real binary: the
// edge_lab sample, its account pointed at a real HTTPS server in this
// process that answers as Cloudflare's API documents, onboarded with a
// token on standard input; validate accepts it; a check and a run read
// and pass; a run against a changed account names every difference; and
// the token appears in no output and no journal.
package main_test

import (
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cfapi/cfapitest"
)

// TestCLI_EdgeExampleReadsAndNamesEveryDifference is the release gate.
func TestCLI_EdgeExampleReadsAndNamesEveryDifference(t *testing.T) {
	const acct = "00000000000000000000000000000000"
	const token = "cf-gate-sentinel-4417"
	srv := cfapitest.New(t, acct, token)
	srv.Tunnels = []map[string]any{{"id": "11111111-2222-3333-4444-555555555555", "name": "prod", "status": "healthy"}}
	srv.Connectors["11111111-2222-3333-4444-555555555555"] = []map[string]any{
		{"id": "c1", "version": "2026.9.0", "arch": "linux_amd64", "conns": []map[string]any{{"colo_name": "fra01"}}},
		{"id": "c2", "version": "2026.9.0", "arch": "linux_amd64", "conns": []map[string]any{{"colo_name": "ams01"}}},
	}
	srv.Apps = []map[string]any{{"id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "name": "Admin", "domain": "admin.example.com", "aud": "audtag",
		"policies": []map[string]any{{"id": "p1", "name": "staff", "decision": "allow", "include": []any{map[string]any{"email_domain": map[string]any{"domain": "example.com"}}}}}}}
	srv.AuthDomain = "example.cloudflareaccess.com"
	srv.Zones = []map[string]any{{"id": "00000000000000000000000000000001", "name": "example.com", "account": map[string]any{"id": acct}}}
	srv.Records["00000000000000000000000000000001"] = []map[string]any{
		{"type": "A", "name": "www.example.com", "content": "192.0.2.10", "proxied": true, "ttl": 1},
		{"type": "A", "name": "www.example.com", "content": "192.0.2.11", "proxied": true, "ttl": 1},
	}

	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "examples", "edge_lab", "pleiades"))); err != nil {
		t.Fatal(err)
	}
	inv := filepath.Join(dir, "inventory.yaml")
	data, err := os.ReadFile(inv)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})))
	pointed := strings.Replace(string(data), "api_base: https://api.cloudflare.com/client/v4",
		"api_base: "+srv.URL+"/client/v4\n      tls_ca_pem: |\n        "+strings.ReplaceAll(caPEM, "\n", "\n        "), 1)
	if pointed == string(data) {
		t.Fatal("the sample's api_base was not found to point elsewhere")
	}
	if err := os.WriteFile(inv, []byte(pointed), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiadesWithStdin(t, dir, token+"\n", "add-credential", "prod-cf", "--token-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	var outputs []string
	if out, err := runPleiadesWithEnv(t, dir, nil, "onboard", "prod-cf"); err != nil {
		t.Fatalf("onboard: %v\n%s", err, out)
	} else {
		outputs = append(outputs, out)
	}
	if out, err := runPleiadesWithEnv(t, dir, nil, "validate", "runbooks/edge.yaml"); err != nil || !strings.Contains(out, "no issues found") {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	for _, mode := range []string{"check", "execute"} {
		rep, raw, code := runPleiadesJSON(t, dir, "run", "runbooks/edge.yaml", "--mode", mode)
		outputs = append(outputs, raw)
		if code != 0 || len(rep.Tasks) != 3 {
			t.Fatalf("%s: exit %d\n%s", mode, code, raw)
		}
		for _, task := range rep.Tasks {
			if task.Status != "ok" {
				t.Errorf("%s: task %s is %s", mode, task.Name, task.Status)
			}
		}
	}

	srv.Tunnels[0]["status"] = "degraded"
	srv.Connectors["11111111-2222-3333-4444-555555555555"] = srv.Connectors["11111111-2222-3333-4444-555555555555"][:1]
	srv.Records["00000000000000000000000000000001"][1]["proxied"] = false
	rep, raw, code := runPleiadesJSON(t, dir, "run", "runbooks/edge.yaml")
	outputs = append(outputs, raw)
	if code == 0 {
		t.Fatalf("a changed account passed\n%s", raw)
	}
	// One task names both of its differences; a failed task ends the
	// device's run, so the next difference shows once the tunnel is mended.
	if !strings.Contains(rep.Tasks[0].Error, "degraded, not healthy") || !strings.Contains(rep.Tasks[0].Error, "fewer than 2") {
		t.Errorf("tunnel: %s", rep.Tasks[0].Error)
	}
	srv.Tunnels[0]["status"] = "healthy"
	srv.Connectors["11111111-2222-3333-4444-555555555555"] = append(srv.Connectors["11111111-2222-3333-4444-555555555555"],
		map[string]any{"id": "c3", "conns": []map[string]any{{"colo_name": "lhr01"}}})
	rep, raw, code = runPleiadesJSON(t, dir, "run", "runbooks/edge.yaml")
	outputs = append(outputs, raw)
	if code == 0 || len(rep.Tasks) != 3 || rep.Tasks[0].Status != "ok" || rep.Tasks[1].Status != "ok" || !strings.Contains(rep.Tasks[2].Error, "192.0.2.11 is proxied false, not true") {
		t.Errorf("dns: exit %d\n%s", code, raw)
	}

	journal := filepath.Join(dir, ".pleiades", "journal")
	_ = filepath.WalkDir(journal, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, readErr := os.ReadFile(path); readErr == nil {
				outputs = append(outputs, string(b))
			}
		}
		return nil
	})
	for _, out := range outputs {
		if strings.Contains(out, token) {
			t.Fatal("the token is in an output or the journal")
		}
	}
}
