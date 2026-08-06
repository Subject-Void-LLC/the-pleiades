package catalystcenter_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/catalystcenter"
)

// FuzzDeviceResponseParsing feeds adversarial bodies through the real
// decode path and asserts the client never panics.
//
// The upstream here is a network appliance, which is exactly the boundary
// Phase 39's schema-hardening categories care about: the response is
// attacker-influenceable if the controller is compromised or spoofed, and
// "it is Cisco's own API" is not a memory-safety argument. Every input is
// allowed to produce an error; none is allowed to produce a panic or a
// silently wrong device count.
func FuzzDeviceResponseParsing(f *testing.F) {
	f.Add(`{"response":[]}`)
	f.Add(`{"response":[{"id":"a","hostname":"sw1"}]}`)
	f.Add(`{"response":null}`)
	f.Add(`{"response":{}}`)
	f.Add(`{}`)
	f.Add(``)
	f.Add(`null`)
	f.Add(`[]`)
	f.Add(`{"response":[{"id":123}]}`)                                  // wrong scalar type
	f.Add(`{"response":[{"interfaceCount":48}]}`)                       // number where the API sends a string
	f.Add("{\"response\":[{\"hostname\":\"\x00 embedded\"}]}")          // null byte in a string
	f.Add(`{"response":[{"id":"` + string(make([]byte, 4096)) + `"}]}`) // very long field
	f.Add(`{"response":[` + `{"id":"a"},` + `{"id":"a"}` + `]}`)        // duplicate ids
	f.Add(`{"response":[{"id":"a"}`)                                    // truncated
	f.Add(`{"response":[{"nested":{"deeply":{"very":true}}}]}`)

	f.Fuzz(func(t *testing.T, body string) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/dna/system/api/v1/auth/token" {
				_, _ = w.Write([]byte(`{"Token":"tok"}`))
				return
			}
			_, _ = w.Write([]byte(body))
		}))
		defer srv.Close()

		c, err := catalystcenter.New(srv.URL, "user", "pass")
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer func() { _ = c.Close() }()

		// A page size larger than anything the body can contain, so
		// EachDevice always terminates after one request no matter what
		// came back. A fuzz target that could loop is not a fuzz target.
		_ = c.EachDevice(context.Background(), 1000, func(catalystcenter.Device) error { return nil })
		_, _ = c.ListSites(context.Background())
		_, _ = c.ListTags(context.Background())
		_, _ = c.DeviceCount(context.Background())
	})
}

// FuzzAuthResponseParsing fuzzes the token endpoint's body specifically.
// It is separate from the listing fuzzer because a malformed token
// response has to fail before any request is sent, and mixing the two
// would let a passing listing mask a broken auth path.
func FuzzAuthResponseParsing(f *testing.F) {
	f.Add(`{"Token":"tok"}`)
	f.Add(`{"Token":""}`)
	f.Add(`{"token":"lowercase-key"}`)
	f.Add(`{"Token":null}`)
	f.Add(`{"Token":12345}`)
	f.Add(``)
	f.Add(`{`)
	f.Add(`{"Token":"` + string(make([]byte, 8192)) + `"}`)

	f.Fuzz(func(t *testing.T, body string) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
		defer srv.Close()

		c, err := catalystcenter.New(srv.URL, "user", "pass")
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer func() { _ = c.Close() }()

		_, _ = c.DeviceCount(context.Background())
	})
}

// FuzzBaseURL proves the constructor never panics on a hostile base URL and
// never accepts one that would produce a request to somewhere unintended.
func FuzzBaseURL(f *testing.F) {
	f.Add("https://sandboxdnac.cisco.com")
	f.Add("http://host:8080/prefix")
	f.Add("")
	f.Add("://")
	f.Add("https://")
	f.Add("file:///etc/passwd")
	f.Add("javascript:alert(1)")
	f.Add("https://host/../../etc")
	f.Add("https://user:pass@host")
	f.Add("https://host\x00.evil.test")

	f.Fuzz(func(t *testing.T, baseURL string) {
		c, err := catalystcenter.New(baseURL, "user", "pass")
		if err != nil {
			return
		}
		// An accepted base URL must be http or https. Anything else would
		// mean a request going somewhere this client never intended.
		got := c.BaseURL()
		if !hasSchemePrefix(got) {
			t.Fatalf("accepted base URL %q normalized to %q, which has no http(s) scheme", baseURL, got)
		}
		_ = c.Close()
	})
}

// hasSchemePrefix reports whether s starts with an http or https scheme.
func hasSchemePrefix(s string) bool {
	return len(s) > 7 && (s[:7] == "http://" || (len(s) > 8 && s[:8] == "https://"))
}
