// A GNS3 server's own words reach the operator's terminal escaped, never as
// control sequences, through the real binary.
package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCLI_ImportGNS3EscapesTheServersWords: a server whose version and node
// status carry terminal escape sequences (a hostile server, or one on a
// path nobody secured; GNS3 has no TLS pinning) has them printed visibly,
// \x1b and all, rather than acted on by the terminal.
func TestCLI_ImportGNS3EscapesTheServersWords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/version":
			_, _ = w.Write([]byte(`{"version":"2.2.61\u001b[2J"}`))
		case "/v2/projects":
			_, _ = w.Write([]byte(`[{"project_id":"p","name":"lab","status":"opened"}]`))
		case "/v2/projects/p/nodes":
			_, _ = w.Write([]byte(`[{"node_id":"n","name":"pc1","node_type":"vpcs","console":5000,"console_type":"telnet","console_host":"127.0.0.1","status":"started\u001b[31m"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	out, err := runPleiades(t, dir, "import-gns3", "--server", srv.URL, "--project", "lab")
	if err != nil {
		t.Fatalf("import-gns3: %v\n%s", err, out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("the server's escape sequences reached the terminal raw:\n%q", out)
	}
	if !strings.Contains(out, `2.2.61\x1b[2J`) || !strings.Contains(out, `started\x1b[31m`) {
		t.Errorf("the server's words are not shown escaped:\n%s", out)
	}
}
