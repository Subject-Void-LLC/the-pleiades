// Package yang: net.yang.schema over RESTCONF, through the real client
// (pkg/restconf.ClientForContext, real TLS pinned to the server's
// certificate, the device's credential) against restconftest serving a
// Catalyst 8000V's YANG library and its sources as IOS XE 17.15.1a was
// measured serving them: an RFC 8525 library naming no location, and a
// modules-state naming each source's URL at the address it was reached at.
package yang

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// restconfPassword is the test server's login password, which the probe
// served there holds once, so a run must mask it.
const restconfPassword = "s3cret-Passw0rd"

// restconfServer serves the probe, its text holding the server's password,
// and returns it with its device.
func restconfServer(t *testing.T, opts restconftest.Options) (*restconftest.Server, *restconftest.Device, *testContext) {
	t.Helper()
	opts.Username, opts.Password = "admin", restconfPassword
	if opts.Sources == nil {
		opts.Sources = map[string][]byte{"pleiades-probe@2026-11-16": []byte(strings.Replace(probeSource, "the secret key 100", "the secret key 100 "+restconfPassword, 1))}
	}
	s := restconftest.New(t, opts)
	rc := newContext(t)
	rc.secrets = restconftest.Secrets(s)
	return s, restconftest.NewDevice(t, s), rc
}

// TestRESTCONF_FetchesFromModulesStateAtTheDevicesOrigin finds no location in
// the RFC 8525 library, reads modules-state, fetches the probe at the
// server's own origin with its credential, and saves it with the password
// masked and every other byte as served.
func TestRESTCONF_FetchesFromModulesStateAtTheDevicesOrigin(t *testing.T) {
	s, dev, rc := restconfServer(t, restconftest.Options{})
	if _, err := Schema(context.Background(), rc, dev, map[string]any{paramModules: []any{"pleiades-probe"}}); err != nil {
		t.Fatal(err)
	}
	got, ok := rc.read(t, "yang/restconf/pleiades-probe@2026-11-16.yang")
	want := strings.Replace(probeSource, "the secret key 100", "the secret key 100 "+placeholder, 1)
	if !ok || got != want {
		t.Fatalf("saved\n%s\nwant the probe, its password masked and nothing else changed", got)
	}
	if rc.stats["via"] != viaRESTCONF || rc.records(t)[0]["masked"] != true {
		t.Errorf("stats = %v, want a masked file over restconf", rc.stats)
	}
	paths := strings.Join(s.Paths(), " ")
	for _, want := range []string{"/restconf/data/ietf-yang-library:yang-library", "/restconf/data/ietf-yang-library:modules-state", "/restconf/tailf/modules/pleiades-probe/2026-11-16"} {
		if !strings.Contains(paths, want) {
			t.Errorf("requests %s lack %s", paths, want)
		}
	}
	if s.Unauthenticated() != 0 {
		t.Errorf("%d requests carried no credential", s.Unauthenticated())
	}
}

// TestRESTCONF_ReadsALibraryThatNamesItsLocations takes the URLs from a
// server whose own library is modules-state, reading nothing more.
func TestRESTCONF_ReadsALibraryThatNamesItsLocations(t *testing.T) {
	s, dev, rc := restconfServer(t, restconftest.Options{NoYANGLibrary: true})
	if _, err := Schema(context.Background(), rc, dev, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	states := 0
	for _, p := range s.Paths() {
		if strings.HasSuffix(p, "ietf-yang-library:modules-state") {
			states++
		}
	}
	if states != 1 || rc.stats["files_changed"] != 1 {
		t.Errorf("modules-state read %d times, %v files; want one read and the one source", states, rc.stats["files_changed"])
	}
}

// TestRESTCONF_RefusesASourceAtAnotherOrigin is the credential held to the
// device's own origin: a schema leaf naming another host is never fetched,
// and nothing is written.
func TestRESTCONF_RefusesASourceAtAnotherOrigin(t *testing.T) {
	s, dev, rc := restconfServer(t, restconftest.Options{SourceOrigin: "https://elsewhere.example:443"})
	_, err := Schema(context.Background(), rc, dev, map[string]any{paramModules: []any{"pleiades-probe"}})
	if err == nil || !strings.Contains(err.Error(), "which is not the device's own origin") || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("an off-origin source: %v, want the refusal naming the credential", err)
	}
	if s.SourceRequests() != 0 || rc.writes != 0 {
		t.Errorf("%d source requests, %d writes; want none", s.SourceRequests(), rc.writes)
	}
}

