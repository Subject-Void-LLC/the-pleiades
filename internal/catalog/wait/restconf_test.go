// Tests for wait.restconf against a RESTCONF server over real TLS
// (restconftest) through the method's own client path: a server whose reads
// under the root answer 502 for minutes, as a booting Catalyst 8000V's did
// (measured), is waited through; one that never answers is given up on at the
// timeout; one presenting a certificate the device does not trust ends the
// wait before any request is sent; and one refusing the login is sent it once.
package wait_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/wait"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// restconfLogin is the credential the test servers ask for.
var restconfLogin = restconftest.Options{Username: "admin", Password: "wait-Restconf-3"}

// restconfContext is the run context carrying s's credential.
func restconfContext(s *restconftest.Server) *waitContext {
	return &waitContext{secrets: restconftest.Secrets(s), stats: map[string]any{}}
}

// capabilitiesPath is where the capability list is read.
const capabilitiesPath = "/restconf/data/ietf-restconf-monitoring:restconf-state/capabilities"

// lateRESTCONF is a server whose capability read answers 502 Bad Gateway
// for its first bad requests, as IOS XE's web server does while its RESTCONF
// starts, then the list. It returns the server and its request count.
func lateRESTCONF(t *testing.T, bad int32) *restconftest.Server {
	t.Helper()
	var seen atomic.Int32
	opts := restconfLogin
	opts.Override = map[string]http.HandlerFunc{capabilitiesPath: func(w http.ResponseWriter, _ *http.Request) {
		if seen.Add(1) <= bad {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("<html><head><title>502 Bad Gateway</title></head><body><center><h1>502 Bad Gateway</h1></center><hr><center>openresty</center></body></html>"))
			return
		}
		w.Header().Set("Content-Type", "application/yang-data+xml")
		_, _ = w.Write([]byte(`<capabilities xmlns="urn:ietf:params:xml:ns:yang:ietf-restconf-monitoring"><capability>urn:ietf:params:restconf:capability:depth:1.0</capability></capabilities>`))
	}}
	return restconftest.New(t, opts)
}

func TestRestconf_Registered(t *testing.T) {
	d, ok := collection.Lookup("wait.restconf")
	if !ok || d.Manifest.Status != collection.StatusImplemented || d.Invoke == nil {
		t.Fatalf("wait.restconf: %+v", d.Manifest)
	}
	m := d.Manifest
	if m.Reversibility.Reversible || !m.Reversibility.ReadOnly || d.Check != nil || m.NoCheckReason == "" {
		t.Errorf("a wait changes nothing, has no check, and says why: %+v", m)
	}
	if len(m.RequiredCapabilities) != 1 || m.RequiredCapabilities[0] != capability.NameRestconf || m.SupportedTransports[0] != capability.TransportRestconf {
		t.Errorf("requirements %v over %v, want RestconfCapable over restconf", m.RequiredCapabilities, m.SupportedTransports)
	}
	for _, p := range m.Doc.Params {
		if strings.Contains(p.Name, "insecure") || strings.Contains(p.Name, "validate") {
			t.Errorf("wait.restconf declares %s, a way to weaken the device's TLS", p.Name)
		}
	}
}

// TestRestconf_WaitsThrough502sUntilTheServerAnswers: HTTPS answering is not
// readiness; the wait goes on through the 502s, each try on a connection of
// its own, and the stats say how it went.
func TestRestconf_WaitsThrough502sUntilTheServerAnswers(t *testing.T) {
	s := lateRESTCONF(t, 3)
	rc := restconfContext(s)
	result, err := wait.Restconf(context.Background(), rc, restconftest.NewDevice(t, s), map[string]any{"timeout": 60, "sleep": 1, "connect_timeout": 5})
	if err != nil || result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if rc.stats["root"] != "/restconf" || rc.stats["failures"] != 3 || rc.stats["tries"] != 4 {
		t.Errorf("stats %v, want the root and three failed tries of four", rc.stats)
	}
	if s.Connections() < 4 {
		t.Errorf("%d connections for 4 tries, want a fresh one each", s.Connections())
	}
	diff, _ := rc.stats[sdk.StatDiff].(map[string]any)
	if diff[sdk.DiffBefore].(map[string]any)["root"] != "/restconf" {
		t.Errorf("diff %v", rc.stats[sdk.StatDiff])
	}
	// The device pins its certificate, which the record's warning says once
	// for the whole wait, as net.restconf.* say it, not once per try.
	if warnings, _ := rc.stats[sdk.StatWarnings].([]string); len(warnings) != 1 || !strings.Contains(warnings[0], "pinned certificate") {
		t.Errorf("warnings %v, want the pinned certificate's once", rc.stats[sdk.StatWarnings])
	}
}

