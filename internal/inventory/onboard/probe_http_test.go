// Tests for the HTTP probe against real HTTPS servers: where the
// credential goes, the OpenAPI document, and the parser under fuzzing.
package onboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestHTTPProbe_CredentialStaysOnItsOrigin: the base URL redirects to
// another server, and the probe does not follow, so the other server
// never sees the request or the credential it carried.
func TestHTTPProbe_CredentialStaysOnItsOrigin(t *testing.T) {
	var elsewhere atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere.Add(1) }))
	defer other.Close()
	srv := httptest.NewTLSServer(http.RedirectHandler(other.URL, http.StatusFound))
	defer srv.Close()

	dev := build(t, generic.TypeHTTP, map[string]inventory.PropertyValue{devicetls.CAPEMProperty: caPEM(srv), generic.BaseURLProperty: srv.URL, generic.HTTPAuthProperty: httpapi.AuthBearer})
	got, err := httpProber{}.Probe(context.Background(), dev, map[string]string{"password": "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if elsewhere.Load() != 0 {
		t.Errorf("the other origin received %d requests", elsewhere.Load())
	}
	if got.Facts["status"] != "302" {
		t.Errorf("facts %v", got.Facts)
	}
}

// TestHTTPProbe_UntrustedCertificateProvesNothing: the registered prober
// verifies certificates against the system's roots, and a test server's
// certificate is not among them.
func TestHTTPProbe_UntrustedCertificateProvesNothing(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	dev := build(t, generic.TypeHTTP, map[string]inventory.PropertyValue{generic.BaseURLProperty: srv.URL})
	if _, err := (httpProber{}).Probe(context.Background(), dev, nil); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("err %v, want a certificate refusal", err)
	}
}

// TestHTTPProbe_ReadsTheOpenAPIDocument records the document's version,
// title and declared paths, and sends the credential with it.
func TestHTTPProbe_ReadsTheOpenAPIDocument(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/openapi.json" {
			_, _ = w.Write([]byte(`{"openapi":"3.0.3","info":{"title":"Stand-in","version":"2.1"},"paths":{"/b":{},"/a":{}}}`))
		}
	}))
	defer srv.Close()
	dev := build(t, generic.TypeHTTP, map[string]inventory.PropertyValue{
		devicetls.CAPEMProperty: caPEM(srv), generic.BaseURLProperty: srv.URL + "/api", generic.HTTPAuthProperty: httpapi.AuthBearer, generic.OpenAPIPathProperty: "/openapi.json",
	})
	got, err := httpProber{}.Probe(context.Background(), dev, map[string]string{"password": "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Facts["openapi"] != "3.0.3" || got.Facts["title"] != "Stand-in" || got.Facts["api_version"] != "2.1" {
		t.Errorf("facts %v", got.Facts)
	}
	if paths, _ := got.Facts["paths"].([]any); !slices.Equal(paths, []any{"/a", "/b"}) {
		t.Errorf("paths %v", paths)
	}
}

// TestHTTPProbe_ModeWithoutItsCredentialIsRefused: basic authentication
// with no stored credential is an error before anything is sent.
func TestHTTPProbe_ModeWithoutItsCredentialIsRefused(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	dev := build(t, generic.TypeHTTP, map[string]inventory.PropertyValue{devicetls.CAPEMProperty: caPEM(srv), generic.BaseURLProperty: srv.URL, generic.HTTPAuthProperty: httpapi.AuthBasic})
	if _, err := (httpProber{}).Probe(context.Background(), dev, nil); err == nil || hits.Load() != 0 {
		t.Fatalf("err %v after %d requests, want a refusal before any", err, hits.Load())
	}
}

// FuzzParseOpenAPI: any document parses or is refused without a panic,
// and every fact kept is bounded.
func FuzzParseOpenAPI(f *testing.F) {
	f.Add([]byte(`{"openapi":"3.1.0","info":{"title":"x","version":"1"},"paths":{"/a":{}}}`))
	f.Add([]byte(`{"swagger":"2.0","paths":{"\u001b[2J":{}}}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(t *testing.T, body []byte) {
		facts, err := parseOpenAPI(body)
		if err != nil {
			return
		}
		for k, v := range facts {
			switch v := v.(type) {
			case string:
				if len(v) > maxFactText {
					t.Fatalf("%s is %d bytes", k, len(v))
				}
			case []any:
				if len(v) > maxFactList {
					t.Fatalf("%s has %d entries", k, len(v))
				}
			default:
				t.Fatalf("%s is %T", k, v)
			}
		}
	})
}

// TestFactText strips what a terminal acts on and bounds the length.
func TestFactText(t *testing.T) {
	if got := factText("a\x1b[31mb\xe2\x80\xaec\x00d"); got != "a[31mbcd" {
		t.Errorf("got %q", got)
	}
	if got := factText(strings.Repeat("é", maxFactText)); len(got) > maxFactText {
		t.Errorf("%d bytes", len(got))
	}
	if got := factList(make([]string, 2*maxFactList)); len(got) != 0 {
		t.Errorf("empty entries were kept: %d", len(got))
	}
}

// FuzzFactText: whatever a device answers, a kept fact is valid UTF-8,
// within bounds, and holds no control or format character, so nothing a
// device says can drive a terminal or reorder text once stored.
func FuzzFactText(f *testing.F) {
	f.Add("SERVING")
	f.Add("\x1b]52;c;SGVsbG8=\x07")
	f.Add("a\xe2\x80\xaeb\xff\xfe")
	f.Fuzz(func(t *testing.T, s string) {
		for _, got := range append([]any{factText(s)}, factList([]string{s, s})...) {
			text, _ := got.(string)
			if len(text) > maxFactText || !utf8.ValidString(text) {
				t.Fatalf("%q is not bounded UTF-8", text)
			}
			for _, r := range text {
				if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
					t.Fatalf("%q keeps %U", text, r)
				}
			}
		}
	})
}