// TestRESTCONF_RefusesALibraryNamingNoLocation is IOS XE's library with no
// modules-state, as rousette is with one: no URL, so nothing is guessed, and
// the refusal names the other protocol.
func TestRESTCONF_RefusesALibraryNamingNoLocation(t *testing.T) {
	_, dev, rc := restconfServer(t, restconftest.Options{Override: map[string]http.HandlerFunc{
		"/restconf/data/ietf-yang-library:modules-state": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yang-data+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"ietf-restconf:errors":{"error":[{"error-type":"application","error-tag":"invalid-value"}]}}`))
		},
	}})
	_, err := Schema(context.Background(), rc, dev, map[string]any{paramModules: []any{"Cisco-IOS-XE-native"}})
	if err == nil || !strings.Contains(err.Error(), "names no location for Cisco-IOS-XE-native@2024-07-01's source") || !strings.Contains(err.Error(), "via: netconf") {
		t.Fatalf("a library naming no location: %v, want the refusal naming NETCONF", err)
	}
}

// TestRESTCONF_CheckFetchesAndWritesNothing reads and fetches as a run does,
// and writes nothing.
func TestRESTCONF_CheckFetchesAndWritesNothing(t *testing.T) {
	s, dev, rc := restconfServer(t, restconftest.Options{})
	res, err := Check(context.Background(), rc, dev, map[string]any{paramModules: []any{"pleiades-probe"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || rc.writes != 0 || s.SourceRequests() != 1 {
		t.Errorf("check: %v, %d writes, %d source requests; want a predicted change, no write, the source read", res, rc.writes, s.SourceRequests())
	}
}

// TestRESTCONF_AFailedDiscoveryAndAFailedModulesState are the task's
// failures, naming RESTCONF: a refused credential ends discovery, and a
// modules-state that cannot be used ends the listing.
func TestRESTCONF_AFailedDiscoveryAndAFailedModulesState(t *testing.T) {
	_, dev, rc := restconfServer(t, restconftest.Options{})
	rc.secrets = map[string]string{wire.SecretUsername: "admin", wire.SecretPassword: "not-the-password"}
	if _, err := Schema(context.Background(), rc, dev, map[string]any{}); err == nil || !strings.Contains(err.Error(), "over restconf: the device refused the credential") {
		t.Errorf("a refused credential: %v", err)
	}
	_, dev, rc = restconfServer(t, restconftest.Options{Override: map[string]http.HandlerFunc{
		"/restconf/data/ietf-yang-library:modules-state": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yang-data+xml")
			_, _ = w.Write([]byte("<not-a-library/>"))
		},
	}})
	if _, err := Schema(context.Background(), rc, dev, map[string]any{}); err == nil || !strings.Contains(err.Error(), "reading modules-state for the sources' URLs") {
		t.Errorf("an unusable modules-state: %v", err)
	}
	// A device the client cannot be built for fails before anything is sent.
	dev.Base = "ftp://nowhere"
	if _, err := Schema(context.Background(), newContext(t), dev, map[string]any{}); err == nil || !strings.Contains(err.Error(), "over restconf") {
		t.Errorf("a device with no usable base URL: %v", err)
	}
}

// TestNamesLocation reads a location named only by a submodule.
func TestNamesLocation(t *testing.T) {
	if namesLocation([]yanglib.Schema{{Name: "a"}}) {
		t.Error("an entry naming no location reads as naming one")
	}
	if !namesLocation([]yanglib.Schema{{Name: "a", Submodules: []yanglib.Schema{{Name: "b", Locations: []string{"/yang/b"}}}}}) {
		t.Error("a submodule's location is not read")
	}
}
