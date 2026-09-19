// Package catalyst_test: the methods against a controller answering
// something other than the captured sandbox's own data.
//
// The fixtures in catalyst_test.go are one real fleet, all of it reachable
// and every device named. A controller answering an error, or reporting a
// device that is unreachable or has no hostname, is what these cover, and
// the unreachable one is net.catalyst.reachability's whole purpose.
package catalyst_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// answeringController starts a controller that authenticates like the
// sandbox and answers each listing path from replies, or with status 500
// for a path replies does not name.
func answeringController(t *testing.T, replies map[string]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/dna/system/api/v1/auth/token" {
			_, _ = w.Write([]byte(`{"Token":"fixture-token"}`))
			return
		}
		for path, body := range replies {
			if strings.HasPrefix(r.URL.Path, path) {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"the controller is having a bad day"}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestReachability_NamesEveryDeviceThatIsNotReachable covers the answer
// this method exists to give: a device whose status is anything but
// Reachable is listed by name, one with no hostname is listed by its
// management address, since an operator cannot act on a blank, and the
// counts distinguish reachable from managed, which are different
// questions a controller answers separately.
func TestReachability_NamesEveryDeviceThatIsNotReachable(t *testing.T) {
	const devices = `{"response":[
		{"hostname":"sw1","managementIpAddress":"10.0.0.1","reachabilityStatus":"Reachable","collectionStatus":"Managed"},
		{"hostname":"sw2","managementIpAddress":"10.0.0.2","reachabilityStatus":"Unreachable","collectionStatus":"Managed"},
		{"hostname":"","managementIpAddress":"10.0.0.3","reachabilityStatus":"Unreachable","collectionStatus":"Partial Collection Failure"}
	]}`
	url := answeringController(t, map[string]string{"/dna/intent/api/v1/network-device": devices})
	rc := newFakeContext(validSecrets())

	if _, err := invoke(t, "net.catalyst.reachability", rc, newController(t, url), nil); err != nil {
		t.Fatalf("reachability: %v", err)
	}
	unreachable, _ := rc.facts["unreachable"].([]string)
	if len(unreachable) != 2 || unreachable[0] != "sw2" || unreachable[1] != "10.0.0.3" {
		t.Errorf("unreachable = %v, want sw2 and the unnamed device's address", unreachable)
	}
	if rc.facts["reachable_count"] != 1 {
		t.Errorf("reachable_count = %v, want 1", rc.facts["reachable_count"])
	}
	if rc.facts["managed_count"] != 2 {
		t.Errorf("managed_count = %v, want 2: collection status is a separate question from reachability", rc.facts["managed_count"])
	}
	if rc.facts["total_device_count"] != 3 {
		t.Errorf("total_device_count = %v, want 3", rc.facts["total_device_count"])
	}
}

// TestMethodsReportAControllerThatFails covers a controller answering an
// error to the listing each method makes: the failure is named for the
// method, so an operator reads which task could not gather rather than an
// HTTP status alone, and no facts are stored.
func TestMethodsReportAControllerThatFails(t *testing.T) {
	url := answeringController(t, nil) // every listing answers 500
	for _, method := range []string{
		"net.catalyst.device_facts",
		"net.catalyst.site_facts",
		"net.catalyst.tag_facts",
		"net.catalyst.reachability",
	} {
		t.Run(method, func(t *testing.T) {
			rc := newFakeContext(validSecrets())
			_, err := invoke(t, method, rc, newController(t, url), nil)
			if err == nil || !strings.Contains(err.Error(), method) {
				t.Errorf("err = %v, want a failure named for %s", err, method)
			}
			if len(rc.facts) != 0 {
				t.Errorf("a failed gather stored %v", rc.facts)
			}
		})
	}
}

// TestMethodsFailWhenTheFirstFactCannotBeStored is the other end of
// TestMethodsFailWhenAFactCannotBeStored: the first emission failing stops
// the method there, rather than being reported as a gather that stored the
// rest.
func TestMethodsFailWhenTheFirstFactCannotBeStored(t *testing.T) {
	_, controller := newSandbox(t)
	for _, tc := range []struct {
		method string
		fact   string
	}{
		{method: "net.catalyst.device_facts", fact: "devices"},
		{method: "net.catalyst.site_facts", fact: "sites"},
		{method: "net.catalyst.tag_facts", fact: "tags"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			rc := newFakeContext(validSecrets())
			rc.failFact = tc.fact
			if _, err := invoke(t, tc.method, rc, controller, nil); !errors.Is(err, errEmitting) {
				t.Errorf("err = %v, want the storing failure", err)
			}
			if _, stored := rc.facts[tc.fact]; stored {
				t.Errorf("the fact that could not be stored is in the results anyway")
			}
		})
	}
}

// TestDeviceFacts_ReadsAPageSizeFromEitherNumberShape covers the tuning
// knob's two arrivals: an unquoted YAML integer decodes to int, the same
// value through JSON to float64, and both mean the same page size. The
// count the controller was asked for is read back from the request it
// received.
func TestDeviceFacts_ReadsAPageSizeFromEitherNumberShape(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{name: "an integer", value: 2, want: "2"},
		{name: "a float", value: float64(2), want: "2"},
		{name: "zero falls back", value: 0, want: "500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked := make(chan string, 4)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/dna/system/api/v1/auth/token" {
					_, _ = w.Write([]byte(`{"Token":"fixture-token"}`))
					return
				}
				select {
				case asked <- r.URL.Query().Get("limit"):
				default:
				}
				_, _ = w.Write([]byte(`{"response":[]}`))
			}))
			t.Cleanup(srv.Close)

			rc := newFakeContext(validSecrets())
			if _, err := invoke(t, "net.catalyst.device_facts", rc, newController(t, srv.URL), map[string]any{"page_size": tc.value}); err != nil {
				t.Fatalf("device_facts: %v", err)
			}
			select {
			case limit := <-asked:
				if limit != tc.want {
					t.Errorf("the controller was asked for %q devices a page, want %q", limit, tc.want)
				}
			default:
				t.Error("the controller was never asked for a page")
			}
		})
	}
}

// TestClientHonorsInsecureSkipVerify covers the one parameter that changes
// how the connection is made: a controller presenting a certificate
// nothing signed is refused by default and accepted with the parameter
// set, so the parameter genuinely reaches the client rather than being
// read and dropped.
func TestClientHonorsInsecureSkipVerify(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/dna/system/api/v1/auth/token" {
			_, _ = w.Write([]byte(`{"Token":"fixture-token"}`))
			return
		}
		_, _ = w.Write([]byte(`{"response":[]}`))
	}))
	t.Cleanup(srv.Close)
	controller := newController(t, srv.URL)

	if _, err := invoke(t, "net.catalyst.device_facts", newFakeContext(validSecrets()), controller, nil); err == nil {
		t.Error("a certificate nothing signed was accepted by default")
	}
	rc := newFakeContext(validSecrets())
	if _, err := invoke(t, "net.catalyst.device_facts", rc, controller, map[string]any{"insecure_skip_verify": true}); err != nil {
		t.Errorf("with insecure_skip_verify set = %v, want the gather to succeed", err)
	}
	if rc.facts["device_count"] != 0 {
		t.Errorf("device_count = %v, want 0 from a controller reporting no devices", rc.facts["device_count"])
	}
}
