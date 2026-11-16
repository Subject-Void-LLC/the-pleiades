// Package restconf_test: net.restconf.facts against a RESTCONF server over
// real TLS (restconftest) serving a real Catalyst 8000V's YANG library,
// through the method's registered Invoke and Check.
package restconf_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/restconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/tlsfacts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestFacts_RecordsTheCertificateItsPinTrusted: a read over the device's
// pinned TLS records the certificate the verified handshake presented, both
// of a pin's forms, its names and its last moment, as stable facts and as
// stats.
func TestFacts_RecordsTheCertificateItsPinTrusted(t *testing.T) {
	s := restconftest.New(t, login)
	rc := newRecorder(restconftest.Secrets(s))
	if _, err := method(t).Invoke(context.Background(), rc, restconftest.NewDevice(t, s), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	cert := s.Certificate()
	want := map[string]any{
		tlsfacts.FactFingerprint:    devicetls.Fingerprint(cert),
		tlsfacts.FactKeyFingerprint: devicetls.KeyFingerprint(cert),
		tlsfacts.FactNotAfter:       cert.NotAfter.UTC().Format(time.RFC3339),
		tlsfacts.FactSubject:        cert.Subject.String(),
	}
	for k, v := range want {
		if rc.facts[k] != v || rc.stats[k] != v {
			t.Errorf("%s: fact %v, stat %v, want %v", k, rc.facts[k], rc.stats[k], v)
		}
	}
	for _, key := range tlsfacts.FactKeys {
		if spec, ok := collection.FactFor(method(t).Manifest.Facts, key); !ok || spec.Kind != collection.FactStable || spec.Type != collection.FactString {
			t.Errorf("fact %s is declared %+v, %v; want a stable string", key, spec, ok)
		}
	}
}

// recorder is a task's context: the secrets it hands the method, and what
// the method recorded.
type recorder struct {
	mu      sync.Mutex
	secrets map[string]string
	stats   map[string]any
	facts   map[string]any
	// failOn makes the nth SetStat of a key fail, counting from 1.
	failOn map[string]int
	sets   map[string]int
}

// newRecorder returns a context holding secrets.
func newRecorder(secrets map[string]string) *recorder {
	return &recorder{secrets: secrets, stats: map[string]any{}, facts: map[string]any{}}
}

// InjectSecrets implements sdk.RunbookContext.
func (r *recorder) InjectSecrets() map[string]string { return r.secrets }

// SetStat implements sdk.RunbookContext.
func (r *recorder) SetStat(key string, value any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sets == nil {
		r.sets = map[string]int{}
	}
	r.sets[key]++
	if n, ok := r.failOn[key]; ok && n == r.sets[key] {
		return errors.New("the stat store is full")
	}
	r.stats[key] = value
	return nil
}

// EmitFact implements sdk.RunbookContext.
func (r *recorder) EmitFact(key string, value any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.facts[key] = value
	return nil
}

// warnings are the warnings the task recorded.
func (r *recorder) warnings() []string {
	w, _ := r.stats[sdk.StatWarnings].([]string)
	return w
}

// method is the registered descriptor.
func method(t *testing.T) collection.Descriptor {
	t.Helper()
	d, ok := collection.Lookup("net.restconf.facts")
	if !ok {
		t.Fatal("net.restconf.facts is not registered")
	}
	return d
}

var login = restconftest.Options{Username: "admin", Password: "facts-Passw0rd"}

func TestFacts_Registered(t *testing.T) {
	d := method(t)
	m := d.Manifest
	if m.Status != collection.StatusImplemented || !m.SupportsCheck || d.Check == nil || !m.Reversibility.ReadOnly {
		t.Errorf("manifest = %+v, want an implemented, read-only method whose check runs it", m)
	}
	if !slices.Equal(m.SupportedTransports, []string{capability.TransportRestconf}) || !slices.Equal(m.RequiredCapabilities, []capability.Name{capability.NameRestconf}) {
		t.Errorf("transports %v, capabilities %v; want restconf and RestconfCapable", m.SupportedTransports, m.RequiredCapabilities)
	}
	for _, key := range []string{"restconf.root", "restconf.capabilities", "restconf.datastores", "yang.modules", "yang.module_count", "yang.modules_digest", "yang.completeness", "yang.content_id"} {
		spec, ok := collection.FactFor(m.Facts, key)
		if !ok || spec.Type == "" || spec.Description == "" {
			t.Errorf("fact %s = %+v, %v; want it declared with a type and a description", key, spec, ok)
		}
	}
	if spec, _ := collection.FactFor(m.Facts, "yang.content_id"); spec.Kind != collection.FactVolatile {
		t.Errorf("yang.content_id is %s, want volatile: a device may regenerate it", spec.Kind)
	}
	if spec, _ := collection.FactFor(m.Facts, "restconf.root"); spec.Kind == collection.FactVolatile {
		t.Error("restconf.root is volatile, want stable: a moved root is a change")
	}
}

func TestFacts_RecordsTheDiscoveryOverVerifiedTLS(t *testing.T) {
	s := restconftest.New(t, login)
	d := method(t)
	for name, run := range map[string]collection.Method{"invoke": d.Invoke, "check": d.Check} {
		rc := newRecorder(restconftest.Secrets(s))
		res, err := run(context.Background(), rc, restconftest.NewDevice(t, s), map[string]any{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.Changed {
			t.Errorf("%s: changed, want a read that changes nothing", name)
		}
		if rc.facts["restconf.root"] != "/restconf" || rc.facts["yang.module_count"] != 556 || rc.facts["yang.completeness"] != "complete" || rc.facts["yang.content_id"] != "fab9bf47e062dcbaeb6739b215fc4b27" {
			t.Errorf("%s: facts %v", name, rc.facts)
		}
		if mods, _ := rc.facts["yang.modules"].([]string); !slices.Contains(mods, "Cisco-IOS-XE-native@2024-07-01") || len(mods) != 556 {
			t.Errorf("%s: yang.modules has %d entries, want the 556 data models with Cisco-IOS-XE-native", name, len(mods))
		}
		// This library is the router's NETCONF one, so the protocol modules
		// it lists are NETCONF's three, kept out of yang.* and named here.
		if own, _ := rc.facts["restconf.modules"].([]string); len(own) != 3 || slices.Contains(rc.facts["yang.modules"].([]string), "ietf-netconf@2011-06-01") {
			t.Errorf("%s: restconf.modules = %v, want the three protocol modules the library lists, none in yang.modules", name, own)
		}
		if ds, _ := rc.facts["restconf.datastores"].([]string); strings.Join(ds, ",") != "intended,operational,running" {
			t.Errorf("%s: restconf.datastores = %v", name, ds)
		}
		if caps, _ := rc.facts["restconf.capabilities"].([]string); len(caps) != len(restconftest.Capabilities) || !slices.IsSorted(caps) {
			t.Errorf("%s: restconf.capabilities = %v, want the server's, sorted", name, caps)
		}
		for key := range rc.facts {
			if rc.stats[key] == nil {
				t.Errorf("%s: fact %s has no stat beside it", name, key)
			}
		}
		// The one warning is the pinned certificate's, which every use of
		// a pin says.
		if w := rc.warnings(); len(w) != 1 || !strings.Contains(w[0], "pinned certificate") {
			t.Errorf("%s: warnings %v, want the pin's alone", name, w)
		}
	}
}

func TestFacts_JSONGivesTheSameModuleSet(t *testing.T) {
	read := func(opts restconftest.Options) map[string]any {
		s := restconftest.New(t, opts)
		rc := newRecorder(restconftest.Secrets(s))
		if _, err := method(t).Invoke(context.Background(), rc, restconftest.NewDevice(t, s), nil); err != nil {
			t.Fatal(err)
		}
		return rc.facts
	}
	asJSON := login
	asJSON.JSON = true
	a, b := read(login), read(asJSON)
	if a["yang.modules_digest"] != b["yang.modules_digest"] || a["yang.modules_digest"] == "" {
		t.Errorf("digests %v and %v, want one module set", a["yang.modules_digest"], b["yang.modules_digest"])
	}
}

func TestFacts_OverPlainHTTPRecordsStatsAndNoFact(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/host-meta":
			_, _ = w.Write([]byte(`<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0"><Link rel="restconf" href="/"/></XRD>`))
		case "/data/ietf-yang-library:yang-library":
			w.Header().Set("Content-Type", "application/yang-data+xml")
			_, _ = w.Write(restconftest.LibraryXML(t))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(plain.Close)
	s := restconftest.New(t, login)
	dev := restconftest.NewDevice(t, s)
	dev.Base, dev.Auth = plain.URL, httpapi.AuthNone
	rc := newRecorder(nil)
	if _, err := method(t).Invoke(context.Background(), rc, dev, nil); err != nil {
		t.Fatal(err)
	}
	if len(rc.facts) != 0 {
		t.Errorf("facts = %v, want none from a device whose identity nothing verified", rc.facts)
	}
	if rc.stats["yang.module_count"] != 556 || rc.stats["restconf.root"] != "/" {
		t.Errorf("stats = %v, want the read kept as stats, its root the origin", rc.stats)
	}
	joined := strings.Join(rc.warnings(), "\n")
	if !strings.Contains(joined, "http://") || !strings.Contains(joined, "capabilities were not read") {
		t.Errorf("warnings %q, want the unverified origin and the unread capabilities said", joined)
	}
}

func TestFacts_ARefusedCredentialFailsAndRecordsNothing(t *testing.T) {
	s := restconftest.New(t, login)
	rc := newRecorder(map[string]string{wire.SecretUsername: "admin", wire.SecretPassword: "wrong-one"})
	_, err := method(t).Invoke(context.Background(), rc, restconftest.NewDevice(t, s), nil)
	// The server echoes the header and password it was sent.
	token := base64.StdEncoding.EncodeToString([]byte("admin:wrong-one"))
	if err == nil || !strings.Contains(err.Error(), "refused the credential") || !strings.Contains(err.Error(), "request carried") ||
		strings.Contains(err.Error(), "wrong-one") || strings.Contains(err.Error(), token) {
		t.Fatalf("err = %v, want the credential refused, the echo exercised and neither form quoted", err)
	}
	if len(rc.facts) != 0 {
		t.Errorf("facts = %v, want none", rc.facts)
	}
}

func TestFacts_ACapabilityListPastTheBoundIsLeftOut(t *testing.T) {
	opts := login
	for i := range 100 {
		opts.Capabilities = append(opts.Capabilities, "urn:example:capability:"+strings.Repeat("x", 900)+":"+string(rune('a'+i%26))+strings.Repeat("y", i))
	}
	s := restconftest.New(t, opts)
	rc := newRecorder(restconftest.Secrets(s))
	if _, err := method(t).Invoke(context.Background(), rc, restconftest.NewDevice(t, s), nil); err != nil {
		t.Fatal(err)
	}
	if caps, ok := rc.facts["restconf.capabilities"]; ok {
		t.Errorf("restconf.capabilities = %v, want it left out past 60 KiB, never recorded cut or empty", caps)
	}
	if rc.facts["yang.module_count"] != 556 {
		t.Errorf("the library's facts were lost beside the left-out list: %v", rc.facts)
	}
	joined := strings.Join(rc.warnings(), "\n")
	if !strings.Contains(joined, "more than restconf.capabilities holds") || !strings.Contains(joined, "pinned certificate") {
		t.Errorf("warnings %v, want the left-out list said beside the pin's", rc.warnings())
	}
}

func TestFacts_NeedsADeviceThatServesRestconf(t *testing.T) {
	m := method(t)
	if _, err := m.Invoke(context.Background(), newRecorder(nil), nil, nil); err == nil {
		t.Error("a task with no device succeeded")
	}
	s := restconftest.New(t, login)
	dev := restconftest.NewDevice(t, s)
	dev.Base = "ftp://router.example"
	if _, err := m.Invoke(context.Background(), newRecorder(nil), dev, nil); err == nil {
		t.Error("a device with an unusable origin succeeded")
	}
}

func TestFacts_AStatThatCannotBeRecordedFailsTheTask(t *testing.T) {
	opts := login
	opts.Override = map[string]http.HandlerFunc{"/restconf/data/ietf-restconf-monitoring:restconf-state/capabilities": func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}}
	s := restconftest.New(t, opts)
	for _, failOn := range []map[string]int{{sdk.StatWarnings: 1}, {"restconf.root": 1}, {sdk.StatWarnings: 2}} {
		rc := newRecorder(restconftest.Secrets(s))
		rc.failOn = failOn
		if _, err := method(t).Invoke(context.Background(), rc, restconftest.NewDevice(t, s), nil); err == nil || !strings.Contains(err.Error(), "the stat store is full") {
			t.Errorf("failing %v: err = %v, want the store's failure", failOn, err)
		}
	}
}

// TestFacts_AReadThatLearnedNothingFailsAndRecordsNothing: both the
// capabilities and the library answered 404 or 403 (access control, after
// one answered read), so the task fails as onboarding does, rather than
// recording a device with no capability and no module over its last set.
func TestFacts_AReadThatLearnedNothingFailsAndRecordsNothing(t *testing.T) {
	notFound := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }
	opts := login
	opts.Override = map[string]http.HandlerFunc{
		"/restconf/data/ietf-yang-library:yang-library":                       notFound,
		"/restconf/data/ietf-yang-library:modules-state":                      notFound,
		"/restconf/data/ietf-restconf-monitoring:restconf-state/capabilities": notFound,
	}
	s := restconftest.New(t, opts)
	rc := newRecorder(restconftest.Secrets(s))
	_, err := method(t).Invoke(context.Background(), rc, restconftest.NewDevice(t, s), nil)
	if err == nil || !strings.Contains(err.Error(), "answered neither") {
		t.Fatalf("err = %v, want a read that learned nothing refused", err)
	}
	if len(rc.facts) != 0 {
		t.Errorf("facts = %v, want none", rc.facts)
	}
}

// TestFacts_AnUnreadLibraryRecordsNoSchemaFact: the capabilities answered and
// the library did not, so restconf.capabilities is recorded and no yang.* or
// restconf.datastores fact is, rather than a module count of zero.
func TestFacts_AnUnreadLibraryRecordsNoSchemaFact(t *testing.T) {
	notFound := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }
	opts := login
	opts.Override = map[string]http.HandlerFunc{
		"/restconf/data/ietf-yang-library:yang-library":  notFound,
		"/restconf/data/ietf-yang-library:modules-state": notFound,
	}
	s := restconftest.New(t, opts)
	rc := newRecorder(restconftest.Secrets(s))
	if _, err := method(t).Invoke(context.Background(), rc, restconftest.NewDevice(t, s), nil); err != nil {
		t.Fatal(err)
	}
	for key := range rc.stats {
		if strings.HasPrefix(key, "yang.") || key == "restconf.datastores" {
			t.Errorf("%s was recorded from a library that was not read", key)
		}
	}
	if _, ok := rc.facts["restconf.capabilities"]; !ok || rc.facts["restconf.root"] != "/restconf" {
		t.Errorf("facts = %v, want the root and the capabilities", rc.facts)
	}
	if !strings.Contains(strings.Join(rc.warnings(), "\n"), "no yang.* or restconf.datastores fact is recorded") {
		t.Errorf("warnings %v, want the left-out families said", rc.warnings())
	}
}
