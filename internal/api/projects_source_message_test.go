// This file holds a refused project source to a message that repeats
// nothing of the URL, through the real router and the real ent-backed store.
package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestProjects_ARefusedSourceNeverRepeatsItsURL creates projects whose URL
// is refused, and holds every refusal to a message that repeats neither the
// URL nor anything in it. The source parser's own error quotes the URL it
// was given, password and all ("parse \"https://user:pa/ss@host/r\": invalid
// port"), and the scrub in front of it could not clean every shape.
func TestProjects_ARefusedSourceNeverRepeatsItsURL(t *testing.T) {
	f := newProjectFixture(t)
	for name, url := range map[string]string{
		"a password holding a slash": "https://svc:pa/hunter2@example.invalid/r.git",
		"a bad port":                 "https://svc:hunter2@example.invalid:badport/r.git",
		"a token in the query":       "https://example.invalid/r.git?access_token=hunter2",
		// No credential at all, and still not repeated: a URL the parser
		// cannot read is refused in a fixed phrase.
		"an unreadable URL":   "https://example.invalid:badport/hunter2.git",
		"a space in the host": "https://exa mple.invalid/hunter2.git",
	} {
		for _, scmType := range []string{"git", "manual"} {
			t.Run(name+", "+scmType, func(t *testing.T) {
				if name == "an unreadable URL" || name == "a space in the host" {
					if scmType != "git" {
						t.Skip("only a git project's source is parsed")
					}
				}
				body, err := json.Marshal(map[string]any{"name": "refused " + name + " " + scmType, "organization": f.orgID, "scm_type": scmType, "scm_url": url})
				if err != nil {
					t.Fatal(err)
				}
				status, resp := f.do(t, http.MethodPost, "/api/v1/projects", json.RawMessage(body))
				if status != http.StatusBadRequest {
					t.Fatalf("status %d, want 400; body %s", status, resp)
				}
				if strings.Contains(string(resp), "hunter2") || strings.Contains(string(resp), "example.invalid") {
					t.Errorf("the refusal repeats the URL: %s", resp)
				}
			})
		}
	}
}