// TestRestconf_AWaitThatGivesUpStillSaysItsWarnings: the record's warnings
// are recorded when the wait times out too.
func TestRestconf_AWaitThatGivesUpStillSaysItsWarnings(t *testing.T) {
	s := lateRESTCONF(t, 1<<30)
	rc := restconfContext(s)
	if _, err := wait.Restconf(context.Background(), rc, restconftest.NewDevice(t, s), map[string]any{"timeout": 2, "sleep": 1, "connect_timeout": 1}); err == nil {
		t.Fatal("a server answering 502 to the end was waited for")
	}
	if warnings, _ := rc.stats[sdk.StatWarnings].([]string); len(warnings) != 1 {
		t.Errorf("warnings %v, want the pinned certificate's once", rc.stats[sdk.StatWarnings])
	}
}

// TestRestconf_GivesUpAtItsTimeoutQuotingTheLastTry: a server that answers
// 502 to the end ends the wait at the timeout, the last answer named.
func TestRestconf_GivesUpAtItsTimeoutQuotingTheLastTry(t *testing.T) {
	s := lateRESTCONF(t, 1<<30)
	_, err := wait.Restconf(context.Background(), restconfContext(s), restconftest.NewDevice(t, s), map[string]any{"timeout": 2, "sleep": 1, "connect_timeout": 1})
	if err == nil || !strings.Contains(err.Error(), "the RESTCONF server of restconf did not answer") || !strings.Contains(err.Error(), "timed out after 2s; the last try said") || !strings.Contains(err.Error(), "502") {
		t.Errorf("gave up with %v", err)
	}
}

// TestRestconf_AHostMeta404FallsBackToTheConventionalRoot: a host-meta
// answered 404 is not a failed try in itself; the read under /restconf
// decides, and here it answers.
func TestRestconf_AHostMeta404FallsBackToTheConventionalRoot(t *testing.T) {
	opts := restconfLogin
	opts.Override = map[string]http.HandlerFunc{"/.well-known/host-meta": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }}
	s := restconftest.New(t, opts)
	rc := restconfContext(s)
	if _, err := wait.Restconf(context.Background(), rc, restconftest.NewDevice(t, s), map[string]any{"timeout": 10, "sleep": 1, "connect_timeout": 2}); err != nil || rc.stats["root_source"] != "default" || rc.stats["tries"] != 1 {
		t.Errorf("a host-meta 404 over a served /restconf: %v, stats %v", err, rc.stats)
	}
}

// untrustedRestconf is a device for s whose TLS pins nothing, so only the
// system's authorities, which do not vouch for s, are trusted.
type untrustedRestconf struct {
	*restconftest.Device
	tls devicetls.Settings
}

// TLSSettings implements devicetls.Configured.
func (d *untrustedRestconf) TLSSettings() devicetls.Settings { return d.tls }

// TestRestconf_AnUntrustedCertificateEndsTheWaitBeforeAnyRequest: the wait
// stops on the first try naming the certificate and the property that would
// trust it, and the server saw no request at all, so no credential.
func TestRestconf_AnUntrustedCertificateEndsTheWaitBeforeAnyRequest(t *testing.T) {
	s := restconftest.New(t, restconfLogin)
	settings, err := devicetls.Parse(inventory.NewProperties(nil))
	if err != nil {
		t.Fatal(err)
	}
	dev := &untrustedRestconf{Device: restconftest.NewDevice(t, s), tls: settings}
	_, err = wait.Restconf(context.Background(), restconfContext(s), dev, map[string]any{"timeout": 60, "sleep": 1})
	if err == nil || !strings.Contains(err.Error(), "stopped at try 1") || !strings.Contains(err.Error(), devicetls.Fingerprint(s.Certificate())) ||
		!strings.Contains(err.Error(), devicetls.FingerprintProperty) || !strings.Contains(err.Error(), devicetls.CAPEMProperty) {
		t.Fatalf("an untrusted certificate: %v", err)
	}
	if len(s.Paths()) != 0 || s.Logins() != 0 {
		t.Errorf("the server saw %v and %d logins, want nothing", s.Paths(), s.Logins())
	}
}

