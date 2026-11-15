// Tests for the RESTCONF probe through the real onboarding path, against a
// RESTCONF server over TLS serving a real Catalyst 8000V's YANG library.
package onboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// restconfLogin is the credential the test servers ask for.
var restconfLogin = restconftest.Options{Username: "admin", Password: "rc-Secret-9"}

// restconfRepo is a project holding one generic_restconf device for s, its
// certificate's authority pinned.
func restconfRepo(t *testing.T, s *restconftest.Server) (repoName string, secrets SecretsFunc, run func() (Result, error)) {
	t.Helper()
	repo, _ := newRepo(t, "rc1", generic.TypeRestconf, map[string]inventory.PropertyValue{
		devicetls.CAPEMProperty: s.CAPEM(), generic.BaseURLProperty: s.URL,
	})
	secrets = staticSecrets(restconftest.Secrets(s))
	return "rc1", secrets, func() (Result, error) {
		return onboardWith(context.Background(), repo, "rc1", secrets, fixedNow, Lookup, options{})
	}
}

// TestRestconfProbe_OnboardsFromTheDevicesOwnLibrary walks a new
// generic_restconf device to active and checks what onboarding recorded: the
// root host-meta named, the capabilities, the NMDA datastores and the module
// set's summary, with the count as text so a second onboarding of the
// unchanged device compares equal and writes nothing.
func TestRestconfProbe_OnboardsFromTheDevicesOwnLibrary(t *testing.T) {
	s := restconftest.New(t, restconfLogin)
	_, _, run := restconfRepo(t, s)
	res, err := run()
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "active" || res.Protocol != "restconf" || !res.Changed {
		t.Fatalf("result %+v, want the device active through the restconf probe", res)
	}
	if res.Facts["restconf_root"] != "/restconf" || res.Facts["yang_module_count"] != "556" || res.Facts["yang_completeness"] != string(yanglib.Complete) {
		t.Errorf("facts %v, want the root, 556 data models, complete", res.Facts)
	}
	if ds, _ := res.Facts["restconf_datastores"].([]any); len(ds) != 3 {
		t.Errorf("restconf_datastores = %v, want the library's three", res.Facts["restconf_datastores"])
	}
	if caps, _ := res.Facts["restconf_capabilities"].([]any); len(caps) != len(restconftest.Capabilities) {
		t.Errorf("restconf_capabilities = %v, want the server's", res.Facts["restconf_capabilities"])
	}
	if !strings.Contains(strings.Join(res.Capabilities, ","), string(capability.NameRestconf)) {
		t.Errorf("capabilities %v, want RestconfCapable granted", res.Capabilities)
	}
	if s.Unauthenticated() != 0 {
		t.Errorf("%d requests carried no credential", s.Unauthenticated())
	}
	again, err := run()
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed {
		t.Errorf("a second onboarding of an unchanged device changed the record: %+v", again)
	}
}

func TestRestconfProbe_ARefusedCredentialProvesNothing(t *testing.T) {
	s := restconftest.New(t, restconfLogin)
	repo, _ := newRepo(t, "rc1", generic.TypeRestconf, map[string]inventory.PropertyValue{
		devicetls.CAPEMProperty: s.CAPEM(), generic.BaseURLProperty: s.URL,
	})
	res, err := onboardWith(context.Background(), repo, "rc1", staticSecrets(map[string]string{"username": "admin", "password": "wrong"}), fixedNow, Lookup, options{})
	if err == nil || res.State == "active" || !strings.Contains(res.Error+err.Error(), "refused the credential") {
		t.Errorf("onboarding with a wrong password = %+v, %v; want it refused and not active", res, err)
	}
}

func TestRestconfProbe_NeitherReadAnsweringGrantsNothing(t *testing.T) {
	opts := restconfLogin
	notFound := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }
	opts.Override = map[string]http.HandlerFunc{
		"/restconf/data/ietf-yang-library:yang-library":                       notFound,
		"/restconf/data/ietf-yang-library:modules-state":                      notFound,
		"/restconf/data/ietf-restconf-monitoring:restconf-state/capabilities": notFound,
	}
	s := restconftest.New(t, opts)
	_, _, run := restconfRepo(t, s)
	res, err := run()
	if err == nil || res.State == "active" || !strings.Contains(err.Error(), "answered neither") {
		t.Errorf("onboarding = %+v, %v; want a root alone refused as proof", res, err)
	}
}

