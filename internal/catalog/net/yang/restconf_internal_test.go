// Package yang: net.yang.schema over RESTCONF, through the real client
// (pkg/restconf.ClientForContext, real TLS pinned to the server's
// certificate, the device's credential) against restconftest serving a
// Catalyst 8000V's YANG library and its sources as IOS XE 17.15.1a was
// measured serving them: an RFC 8525 library naming no location, and a
// modules-state naming each source's URL at the address it was reached at.
package yang

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

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

// TestRESTCONF_ADebugPageEchoingTheLoginNeverReachesTheError: a server
// answering a source's URL with a page that echoes the request's
// Authorization header is refused as not the source, and the refusal holds
// neither the header's credential nor the password inside it.
func TestRESTCONF_ADebugPageEchoingTheLoginNeverReachesTheError(t *testing.T) {
	basic := base64.StdEncoding.EncodeToString([]byte("admin:" + restconfPassword))
	// The header first, where the source's first statement would be, which
	// is the text a refusal is likeliest to quote.
	page := "Authorization: Basic " + basic + "\nAccept: application/yang\n"
	s, dev, rc := restconfServer(t, restconftest.Options{Sources: map[string][]byte{"pleiades-probe@2026-11-16": []byte(page)}})
	_, err := Schema(context.Background(), rc, dev, map[string]any{paramModules: []any{"pleiades-probe"}})
	if err == nil || !strings.Contains(err.Error(), "not the YANG source of module pleiades-probe@2026-11-16") {
		t.Fatalf("a debug page: %v, want it refused as not the source", err)
	}
	for _, leaked := range []string{basic, restconfPassword, "Basic"} {
		if strings.Contains(err.Error(), leaked) {
			t.Errorf("the refusal holds %q: %v", leaked, err)
		}
	}
	if s.SourceRequests() != 1 || rc.writes != 0 {
		t.Errorf("%d source requests, %d writes; want the one read and no write", s.SourceRequests(), rc.writes)
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
	// A refusal of modules-state other than "not here" (404, 400) is the
	// server failing or denying it, the task's failure, never read as a
	// server naming no source.
	for _, status := range []int{http.StatusInternalServerError, http.StatusForbidden} {
		_, dev, rc = restconfServer(t, restconftest.Options{Override: map[string]http.HandlerFunc{
			"/restconf/data/ietf-yang-library:modules-state": func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/yang-data+json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"ietf-restconf:errors":{"error":[{"error-type":"application","error-tag":"operation-failed"}]}}`))
			},
		}})
		if _, err := Schema(context.Background(), rc, dev, map[string]any{paramModules: []any{"Cisco-IOS-XE-native"}}); err == nil ||
			!strings.Contains(err.Error(), "reading modules-state for the sources' URLs") || strings.Contains(err.Error(), "names no location") {
			t.Errorf("modules-state answering %d: %v, want the task's failure, not a library naming no location", status, err)
		}
	}
	// A device the client cannot be built for fails before anything is sent.
	dev.Base = "ftp://nowhere"
	if _, err := Schema(context.Background(), newContext(t), dev, map[string]any{}); err == nil || !strings.Contains(err.Error(), "over restconf") {
		t.Errorf("a device with no usable base URL: %v", err)
	}
}

// TestRESTCONF_AFetchOfEveryModuleWaitsOutTheSessionCap is the fetch measured
// failing on a Catalyst 8000V (IOS XE 17.15, 2026-11-17): every one of 676
// sources, from a server capping its sessions at 100 as IOS XE does, each
// request a session held after its answer. Through the method and the real
// client at its real pacing, every source is fetched and written, and the
// task says how long it waited.
func TestRESTCONF_AFetchOfEveryModuleWaitsOutTheSessionCap(t *testing.T) {
	sources := map[string][]byte{}
	for i := range 676 {
		name := fmt.Sprintf("m%03d", i)
		sources[name+"@2026-11-16"] = []byte("module " + name + " {\n  namespace \"urn:t:" + name + "\";\n  prefix p;\n  revision 2026-11-16;\n}\n")
	}
	// Held for less than the client's first wait, so each wait frees the cap.
	s, dev, rc := restconfServer(t, restconftest.Options{Sources: sources, SessionCap: 100, SessionHold: 500 * time.Millisecond})
	if _, err := Schema(context.Background(), rc, dev, map[string]any{paramMaxModules: 1000}); err != nil {
		t.Fatalf("a fetch of every module under the cap: %v", err)
	}
	if rc.stats["files_changed"] != 676 || s.SessionRefusals() == 0 {
		t.Fatalf("%v files changed, %d refusals; want all 676 written past a cap that was reached", rc.stats["files_changed"], s.SessionRefusals())
	}
	for _, name := range []string{"m000", "m100", "m675"} {
		got, ok := rc.read(t, "yang/restconf/"+name+"@2026-11-16.yang")
		if !ok || got != string(sources[name+"@2026-11-16"]) {
			t.Errorf("%s saved as %q, want it as served", name, got)
		}
	}
	warned := fmt.Sprint(rc.stats["warnings"])
	if !strings.Contains(warned, fmt.Sprintf("refused %d RESTCONF requests with 409 in-use, too many sessions", s.SessionRefusals())) {
		t.Errorf("warnings %s do not name the %d refusals waited out", warned, s.SessionRefusals())
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
