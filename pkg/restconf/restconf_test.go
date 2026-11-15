// Package restconf_test: the client against a RESTCONF server over real TLS
// (restconftest), serving a real Catalyst 8000V's YANG library, through the
// same ClientFor path onboarding and net.restconf.facts take.
package restconf_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// login is the credential every test server asks for.
var login = restconftest.Options{Username: "admin", Password: "s3cret-Passw0rd"}

// discover runs ClientFor and Discover against s through device.
func discover(t *testing.T, s *restconftest.Server, dev *restconftest.Device) (restconf.Discovery, *restconf.Client, error) {
	t.Helper()
	client, _, err := restconf.ClientFor(dev, "router", restconftest.Secrets(s))
	if err != nil {
		t.Fatal(err)
	}
	d, err := client.Discover(context.Background())
	return d, client, err
}

func TestDiscover_ARealIOSXELibraryOverVerifiedTLS(t *testing.T) {
	s := restconftest.New(t, login)
	d, client, err := discover(t, s, restconftest.NewDevice(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if !client.Verified() {
		t.Error("Verified = false over https, whose certificate devicetls always checks")
	}
	if d.Root != "/restconf" {
		t.Errorf("Root = %q, want the /restconf host-meta names", d.Root)
	}
	if d.Completeness != yanglib.Complete || len(d.Modules) != 559 || d.Source != yanglib.SourceLibrary11 || d.LibraryError != "" {
		t.Fatalf("Discovery = %s, %d modules from %s (%q); want complete, 559, from yang-library", d.Completeness, len(d.Modules), d.Source, d.LibraryError)
	}
	if got := strings.Join(d.Datastores, ","); got != "intended,operational,running" {
		t.Errorf("Datastores = %q, want the NMDA set the library lists", got)
	}
	if d.ContentID != "fab9bf47e062dcbaeb6739b215fc4b27" {
		t.Errorf("ContentID = %q, want the library's content-id", d.ContentID)
	}
	if len(d.Protocol) != len(restconftest.Capabilities) || d.CapabilitiesError != "" {
		t.Errorf("Protocol = %v (%q), want the server's %d capabilities", d.Protocol, d.CapabilitiesError, len(restconftest.Capabilities))
	}
	if s.Unauthenticated() != 0 {
		t.Errorf("%d requests carried no credential, want every one to carry the device's", s.Unauthenticated())
	}
	want := []string{"/.well-known/host-meta", "/restconf/data/ietf-restconf-monitoring:restconf-state/capabilities", "/restconf/data/ietf-yang-library:yang-library"}
	if got := strings.Join(s.Paths(), " "); got != strings.Join(want, " ") {
		t.Errorf("requests = %s, want %s", got, strings.Join(want, " "))
	}
}

func TestDiscover_JSONReadsTheSameModuleSet(t *testing.T) {
	xmlServer := restconftest.New(t, login)
	fromXML, _, err := discover(t, xmlServer, restconftest.NewDevice(t, xmlServer))
	if err != nil {
		t.Fatal(err)
	}
	opts := login
	opts.JSON = true
	jsonServer := restconftest.New(t, opts)
	fromJSON, _, err := discover(t, jsonServer, restconftest.NewDevice(t, jsonServer))
	if err != nil {
		t.Fatal(err)
	}
	if fromJSON.ModulesDigest() != fromXML.ModulesDigest() || fromJSON.Completeness != yanglib.Complete {
		t.Errorf("JSON gave %d modules (%s), XML %d; want one module set", len(fromJSON.Modules), fromJSON.Completeness, len(fromXML.Modules))
	}
	if strings.Join(fromJSON.Protocol, ",") != strings.Join(fromXML.Protocol, ",") || strings.Join(fromJSON.Datastores, ",") != strings.Join(fromXML.Datastores, ",") {
		t.Errorf("JSON capabilities %v and datastores %v differ from XML's", fromJSON.Protocol, fromJSON.Datastores)
	}
}

func TestDiscover_FallsBackToModulesState(t *testing.T) {
	opts := login
	opts.NoYANGLibrary = true
	s := restconftest.New(t, opts)
	d, _, err := discover(t, s, restconftest.NewDevice(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if d.Source != yanglib.SourceLibrary10 || d.Completeness != yanglib.Complete || len(d.Modules) == 0 {
		t.Errorf("Discovery = %s from %s with %d modules, want complete from modules-state", d.Completeness, d.Source, len(d.Modules))
	}
	if len(d.Datastores) != 0 || d.ContentID == "" {
		t.Errorf("Datastores = %v, ContentID = %q; want none (RFC 7895 lists none) and the module-set-id", d.Datastores, d.ContentID)
	}
}

func TestDiscover_AWrongPasswordIsARefusedCredential(t *testing.T) {
	s := restconftest.New(t, login)
	dev := restconftest.NewDevice(t, s)
	secrets := restconftest.Secrets(s)
	secrets[wire.SecretPassword] = "wrong-password"
	client, _, err := restconf.ClientFor(dev, "router", secrets)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Discover(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refused the credential") {
		t.Fatalf("Discover = %v, want the credential refused", err)
	}
	if strings.Contains(err.Error(), "wrong-password") {
		t.Errorf("the error %q carries the password", err)
	}
}

func TestDiscover_NoCredentialSentSaysHowToSendOne(t *testing.T) {
	s := restconftest.New(t, login)
	dev := restconftest.NewDevice(t, s)
	dev.Auth = httpapi.AuthNone
	_, _, err := discover(t, s, dev)
	if err == nil || !strings.Contains(err.Error(), "http_auth") {
		t.Fatalf("Discover = %v, want the http_auth property named", err)
	}
}

func TestDiscover_AnUntrustedCertificateIsNamedByItsFingerprint(t *testing.T) {
	s := restconftest.New(t, login)
	dev := restconftest.NewDevice(t, s)
	settings, err := devicetls.Parse(inventory.NewProperties(nil))
	if err != nil {
		t.Fatal(err)
	}
	untrusted := &untrustedDevice{Device: dev, tls: settings}
	client, _, err := restconf.ClientFor(untrusted, "router", restconftest.Secrets(s))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Discover(context.Background())
	if err == nil || !strings.Contains(err.Error(), devicetls.Fingerprint(s.Certificate())) {
		t.Fatalf("Discover = %v, want the certificate's fingerprint named", err)
	}
	if len(s.Paths()) != 0 {
		t.Errorf("the server saw %v; a refused handshake sends no request", s.Paths())
	}
}

// untrustedDevice is a device whose TLS pins nothing, so only the system's
// authorities are trusted.
type untrustedDevice struct {
	*restconftest.Device
	tls devicetls.Settings
}

// TLSSettings implements devicetls.Configured.
func (d *untrustedDevice) TLSSettings() devicetls.Settings { return d.tls }

func TestDiscoverRoot_RefusesARootOffTheDevicesOrigin(t *testing.T) {
	link := func(href string) string {
		return `<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0"><Link rel="restconf" href="` + href + `"/></XRD>`
	}
	cases := map[string]string{
		"another host":            link("https://elsewhere.example/restconf"),
		"another scheme":          link("http://127.0.0.1/restconf"),
		"a protocol-relative URL": link("//elsewhere.example/restconf"),
		"a relative path":         link("restconf"),
		"a query":                 link("/restconf?x=1"),
		"a fragment":              link("/restconf#x"),
		"a dot segment":           link("/a/../restconf"),
		"an escaped dot segment":  link("/a/%2E%2E/restconf"),
		"an escaped control":      link("/rest%0Aconf"),
		"user information":        link("https://u@127.0.0.1/restconf"),
		"two roots":               `<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0"><Link rel="restconf" href="/a"/><Link rel="restconf" href="/b"/></XRD>`,
		"no restconf link":        `<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0"><Link rel="lrdd" href="/a"/></XRD>`,
		"not XRD":                 `<html><body>hello</body></html>`,
		"another namespace":       `<XRD xmlns="urn:example"><Link rel="restconf" href="/a"/></XRD>`,
		"not XML":                 `{"links":[]}`,
		"a long root":             link("/" + strings.Repeat("a", 1100)),
	}
	for name, doc := range cases {
		opts := login
		opts.HostMeta = doc
		s := restconftest.New(t, opts)
		client, _, err := restconf.ClientFor(restconftest.NewDevice(t, s), "router", restconftest.Secrets(s))
		if err != nil {
			t.Fatal(err)
		}
		if root, err := client.DiscoverRoot(context.Background()); err == nil {
			t.Errorf("%s: DiscoverRoot = %q, want a refusal", name, root)
		}
		if _, found := client.Root(); found {
			t.Errorf("%s: a refused root was kept", name)
		}
	}
}

func TestDiscoverRoot_TakesARootTheDeviceNamesOnItsOwnOrigin(t *testing.T) {
	opts := login
	opts.Root = "/api/rc"
	s := restconftest.New(t, opts)
	d, _, err := discover(t, s, restconftest.NewDevice(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if d.Root != "/api/rc" || len(d.Modules) != 559 {
		t.Errorf("Root = %q with %d modules, want /api/rc and the library read under it", d.Root, len(d.Modules))
	}
	// The same root written as an absolute URL on the device's own origin,
	// with a trailing slash, is the same root. The server's own address is
	// known only once it runs, so the host-meta handler reads it then.
	var origin atomic.Value
	opts = login
	opts.Override = map[string]http.HandlerFunc{"/.well-known/host-meta": func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xrd+xml")
		_, _ = w.Write([]byte(`<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0"><Link rel="restconf" href="` + origin.Load().(string) + `/restconf/"/></XRD>`))
	}}
	s2 := restconftest.New(t, opts)
	origin.Store(s2.URL)
	client, _, err := restconf.ClientFor(restconftest.NewDevice(t, s2), "router", restconftest.Secrets(s2))
	if err != nil {
		t.Fatal(err)
	}
	if root, err := client.DiscoverRoot(context.Background()); err != nil || root != "/restconf" {
		t.Errorf("DiscoverRoot = %q, %v; want /restconf", root, err)
	}
}

func TestDiscoverRoot_AMissingHostMetaIsAnError(t *testing.T) {
	opts := login
	opts.Override = map[string]http.HandlerFunc{"/.well-known/host-meta": func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) }}
	s := restconftest.New(t, opts)
	_, _, err := discover(t, s, restconftest.NewDevice(t, s))
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("Discover = %v, want the host-meta's 404 named", err)
	}
}

func TestDiscover_ARedirectIsNotFollowed(t *testing.T) {
	var elsewhere atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere.Add(1) }))
	t.Cleanup(other.Close)
	opts := login
	opts.Override = map[string]http.HandlerFunc{"/.well-known/host-meta": func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/.well-known/host-meta", http.StatusFound)
	}}
	s := restconftest.New(t, opts)
	if _, _, err := discover(t, s, restconftest.NewDevice(t, s)); err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("Discover = %v, want the redirect refused as an answer", err)
	}
	if elsewhere.Load() != 0 {
		t.Error("the credential followed a redirect to another server")
	}
}

