// Package restconf_test: fetching YANG sources from a RESTCONF server over
// real TLS (restconftest serving them as IOS XE 17.15.1a was measured to),
// through the same ClientFor path net.yang.schema takes.
package restconf_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// probeSource is a module holding words a configuration masker reads as
// secrets, and the server's own password: a source comes back as it was
// served, the client masking nothing.
const probeSource = "module pleiades-probe {\n  namespace \"urn:pleiades:probe\";\n  prefix pp;\n  revision 2026-11-16;\n" +
	"  leaf password { type string; description \"secret key 100 at 10.1.2.3, s3cret-Passw0rd\"; }\n}\n"

// sourceServer is a server holding probeSource, and its client with the
// root discovered.
func sourceServer(t *testing.T, opts restconftest.Options) (*restconftest.Server, *restconf.Client) {
	t.Helper()
	opts.Username, opts.Password = login.Username, login.Password
	if opts.Sources == nil {
		opts.Sources = map[string][]byte{"pleiades-probe@2026-11-16": []byte(probeSource)}
	}
	s := restconftest.New(t, opts)
	_, client, err := discover(t, s, restconftest.NewDevice(t, s))
	if err != nil {
		t.Fatal(err)
	}
	return s, client
}

// TestGetSchema_FetchesTheSourceItsModulesStateNames reads a schema leaf as
// IOS XE writes it, at the device's own address with :443 written out (which
// is the device's origin), and fetches the source byte for byte.
func TestGetSchema_FetchesTheSourceItsModulesStateNames(t *testing.T) {
	s, client := sourceServer(t, restconftest.Options{})
	schemas, err := client.StateSchemas(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 1 || len(schemas[0].Locations) != 1 {
		t.Fatalf("StateSchemas = %+v, want the one source's entry and its URL", schemas)
	}
	got, err := client.GetSchema(context.Background(), schemas[0].Locations[0], 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != probeSource {
		t.Errorf("GetSchema = %q, want the source as served", got)
	}
	if err := yanglib.CheckSource(got, schemas[0], false); err != nil {
		t.Error(err)
	}
	if s.Unauthenticated() != 0 {
		t.Errorf("%d requests carried no credential", s.Unauthenticated())
	}
}

// TestGetSchema_RefusesALocationAtAnotherOrigin holds the credential to the
// device's own origin: a location at another host, port or scheme, or
// carrying user information, is refused before anything is sent.
func TestGetSchema_RefusesALocationAtAnotherOrigin(t *testing.T) {
	s, client := sourceServer(t, restconftest.Options{})
	own, err := url.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	before := len(s.Paths())
	for _, location := range []string{
		"https://elsewhere.example" + "/restconf/tailf/modules/pleiades-probe/2026-11-16",
		"http://" + own.Host + "/restconf/tailf/modules/pleiades-probe/2026-11-16",
		"https://" + own.Hostname() + ":1/restconf/tailf/modules/pleiades-probe/2026-11-16",
		"https://" + own.Hostname() + "/restconf/tailf/modules/pleiades-probe/2026-11-16",
		"https://admin:x@" + own.Host + "/restconf/tailf/modules/pleiades-probe/2026-11-16",
		"//elsewhere.example/restconf/tailf/modules/pleiades-probe/2026-11-16",
		"http://localhost:9938/restconf/tailf/modules/pleiades-probe/2026-11-16",
	} {
		_, err := client.GetSchema(context.Background(), location, 1<<20)
		var off *restconf.OffOriginError
		if !errors.As(err, &off) || !strings.Contains(err.Error(), "credential") {
			t.Errorf("GetSchema(%q) = %v, want an OffOriginError", location, err)
		}
	}
	for _, location := range []string{"mailto:x@y", "https://%zz", "/restconf/../etc/passwd"} {
		if _, err := client.GetSchema(context.Background(), location, 1<<20); err == nil {
			t.Errorf("GetSchema(%q) succeeded, want a refusal", location)
		}
	}
	if after := len(s.Paths()); after != before {
		t.Errorf("%d requests were sent for refused locations, want none", after-before)
	}
}

// TestGetSchema_ResolvesAPathAgainstTheOrigin reads a location with no host
// as a path on the device's own origin.
func TestGetSchema_ResolvesAPathAgainstTheOrigin(t *testing.T) {
	_, client := sourceServer(t, restconftest.Options{})
	got, err := client.GetSchema(context.Background(), "/restconf/tailf/modules/pleiades-probe/2026-11-16", 1<<20)
	if err != nil || string(got) != probeSource {
		t.Errorf("GetSchema of a path = %q, %v; want the source", got, err)
	}
	if ref, err := client.SchemaRef("restconf/tailf/modules/x?a=1#frag"); err != nil || ref != "/restconf/tailf/modules/x?a=1" {
		t.Errorf("SchemaRef of a relative path = %q, %v; want it rooted at the origin, its query kept and fragment dropped", ref, err)
	}
}

// TestSchemaRef_ReadsEachSchemesDefaultPort holds a plain-HTTP device's
// origin to port 80 written out or left out, and a scheme with no default port
// to another origin.
func TestSchemaRef_ReadsEachSchemesDefaultPort(t *testing.T) {
	client, err := restconf.New("http://device.example", http.DefaultClient, "none", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ref, err := client.SchemaRef("http://DEVICE.example:80/yang/m"); err != nil || ref != "/yang/m" {
		t.Errorf("SchemaRef with :80 written out = %q, %v; want the path on the device's origin", ref, err)
	}
	var off *restconf.OffOriginError
	if _, err := client.SchemaRef("ftp://device.example/yang/m"); !errors.As(err, &off) {
		t.Errorf("SchemaRef of another scheme = %v, want an OffOriginError", err)
	}
}

// TestGetSchema_BoundsAndStatuses refuses a source past its bound, and
// returns the server's own refusal of one it lacks.
func TestGetSchema_BoundsAndStatuses(t *testing.T) {
	s, client := sourceServer(t, restconftest.Options{})
	base := s.URL + "/restconf/tailf/modules/"
	if _, err := client.GetSchema(context.Background(), base+"pleiades-probe/2026-11-16", 16); err == nil || !strings.Contains(err.Error(), "larger than 16 bytes") {
		t.Errorf("a source past its bound: %v, want a refusal naming it", err)
	}
	var status *restconf.StatusError
	if _, err := client.GetSchema(context.Background(), base+"missing/2001-01-01", 1<<20); !errors.As(err, &status) || status.Status != http.StatusNotFound {
		t.Errorf("a missing source: %v, want the server's 404", err)
	}
}

// TestStateSchemas_OfAServerWithoutModulesState is the server's own refusal,
// which a caller reads as no locations named.
func TestStateSchemas_OfAServerWithoutModulesState(t *testing.T) {
	s := restconftest.New(t, restconftest.Options{Username: login.Username, Password: login.Password, Override: map[string]http.HandlerFunc{
		"/restconf/data/ietf-yang-library:modules-state": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "not here", http.StatusNotFound)
		},
	}})
	_, client, err := discover(t, s, restconftest.NewDevice(t, s))
	if err != nil {
		t.Fatal(err)
	}
	var status *restconf.StatusError
	if _, err := client.StateSchemas(context.Background()); !errors.As(err, &status) || status.Status != http.StatusNotFound {
		t.Errorf("StateSchemas = %v, want the 404", err)
	}
}