// TestRestconf_ARefusedLoginEndsTheWait: a password the server refuses is
// sent once, not at every try until the timeout, and never to host-meta.
func TestRestconf_ARefusedLoginEndsTheWait(t *testing.T) {
	s := restconftest.New(t, restconfLogin)
	rc := &waitContext{secrets: map[string]string{"username": "admin", "password": "wrong"}, stats: map[string]any{}}
	_, err := wait.Restconf(context.Background(), rc, restconftest.NewDevice(t, s), map[string]any{"timeout": 60, "sleep": 1})
	if err == nil || !strings.Contains(err.Error(), "stopped at try 1") || !strings.Contains(err.Error(), "refused this login") || s.Logins() != 1 {
		t.Errorf("a refused login: %v after %d logins", err, s.Logins())
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Errorf("the error %q carries the password", err)
	}
}

// TestRestconf_Refusals: bad timing, no device, a device serving no
// RESTCONF and an unusable record are refused before any request.
func TestRestconf_Refusals(t *testing.T) {
	s := restconftest.New(t, restconfLogin)
	if _, err := wait.Restconf(context.Background(), restconfContext(s), restconftest.NewDevice(t, s), map[string]any{"timeout": 0}); err == nil {
		t.Error("a zero timeout was accepted")
	}
	if _, err := wait.Restconf(context.Background(), restconfContext(s), nil, nil); err == nil {
		t.Error("waited for no device")
	}
	plain := &waitTarget{Stub: newWaitStub(), host: "127.0.0.1", port: 1}
	if _, err := wait.Restconf(context.Background(), restconfContext(s), plain, nil); err == nil || !strings.Contains(err.Error(), "serves no RESTCONF") {
		t.Errorf("a device serving no RESTCONF: %v", err)
	}
	bad := restconftest.NewDevice(t, s)
	bad.Base = "ftp://router.example"
	if _, err := wait.Restconf(context.Background(), restconfContext(s), bad, map[string]any{"timeout": 60, "sleep": 1}); err == nil || !strings.Contains(err.Error(), "stopped at try 1") {
		t.Errorf("an unusable record: %v", err)
	}
	if len(s.Paths()) != 0 {
		t.Errorf("requests %v reached the server", s.Paths())
	}
}

// TestRestconf_AStatItCannotRecordFailsTheTask: a ready server whose diff,
// root, tries or warnings stat cannot be recorded fails the task.
func TestRestconf_AStatItCannotRecordFailsTheTask(t *testing.T) {
	for _, key := range []string{sdk.StatDiff, "root", "tries", sdk.StatWarnings} {
		t.Run(key, func(t *testing.T) {
			s := restconftest.New(t, restconfLogin)
			rc := restconfContext(s)
			rc.failOn = key
			_, err := wait.Restconf(context.Background(), rc, restconftest.NewDevice(t, s), map[string]any{"timeout": 10, "sleep": 1, "connect_timeout": 5})
			if !errors.Is(err, errWaitStat) {
				t.Errorf("failing the %s stat: %v", key, err)
			}
		})
	}
}

// iosxeLogin is restconftest's IOS XE mode, answering as a Catalyst 8000V
// (IOS XE 17.15.1a) did on 2026-11-15.
var iosxeLogin = restconftest.Options{IOSXE: true, Username: "admin", Password: "iosxe-Wait-4"}