func TestDiscover_AnUnreadableLibraryOrCapabilityListIsSaidNotFatal(t *testing.T) {
	opts := login
	opts.Override = map[string]http.HandlerFunc{
		"/restconf/data/ietf-yang-library:yang-library": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yang-data+xml")
			_, _ = w.Write([]byte(`<data xmlns="urn:example"/>`))
		},
		"/restconf/data/ietf-restconf-monitoring:restconf-state/capabilities": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yang-data+json")
			_, _ = w.Write([]byte(`{"other":1}`))
		},
	}
	s := restconftest.New(t, opts)
	d, _, err := discover(t, s, restconftest.NewDevice(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if d.Completeness != yanglib.Unknown || d.LibraryError == "" || len(d.Modules) != 0 {
		t.Errorf("Discovery = %s (%q) with %d modules, want unknown with the reason", d.Completeness, d.LibraryError, len(d.Modules))
	}
	if d.CapabilitiesError == "" || len(d.Protocol) != 0 {
		t.Errorf("Protocol = %v (%q), want none and the reason", d.Protocol, d.CapabilitiesError)
	}
}

func TestDiscover_AnEmptyLibraryIsUnknown(t *testing.T) {
	opts := login
	opts.Library = []byte(`<yang-library xmlns="urn:ietf:params:xml:ns:yang:ietf-yang-library"/>`)
	s := restconftest.New(t, opts)
	d, _, err := discover(t, s, restconftest.NewDevice(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if d.Completeness != yanglib.Unknown || !strings.Contains(d.LibraryError, "empty") {
		t.Errorf("Discovery = %s (%q), want unknown and the library named empty", d.Completeness, d.LibraryError)
	}
}

func TestGetData_AnErrorIsStructuredAndMasked(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		opts := login
		opts.JSON = asJSON
		body := `<errors xmlns="urn:ietf:params:xml:ns:yang:ietf-restconf"><error><error-type>application</error-type><error-tag>invalid-value</error-tag><error-path>/ietf-interfaces:interfaces/interface[name='Gi1']</error-path><error-message>username admin password 0 Hunter2Hunter2 rejected</error-message></error></errors>`
		mediaType := "application/yang-data+xml"
		if asJSON {
			body = `{"ietf-restconf:errors":{"error":[{"error-type":"application","error-tag":"invalid-value","error-path":"/ietf-interfaces:interfaces/interface[name='Gi1']","error-message":"username admin password 0 Hunter2Hunter2 rejected"}]}}`
			mediaType = "application/yang-data+json"
		}
		opts.Override = map[string]http.HandlerFunc{"/restconf/data/ietf-interfaces:interfaces": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", mediaType)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(body))
		}}
		s := restconftest.New(t, opts)
		client, _, err := restconf.ClientFor(restconftest.NewDevice(t, s), "router", restconftest.Secrets(s))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.DiscoverRoot(context.Background()); err != nil {
			t.Fatal(err)
		}
		_, _, err = client.GetData(context.Background(), restconf.Path{restconf.Node("ietf-interfaces", "interfaces")})
		var status *restconf.StatusError
		if !errors.As(err, &status) {
			t.Fatalf("json=%v: GetData = %v, want a *StatusError", asJSON, err)
		}
		if status.Status != http.StatusBadRequest || !status.HasTag("invalid-value") || status.HasTag("access-denied") || len(status.Errors) != 1 || status.Errors[0].Type != "application" {
			t.Errorf("json=%v: StatusError = %+v, want 400 with the server's invalid-value", asJSON, status)
		}
		if strings.Contains(err.Error(), "Hunter2Hunter2") || !strings.Contains(err.Error(), "Gi1") {
			t.Errorf("json=%v: error %q, want the path kept and the password masked", asJSON, err)
		}
	}
}

func TestGetData_AnAnswerWithNoErrorsContainerIsQuoted(t *testing.T) {
	opts := login
	opts.Override = map[string]http.HandlerFunc{
		"/restconf/data/a:b": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal\x1b[31m error"))
		},
		"/restconf/data/a:c": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) },
		"/restconf/data/a:d": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yang-data+xml")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`<data><x><!DOCTYPE x></x></data>`))
		},
	}
	s := restconftest.New(t, opts)
	client, _, err := restconf.ClientFor(restconftest.NewDevice(t, s), "router", restconftest.Secrets(s))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.DiscoverRoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, err = client.GetData(context.Background(), restconf.Path{restconf.Node("a", "b")})
	if err == nil || strings.Contains(err.Error(), "\x1b") || !strings.Contains(err.Error(), "500") {
		t.Errorf("GetData = %q, want the 500 with its text quoted", err)
	}
	_, _, err = client.GetData(context.Background(), restconf.Path{restconf.Node("a", "c")})
	if err == nil || !strings.HasSuffix(err.Error(), "503 Service Unavailable") {
		t.Errorf("GetData = %v, want the bare status", err)
	}
	_, _, err = client.GetData(context.Background(), restconf.Path{restconf.Node("a", "d")})
	if err == nil || !strings.Contains(err.Error(), "withheld") {
		t.Errorf("GetData = %v, want an answer masking cannot vouch for withheld", err)
	}
}

