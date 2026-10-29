// Tests of how a Batfish refusal reaches an error: the service's whole
// message on one line, not its first line, bounded and quoted.
package batfish

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDo_SaysWhatBatfishSaid serves the two shapes Batfish's v2 API answers
// an error with: a plain sentence, and a Java exception whose reason follows
// its class name on the same line and whose stack follows on the next ones.
// The whole reason reaches the error, a control character is shown, and a
// stack is bounded rather than copied whole.
func TestDo_SaysWhatBatfishSaid(t *testing.T) {
	stack := "java.lang.IllegalArgumentException: Snapshot 'candidate' already exists\n" +
		strings.Repeat("\tat org.batfish.coordinator.WorkMgr.initSnapshot(WorkMgr.java:1712)\n", 20)
	answers := map[string][2]string{
		"/v2/networks/missing": {"404", "Network 'missing' does not exist\n"},
		"/v2/networks/stack":   {"400", stack},
		"/v2/networks/escape":  {"500", "bad\x1b[2Jthing"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := answers[r.URL.Path]
		w.Header().Set("Content-Type", "text/plain")
		switch a[0] {
		case "404":
			w.WriteHeader(http.StatusNotFound)
		case "400":
			w.WriteHeader(http.StatusBadRequest)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
		_, _ = w.Write([]byte(a[1]))
	}))
	defer srv.Close()
	c, err := New(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for path, want := range map[string]string{
		"/v2/networks/missing": `404 Not Found: "Network 'missing' does not exist"`,
		"/v2/networks/stack":   `"java.lang.IllegalArgumentException: Snapshot 'candidate' already exists at org.batfish`,
		"/v2/networks/escape":  `"bad\x1b[2Jthing"`,
	} {
		_, err := c.do(ctx, http.MethodGet, path, nil, nil, "")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("GET %s = %v, want it to hold %s", path, err, want)
		}
		if err != nil && (strings.ContainsRune(err.Error(), 0x1b) || len(err.Error()) > 400) {
			t.Errorf("GET %s: the error is unbounded or unescaped: %q", path, err)
		}
	}
}