func TestRestconfProbe_OverPlainHTTPSaysNothingIsVerified(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/host-meta":
			_, _ = w.Write([]byte(`<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0"><Link rel="restconf" href="/restconf"/></XRD>`))
		case "/restconf/data/ietf-yang-library:yang-library":
			w.Header().Set("Content-Type", restconf.MediaTypeXML)
			_, _ = w.Write(restconftest.LibraryXML(t))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(plain.Close)
	item := build(t, generic.TypeRestconf, map[string]inventory.PropertyValue{generic.BaseURLProperty: plain.URL, generic.HTTPAuthProperty: httpapi.AuthNone})
	probed, err := restconfProber{}.Probe(context.Background(), item, nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(probed.Warnings, "\n")
	if !strings.Contains(joined, "nothing proves which server answered") || !strings.Contains(joined, "capabilities were not read") {
		t.Errorf("warnings %q, want the unverified origin and the unread capabilities said", joined)
	}
	if probed.Facts["yang_module_count"] != "556" {
		t.Errorf("facts %v, want the library read", probed.Facts)
	}
}

func TestRestconfProbed_LeavesALongCapabilityOutWhole(t *testing.T) {
	d := restconf.Discovery{Root: "", Discovery: yanglib.Discovery{
		Protocol:     []string{"urn:short", "urn:" + strings.Repeat("x", maxFactText)},
		Completeness: yanglib.Unknown,
		Dropped:      2,
		LibraryError: "refused",
	}}
	got := restconfProbed(d)
	if caps, _ := got.Facts["restconf_capabilities"].([]any); len(caps) != 1 || caps[0] != "urn:short" {
		t.Errorf("capabilities = %v, want the long one left out whole", got.Facts["restconf_capabilities"])
	}
	if got.Facts["restconf_root"] != "/" {
		t.Errorf("root = %v, want / for a root at the origin", got.Facts["restconf_root"])
	}
	joined := strings.Join(got.Warnings, "\n")
	for _, want := range []string{"longer than", "YANG library was not read", "2 malformed"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q, want %q said", joined, want)
		}
	}
	if (restconfProber{}).Protocol() != "restconf" {
		t.Error("the probe does not name its protocol")
	}
}

// TestRestconfProbe_OnboardsIOSXEWithNoCredentialAtHostMeta: against
// restconftest's IOS XE mode (IOS XE 17.15.1a's answers, 2026-11-15), the
// probe sends host-meta no credential, takes /restconf from its 404, and
// records that the root came from the default; a wrong password is sent once
// and grants nothing.
func TestRestconfProbe_OnboardsIOSXEWithNoCredentialAtHostMeta(t *testing.T) {
	opts := restconftest.Options{IOSXE: true, Username: "admin", Password: "iosxe-Onboard-2"}
	s := restconftest.New(t, opts)
	_, _, run := restconfRepo(t, s)
	res, err := run()
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "active" || res.Facts["restconf_root"] != "/restconf" || res.Facts["restconf_root_source"] != "default" || s.HostMetaLogins() != 0 {
		t.Errorf("result %+v with %d credentials to host-meta", res, s.HostMetaLogins())
	}

	wrong := restconftest.New(t, opts)
	repo, _ := newRepo(t, "rc2", generic.TypeRestconf, map[string]inventory.PropertyValue{
		devicetls.CAPEMProperty: wrong.CAPEM(), generic.BaseURLProperty: wrong.URL,
	})
	if _, err := onboardWith(context.Background(), repo, "rc2", staticSecrets(map[string]string{"username": "admin", "password": "wrong"}), fixedNow, Lookup, options{}); err == nil || wrong.Logins() != 1 || wrong.HostMetaLogins() != 0 {
		t.Errorf("a wrong password: %v after %d logins (%d to host-meta)", err, wrong.Logins(), wrong.HostMetaLogins())
	}
}