func TestGetData_BuildsEveryKeyIntoItsOwnSegment(t *testing.T) {
	want := "/restconf/data/ietf-interfaces:interfaces/interface=GigabitEthernet0%2F0%2F1%2Cx"
	opts := login
	opts.Override = map[string]http.HandlerFunc{want: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yang-data+json")
		_, _ = w.Write([]byte(`{"ietf-interfaces:interface":[]}`))
	}}
	s := restconftest.New(t, opts)
	client, _, err := restconf.ClientFor(restconftest.NewDevice(t, s), "router", restconftest.Secrets(s))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.DiscoverRoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := restconf.Path{restconf.Node("ietf-interfaces", "interfaces"), restconf.Node("", "interface").Entry("GigabitEthernet0/0/1,x")}
	_, mediaType, err := client.GetData(context.Background(), p)
	if err != nil {
		t.Fatalf("GetData = %v; the server saw %v, want %s", err, s.Paths(), want)
	}
	if mediaType != restconf.MediaTypeJSON {
		t.Errorf("media type = %s, want JSON", mediaType)
	}
}

func TestGetData_NeedsTheRoot(t *testing.T) {
	s := restconftest.New(t, login)
	client, _, err := restconf.ClientFor(restconftest.NewDevice(t, s), "router", restconftest.Secrets(s))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.GetData(context.Background(), nil); err == nil {
		t.Error("GetData before DiscoverRoot succeeded")
	}
	if len(s.Paths()) != 0 {
		t.Errorf("requests %v were sent", s.Paths())
	}
}

