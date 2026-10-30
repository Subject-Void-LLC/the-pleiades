// pleiades import-containerlab --api, through the commands a person runs: the
// server added as a device with its login, onboarded (recognised by its
// /health), and a lab imported from its inspect. The server answers in the
// shapes the real containerlab API server does.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// clabInspectServer answers like a containerlab API server with one lab.
func clabInspectServer(t *testing.T) *httptest.Server {
	t.Helper()
	const token = "tok-0123456789abcdef"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"healthy","uptime":"5s","startTime":"2026-10-30T22:56:52Z","version":"v0.9.0"}`))
	})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Username, Password string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Username != "pleiades" || in.Password != "swordfish-0042" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Invalid username or password"}`))
			return
		}
		_, _ = w.Write([]byte(`{"token":"` + token + `"}`))
	})
	mux.HandleFunc("GET /api/v1/labs/{lab}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.PathValue("lab") != "pltest" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"lab '` + r.PathValue("lab") + `' not found"}`))
			return
		}
		_, _ = w.Write([]byte(`[
		 {"name":"clab-pltest-n1","container_id":"3db3a8af8054","image":"alpine:3.20.10","kind":"linux","state":"running","status":"Up 9 seconds","ipv4_address":"172.20.20.2/24","ipv6_address":"3fff:172:20:20::2/64","lab_name":"pltest","nodeName":"n1","owner":"pleiades","mgmt_network":"clab"},
		 {"name":"clab-pltest-n2","container_id":"44a312d81748","image":"alpine:3.20.10","kind":"linux","state":"running","status":"Up 9 seconds","ipv4_address":"172.20.20.3/24","ipv6_address":"","lab_name":"pltest","nodeName":"n2","owner":"pleiades","mgmt_network":"clab","ports":[{"host_ip":"0.0.0.0","host_port":2202,"port":22,"protocol":"tcp"}]}]`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestImportContainerlab_FromTheAPIServer: the lab's nodes become
// ephemeral generic_ssh devices at the address inspect gives (a published SSH
// port over the management address), a lab the server does not have is named
// as missing, and a device onboarding did not recognise is refused.
func TestImportContainerlab_FromTheAPIServer(t *testing.T) {
	srv := clabInspectServer(t)
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		for _, step := range []struct {
			name string
			fn   func([]string) error
			args []string
		}{
			{"add-host", runAddHost, []string{"clab-api", "--dir", dir, "--type", "generic_http", "--set", "base_url=" + srv.URL, "--set", "http_auth=basic", "--set", "http_allow_plaintext_credentials=true"}},
			{"add-host (plain)", runAddHost, []string{"plain-api", "--dir", dir, "--type", "generic_http", "--set", "base_url=" + srv.URL, "--set", "http_auth=basic", "--set", "http_allow_plaintext_credentials=true"}},
			{"add-credential", runAddCredential, []string{"clab-api", "--dir", dir, "--username", "pleiades", "--password", "swordfish-0042"}},
			{"onboard", runOnboard, []string{"clab-api", "--dir", dir}},
		} {
			if err := step.fn(step.args); err != nil {
				t.Fatalf("%s: %v", step.name, err)
			}
		}
	})

	// A device onboarding never recognised is refused.
	if err := runImportContainerlab([]string{"--api", "plain-api", "--lab", "pltest", "--dir", dir}); err == nil || !strings.Contains(err.Error(), "not a containerlab API server") {
		t.Errorf("an unrecognised device: %v", err)
	}
	// A lab the server does not have is named as missing.
	if err := runImportContainerlab([]string{"--api", "clab-api", "--lab", "ghost", "--dir", dir}); err == nil || !strings.Contains(err.Error(), "no lab") {
		t.Errorf("an absent lab: %v", err)
	}
	// Neither or both sources is a usage error.
	if err := runImportContainerlab([]string{"--lab", "pltest", "--dir", dir}); err == nil {
		t.Error("no source was accepted")
	}
	if err := runImportContainerlab([]string{"--api", "clab-api", "--topology", "x.json", "--lab", "pltest", "--dir", dir}); err == nil {
		t.Error("two sources were accepted")
	}

	var err error
	out := captureStdout(t, func() {
		err = runImportContainerlab([]string{"--api", "clab-api", "--lab", "pltest", "--dir", dir, "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	var res clabResult
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil {
		t.Fatalf("the report is not JSON: %v\n%s", jerr, out)
	}
	got := map[string]clabImported{}
	for _, n := range res.Nodes {
		got[n.Device] = n
	}
	if len(got) != 2 || got["n1"].Host != "172.20.20.2" || got["n1"].Port != 22 || got["n2"].Host != "127.0.0.1" || got["n2"].Port != 2202 {
		t.Errorf("imported %+v", got)
	}
	for _, name := range []string{"n1", "n2"} {
		item := loadHost(t, dir, name)
		if !isEphemeral(item) {
			t.Errorf("%s is not flagged ephemeral", name)
		}
	}
}