// TestRestconf_IOSXEStartingIsWaitedThrough: IOS XE answers host-meta 404 to
// no login, so each try takes /restconf; its reads answer Cisco's 502 page
// while RESTCONF starts, then the capability list, and host-meta never sees
// the credential.
func TestRestconf_IOSXEStartingIsWaitedThrough(t *testing.T) {
	opts := iosxeLogin
	opts.Starting = 3
	s := restconftest.New(t, opts)
	rc := restconfContext(s)
	if _, err := wait.Restconf(context.Background(), rc, restconftest.NewDevice(t, s), map[string]any{"timeout": 60, "sleep": 1, "connect_timeout": 5}); err != nil {
		t.Fatal(err)
	}
	if rc.stats["root"] != "/restconf" || rc.stats["root_source"] != "default" || rc.stats["tries"] != 4 || rc.stats["failures"] != 3 {
		t.Errorf("stats %v, want /restconf from the default after three 502s", rc.stats)
	}
	if s.HostMetaLogins() != 0 || s.Logins() != 4 {
		t.Errorf("%d credentials sent, %d to host-meta; want one per try, none to host-meta", s.Logins(), s.HostMetaLogins())
	}
}

// TestRestconf_IOSXEAWrongPasswordEndsTheWaitAfterOneLogin: IOS XE answers
// host-meta 404 to a wrong login as to none; the wait sends it none, and the
// read under /restconf answers 401, which ends the wait after one attempt.
func TestRestconf_IOSXEAWrongPasswordEndsTheWaitAfterOneLogin(t *testing.T) {
	s := restconftest.New(t, iosxeLogin)
	rc := &waitContext{secrets: map[string]string{"username": "admin", "password": "wrong"}, stats: map[string]any{}}
	_, err := wait.Restconf(context.Background(), rc, restconftest.NewDevice(t, s), map[string]any{"timeout": 60, "sleep": 1})
	if err == nil || !strings.Contains(err.Error(), "stopped at try 1") || s.Logins() != 1 || s.HostMetaLogins() != 0 {
		t.Errorf("a wrong password: %v after %d logins (%d to host-meta)", err, s.Logins(), s.HostMetaLogins())
	}
}

// TestRestconf_NoRestconfAtTheDefaultRootIsAFailedTry: host-meta 404 and a
// 404 under /restconf may be a server not serving yet, so the wait goes on
// to its timeout.
func TestRestconf_NoRestconfAtTheDefaultRootIsAFailedTry(t *testing.T) {
	opts := restconfLogin
	opts.Root = "/api/rc"
	opts.Override = map[string]http.HandlerFunc{"/.well-known/host-meta": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }}
	s := restconftest.New(t, opts)
	_, err := wait.Restconf(context.Background(), restconfContext(s), restconftest.NewDevice(t, s), map[string]any{"timeout": 2, "sleep": 1, "connect_timeout": 1})
	if err == nil || !strings.Contains(err.Error(), "timed out") || strings.Contains(err.Error(), "stopped") {
		t.Errorf("no RESTCONF at /restconf: %v", err)
	}
}

// TestRestconf_ARootAtTheOriginIsSaidAsSlash: host-meta naming the origin
// itself as the root is waited for there, and the root stat says "/".
func TestRestconf_ARootAtTheOriginIsSaidAsSlash(t *testing.T) {
	opts := restconfLogin
	opts.HostMeta = `<XRD xmlns='http://docs.oasis-open.org/ns/xri/xrd-1.0'><Link rel='restconf' href='/'/></XRD>`
	opts.Override = map[string]http.HandlerFunc{"/data/ietf-restconf-monitoring:restconf-state/capabilities": func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yang-data+xml")
		_, _ = w.Write([]byte(`<capabilities xmlns="urn:ietf:params:xml:ns:yang:ietf-restconf-monitoring"><capability>urn:ietf:params:restconf:capability:depth:1.0</capability></capabilities>`))
	}}
	s := restconftest.New(t, opts)
	rc := restconfContext(s)
	if _, err := wait.Restconf(context.Background(), rc, restconftest.NewDevice(t, s), map[string]any{"timeout": 10, "sleep": 1, "connect_timeout": 2}); err != nil || rc.stats["root"] != "/" {
		t.Errorf("a root at the origin: %v, stats %v", err, rc.stats)
	}
}