func TestDiscover_PastTheHostMetaBoundIsRefused(t *testing.T) {
	opts := login
	opts.HostMeta = `<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0"><Link rel="restconf" href="/restconf"/>` + strings.Repeat(" ", restconf.MaxHostMetaBytes) + `</XRD>`
	s := restconftest.New(t, opts)
	if _, _, err := discover(t, s, restconftest.NewDevice(t, s)); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("Discover = %v, want the oversized host-meta refused", err)
	}
}

func TestDiscover_PastTheDataBoundTheLibraryIsUnread(t *testing.T) {
	opts := login
	huge := append([]byte(`<yang-library xmlns="urn:ietf:params:xml:ns:yang:ietf-yang-library">`), make([]byte, restconf.MaxDataBytes)...)
	opts.Override = map[string]http.HandlerFunc{"/restconf/data/ietf-yang-library:yang-library": func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yang-data+xml")
		_, _ = w.Write(huge)
	}}
	s := restconftest.New(t, opts)
	d, _, err := discover(t, s, restconftest.NewDevice(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.LibraryError, "larger than") || len(d.Modules) != 0 {
		t.Errorf("LibraryError = %q with %d modules, want the oversized answer refused whole", d.LibraryError, len(d.Modules))
	}
}

func TestClientFor_OverPlainHTTPIsUnverifiedAndWarned(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/host-meta" {
			_, _ = w.Write([]byte(`<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0"><Link rel="restconf" href="/restconf"/></XRD>`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(plain.Close)
	s := restconftest.New(t, login)
	dev := restconftest.NewDevice(t, s)
	dev.Base, dev.Auth, dev.AllowPlaintext = plain.URL, httpapi.AuthBasic, true
	plainDev := &untrustedDevice{Device: dev}
	client, warnings, err := restconf.ClientFor(plainDev, "router", restconftest.Secrets(s))
	if err != nil {
		t.Fatal(err)
	}
	if client.Verified() {
		t.Error("Verified = true over http://")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "unencrypted") {
		t.Errorf("warnings = %v, want the plaintext credential named", warnings)
	}
	if _, err := client.DiscoverRoot(context.Background()); err != nil {
		t.Errorf("DiscoverRoot over http = %v", err)
	}
	dev.AllowPlaintext = false
	if _, _, err := restconf.ClientFor(plainDev, "router", nil); err == nil {
		t.Error("ClientFor sent basic authentication over http:// without the device's opt-in")
	}
}

func TestClientFor_RefusesWhatIsNotARestconfDevice(t *testing.T) {
	if _, _, err := restconf.ClientFor(struct{}{}, "x", nil); err == nil {
		t.Error("ClientFor accepted a device that serves no RESTCONF")
	}
	s := restconftest.New(t, login)
	dev := restconftest.NewDevice(t, s)
	dev.Base = s.URL + "/restconf"
	if _, _, err := restconf.ClientFor(dev, "x", nil); err == nil || !strings.Contains(err.Error(), "no path") {
		t.Errorf("ClientFor = %v, want a base URL with a path refused", err)
	}
}

func TestNew_NeedsAnHTTPClient(t *testing.T) {
	if _, err := restconf.New("https://192.0.2.1", nil, httpapi.AuthNone, false, nil); err == nil {
		t.Error("New accepted no HTTP client")
	}
	c, err := restconf.New("https://192.0.2.1/", http.DefaultClient, httpapi.AuthNone, false, nil)
	if err != nil || c.Base() != "https://192.0.2.1" {
		t.Errorf("New = %v, %v; want the origin kept without its slash", c, err)
	}
}
