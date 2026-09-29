// Tests for http.request's json stat (Phase 117a), against real HTTP
// servers in this process, as the rest of this package's tests are.
package http_test

import (
	"io"
	nethttp "net/http"
	"strings"
	"testing"
)

// TestRequest_RecordsAJSONBodyDecoded: a body the server says is JSON is
// also recorded decoded, so a later task can read one field of a ticket;
// the text stays in content either way.
func TestRequest_RecordsAJSONBodyDecoded(t *testing.T) {
	for _, contentType := range []string{"application/json", "application/json; charset=utf-8", "application/vnd.api+json"} {
		server := requestServer(t, func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			w.Header().Set("Content-Type", contentType)
			_, _ = io.WriteString(w, `{"result":{"number":"INC0010001","cmdb_ci":"core-sw1","priority":2}}`)
		})
		rc := newRequestContext()
		if _, err := requestRun(rc, map[string]any{"url": server.URL + "/ticket"}); err != nil {
			t.Fatalf("%s: %v", contentType, err)
		}
		body, ok := rc.stats["json"].(map[string]any)
		if !ok {
			t.Fatalf("%s: json stat = %#v, want the decoded body", contentType, rc.stats["json"])
		}
		result, _ := body["result"].(map[string]any)
		if result["cmdb_ci"] != "core-sw1" || result["priority"] != 2.0 {
			t.Errorf("%s: decoded body = %#v", contentType, body)
		}
		if !strings.Contains(rc.stats["content"].(string), "INC0010001") {
			t.Errorf("%s: content no longer holds the text", contentType)
		}
	}
}

// TestRequest_LeavesJSONOutWhenItIsNot: a body that is not declared JSON,
// declared JSON and malformed, empty, or over the decode bound is recorded
// as text only, and the task does not fail for it.
func TestRequest_LeavesJSONOutWhenItIsNot(t *testing.T) {
	big := `{"k":"` + strings.Repeat("x", 1<<20) + `"}`
	for name, tc := range map[string]struct{ contentType, body string }{
		"text":      {"text/plain", `{"looks":"like json"}`},
		"malformed": {"application/json", `{"unterminated":`},
		"trailing":  {"application/json", `{"a":1} {"b":2}`},
		"empty":     {"application/json", ``},
		"too large": {"application/json", big},
		"no type":   {"", `{"a":1}`},
	} {
		server := requestServer(t, func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			if tc.contentType != "" {
				w.Header().Set("Content-Type", tc.contentType)
			} else {
				// net/http sniffs a type when none is set; this one stays
				// absent only when the header is present and empty.
				w.Header()["Content-Type"] = nil
			}
			_, _ = io.WriteString(w, tc.body)
		})
		rc := newRequestContext()
		if _, err := requestRun(rc, map[string]any{"url": server.URL + "/x"}); err != nil {
			t.Fatalf("%s: the task failed: %v", name, err)
		}
		if _, present := rc.stats["json"]; present {
			t.Errorf("%s: json was recorded for a body that is not decodable JSON within the bound", name)
		}
	}
}

// TestRequest_RefusesABodyOverTheBound: a body over requestMaxBodyBytes
// fails the task rather than exhausting memory or being cut short, and one
// at the bound is read whole.
func TestRequest_RefusesABodyOverTheBound(t *testing.T) {
	const limit = 16 << 20
	for _, tc := range []struct {
		size    int
		wantErr bool
	}{{limit, false}, {limit + 1, true}} {
		size := tc.size
		server := requestServer(t, func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			_, _ = io.WriteString(w, strings.Repeat("x", size))
		})
		rc := newRequestContext()
		_, err := requestRun(rc, map[string]any{"url": server.URL + "/big"})
		if tc.wantErr {
			if err == nil || !strings.Contains(err.Error(), "larger than 16 MiB") {
				t.Errorf("a %d-byte body: error = %v, want the bound refusal", size, err)
			}
			continue
		}
		if err != nil || len(rc.stats["content"].(string)) != size {
			t.Errorf("a %d-byte body: error = %v, content %d bytes", size, err, len(rc.stats["content"].(string)))
		}
	}
}
