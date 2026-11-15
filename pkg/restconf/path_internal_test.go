// Package restconf: tests of the path builder, the answer classifier and the
// error reader, the parts a test server would reach only by accident.
package restconf

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

func TestEscaped_BuildsRFC8040Paths(t *testing.T) {
	cases := []struct {
		path Path
		want string
	}{
		{nil, ""},
		{Path{Node("ietf-interfaces", "interfaces")}, "ietf-interfaces:interfaces"},
		{Path{Node("ietf-interfaces", "interfaces"), Node("", "interface").Entry("eth0")}, "ietf-interfaces:interfaces/interface=eth0"},
		{Path{Node("m", "list").Entry("a,b", "", "c/d")}, "m:list=a%2Cb,,c%2Fd"},
		{Path{Node("m", "x"), Node("other", "y")}, "m:x/other:y"},
		{Path{Node("m", "l").Entry("caf\u00e9 50% ~_.-")}, "m:l=caf%C3%A9%2050%25%20~_.-"},
	}
	for _, tc := range cases {
		got, err := tc.path.Escaped()
		if err != nil || got != tc.want {
			t.Errorf("%+v.Escaped() = %q, %v; want %q", tc.path, got, err, tc.want)
		}
	}
}

func TestEscaped_RefusesWhatIsNotAPath(t *testing.T) {
	long := make(Path, MaxSegments+1)
	for i := range long {
		long[i] = Node("m", "n")
	}
	cases := map[string]Path{
		"too many nodes":           long,
		"a first node unqualified": {Node("", "interfaces")},
		"a name not an identifier": {Node("m", "1bad")},
		"an empty name":            {Node("m", "")},
		"a module not one":         {Node("bad module", "x")},
		"an entry with no key":     {Node("m", "l").Entry()},
		"a key past the bound":     {Node("m", "l").Entry(strings.Repeat("k", MaxKeyBytes+1))},
		"a key not UTF-8":          {Node("m", "l").Entry("\xff")},
		"a key with a newline":     {Node("m", "l").Entry("a\nb")},
		"a key with a C1 control":  {Node("m", "l").Entry("a\u0085b")},
		"a key with DEL":           {Node("m", "l").Entry("a\x7fb")},
		"a later node bad":         {Node("m", "x"), Node("", "a/b")},
	}
	for name, p := range cases {
		if got, err := p.Escaped(); err == nil {
			t.Errorf("%s: Escaped() = %q, want a refusal", name, got)
		}
	}
}

func TestEntry_CopiesItsKeys(t *testing.T) {
	keys := []string{"a"}
	s := Node("m", "l").Entry(keys...)
	keys[0] = "changed"
	if s.Keys[0] != "a" {
		t.Error("Entry kept the caller's slice")
	}
}

// FuzzEscaped holds the builder to its promise for any key: the path either
// is refused, or names exactly the nodes and keys it was given, each key
// decoding back to itself, and joins under a device's origin with every
// escape kept, unless a decoded "/" would make a dot segment, which the join
// refuses too.
func FuzzEscaped(f *testing.F) {
	for _, seed := range []string{"", "eth0", "GigabitEthernet0/0/1", "a,b", "../..", "%2F", "50%", "a=b", "x;y", "\x00", "caf\u00e9", "/.", "a/../b"} {
		f.Add(seed, "second")
	}
	base, err := url.Parse("https://192.0.2.1")
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, key1, key2 string) {
		p := Path{Node("ietf-interfaces", "interfaces"), Node("", "interface").Entry(key1, key2)}
		escaped, err := p.Escaped()
		if err != nil {
			return
		}
		segments := strings.Split(escaped, "/")
		if len(segments) != 2 || segments[0] != "ietf-interfaces:interfaces" {
			t.Fatalf("%q split into %q, want two nodes", escaped, segments)
		}
		name, keyPart, found := strings.Cut(segments[1], "=")
		if !found || name != "interface" {
			t.Fatalf("%q: the entry is %q, want interface=...", escaped, segments[1])
		}
		keys := strings.Split(keyPart, ",")
		if len(keys) != 2 {
			t.Fatalf("%q holds %d keys, want 2", escaped, len(keys))
		}
		for i, want := range []string{key1, key2} {
			got, err := url.PathUnescape(keys[i])
			if err != nil || got != want {
				t.Fatalf("key %d of %q decodes to %q (%v), want %q", i, escaped, got, err, want)
			}
		}
		u, err := httpapi.Join(base, "/restconf/data/"+escaped)
		if err != nil {
			// Join refuses only a dot segment a decoded "/" makes.
			if !strings.Contains(key1+key2, "/") {
				t.Fatalf("Join refused %q: %v", escaped, err)
			}
			return
		}
		if got := u.EscapedPath(); got != "/restconf/data/"+escaped {
			t.Fatalf("Join sent %q, want %q exactly", got, "/restconf/data/"+escaped)
		}
	})
}

func TestIsJSON_ByMediaTypeThenByFirstByte(t *testing.T) {
	cases := []struct {
		a    answer
		want bool
	}{
		{answer{mediaType: MediaTypeJSON}, true},
		{answer{mediaType: "application/json", body: []byte("<x/>")}, true},
		{answer{mediaType: MediaTypeXML, body: []byte("{}")}, false},
		{answer{mediaType: "text/xml"}, false},
		{answer{body: []byte("  \n{\"a\":1}")}, true},
		{answer{body: []byte("[1]")}, true},
		{answer{body: []byte("<x/>")}, false},
	}
	for _, tc := range cases {
		if got := tc.a.isJSON(); got != tc.want {
			t.Errorf("isJSON(%+v) = %v, want %v", tc.a, got, tc.want)
		}
	}
	if mediaTypeOf("Application/YANG-Data+JSON; charset=utf-8") != MediaTypeJSON || mediaTypeOf(";;") != "" {
		t.Error("mediaTypeOf did not read a media type, or read one that does not parse")
	}
}

func TestStatusError_ReadsAndBoundsTheServersErrors(t *testing.T) {
	if e := statusError(answer{status: 404}); len(e.Errors) != 0 || e.Said != "" || e.Error() != "the device answered 404 Not Found" {
		t.Errorf("an empty answer = %q, %+v", e.Error(), e)
	}
	var b strings.Builder
	b.WriteString(`{"ietf-restconf:errors":{"error":[`)
	for i := range maxErrors + 3 {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"error-type":"protocol","error-tag":"lock-denied"}`)
	}
	b.WriteString(`]}}`)
	e := statusError(answer{status: 409, mediaType: MediaTypeJSON, body: []byte(b.String())})
	if len(e.Errors) != maxErrors || !e.HasTag("lock-denied") {
		t.Errorf("kept %d errors, want %d", len(e.Errors), maxErrors)
	}
	for _, body := range []string{`{"other":{}}`, `{"ietf-restconf:errors":`} {
		if got := errorsFromJSON([]byte(body)); got != nil {
			t.Errorf("errorsFromJSON(%q) = %v, want none", body, got)
		}
	}
	if got := errorsFromXML([]byte(`<errors xmlns="urn:example"><error><error-tag>x</error-tag></error></errors>`)); got != nil {
		t.Errorf("errorsFromXML read another namespace's errors: %v", got)
	}
}

// refusingClient is a client, its root already found, of a plain HTTP
// server answering every request with handler.
func refusingClient(t *testing.T, handler http.HandlerFunc, auth string) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, srv.Client(), auth, auth != httpapi.AuthNone, map[string]string{"username": "u", "password": "p"})
	if err != nil {
		t.Fatal(err)
	}
	c.root, c.rootFound = "/restconf", true
	return c
}

func TestGet_AnAuthenticated403IsAccessControlNotARefusedCredential(t *testing.T) {
	c := refusingClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", MediaTypeXML)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<errors xmlns="urn:ietf:params:xml:ns:yang:ietf-restconf"><error><error-type>application</error-type><error-tag>access-denied</error-tag></error></errors>`))
	}, httpapi.AuthBasic)
	_, _, err := c.GetData(context.Background(), pathYANGLibrary)
	var status *StatusError
	if !errors.As(err, &status) || !status.HasTag("access-denied") {
		t.Fatalf("GetData = %v, want the access-denied StatusError", err)
	}
	d := yanglib.Discovery{}
	if err := c.library(context.Background(), &d); err != nil || !strings.Contains(d.LibraryError, "access-denied") {
		t.Errorf("library = %v, LibraryError %q; want the access refusal said, not fatal", err, d.LibraryError)
	}
}

func TestDiscover_ATransportFailureAfterTheRootIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Every data read finds the connection cut.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("no hijacker")
			return
		}
		conn, _, err := hj.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, srv.Client(), httpapi.AuthNone, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.root, c.rootFound = "/restconf", true
	if _, err := c.Discover(context.Background()); err == nil {
		t.Error("Discover over a cut connection succeeded")
	}
	d := yanglib.Discovery{}
	if err := c.library(context.Background(), &d); err == nil {
		t.Error("library over a cut connection was not an error")
	}
}

func TestCapabilities_RefusesWhatIsNotTheMonitoringList(t *testing.T) {
	for name, tc := range map[string]struct{ mediaType, body string }{
		"another XML root": {MediaTypeXML, `<capabilities xmlns="urn:example"><capability>x</capability></capabilities>`},
		"broken XML":       {MediaTypeXML, `<capabilities`},
		"too many":         {MediaTypeJSON, `{"ietf-restconf-monitoring:capabilities":{"capability":[` + strings.TrimSuffix(strings.Repeat(`"x",`, maxCapabilities+1), ",") + `]}}`},
	} {
		c := refusingClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", tc.mediaType)
			_, _ = w.Write([]byte(tc.body))
		}, httpapi.AuthNone)
		if caps, err := c.capabilities(context.Background()); err == nil || !errors.Is(err, errUnusable) {
			t.Errorf("%s: capabilities = %v, %v; want an unusable answer", name, caps, err)
		}
	}
}

func TestGet_RefusesARefThatLeavesTheOrigin(t *testing.T) {
	c := refusingClient(t, func(http.ResponseWriter, *http.Request) { t.Error("a request was sent") }, httpapi.AuthNone)
	if _, err := c.get(context.Background(), "//elsewhere/x", acceptYANG, 10); err == nil {
		t.Error("get sent a protocol-relative ref")
	}
	c.auth = httpapi.AuthBearer
	c.secrets = nil
	if _, err := c.get(context.Background(), "/x", acceptYANG, 10); err == nil {
		t.Error("get sent a bearer request with no token")
	}
	c.auth = httpapi.AuthNone
	c.rootFound = true
	if _, _, err := c.GetData(context.Background(), Path{Node("", "x")}); err == nil {
		t.Error("GetData sent an unqualified first node")
	}
}

func TestClientFor_ARequestThatNeverAnswersTimesOut(t *testing.T) {
	old := requestTimeout
	requestTimeout = 200 * time.Millisecond
	t.Cleanup(func() { requestTimeout = old })
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release); srv.Close() })
	c, _, err := ClientFor(plainDevice{base: srv.URL}, "router", nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := c.DiscoverRoot(context.Background()); err == nil {
		t.Fatal("DiscoverRoot against a silent server succeeded")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("the request took %s, want the timeout to end it", time.Since(start))
	}
}

// plainDevice is an http:// RESTCONF device sending no credential.
type plainDevice struct{ base string }

// RestconfBaseURL implements capability.RestconfCapable.
func (d plainDevice) RestconfBaseURL() string { return d.base }

// RestconfAuth implements capability.RestconfCapable.
func (plainDevice) RestconfAuth() string { return httpapi.AuthNone }

// RestconfAllowPlaintextCredentials implements capability.RestconfCapable.
func (plainDevice) RestconfAllowPlaintextCredentials() bool { return false }

// TestCapabilities_ReadsTheContainerOrItsParent holds both shapes servers
// answer with: the capabilities container itself (IOS XE) and wrapped in
// restconf-state (rousette, measured 2026-11-14), in either encoding.
func TestCapabilities_ReadsTheContainerOrItsParent(t *testing.T) {
	const ns = `xmlns="urn:ietf:params:xml:ns:yang:ietf-restconf-monitoring"`
	for _, body := range []string{
		`<capabilities ` + ns + `><capability>urn:a</capability><capability>urn:b</capability></capabilities>`,
		`<restconf-state ` + ns + `><capabilities><capability>urn:a</capability><capability>urn:b</capability></capabilities></restconf-state>`,
	} {
		if got, ok := capabilitiesFromXML([]byte(body)); !ok || strings.Join(got, ",") != "urn:a,urn:b" {
			t.Errorf("capabilitiesFromXML(%s) = %v, %v", body, got, ok)
		}
	}
	for _, body := range []string{
		`{"ietf-restconf-monitoring:capabilities":{"capability":["urn:a","urn:b"]}}`,
		`{"ietf-restconf-monitoring:restconf-state":{"capabilities":{"capability":["urn:a","urn:b"]}}}`,
	} {
		if got, ok := capabilitiesFromJSON([]byte(body)); !ok || strings.Join(got, ",") != "urn:a,urn:b" {
			t.Errorf("capabilitiesFromJSON(%s) = %v, %v", body, got, ok)
		}
	}
	for _, body := range []string{`<restconf-state ` + ns + `/>`, `<other ` + ns + `/>`, `<capabilities/>`} {
		if _, ok := capabilitiesFromXML([]byte(body)); ok {
			t.Errorf("capabilitiesFromXML read %s", body)
		}
	}
	for _, body := range []string{`{"ietf-restconf-monitoring:restconf-state":{}}`, `{}`, `[`} {
		if _, ok := capabilitiesFromJSON([]byte(body)); ok {
			t.Errorf("capabilitiesFromJSON read %s", body)
		}
	}
}

// TestRestconfLink_ReadsAnUnclosedLink: rousette's host-meta leaves its Link
// unclosed (measured 2026-11-14); it is read, and a self-closed one still is.
func TestRestconfLink_ReadsAnUnclosedLink(t *testing.T) {
	for _, doc := range []string{
		`<XRD xmlns='http://docs.oasis-open.org/ns/xri/xrd-1.0'><Link rel='restconf' href='/restconf/'></XRD>`,
		`<XRD xmlns='http://docs.oasis-open.org/ns/xri/xrd-1.0'><Link rel='restconf' href='/restconf/'/></XRD>`,
		`<XRD><Link rel='restconf' href='/restconf/'></Link></XRD>`,
	} {
		if href, err := restconfLink([]byte(doc)); err != nil || href != "/restconf/" {
			t.Errorf("restconfLink(%s) = %q, %v", doc, href, err)
		}
	}
}

// TestRefusedError_IsNotAStatusError: a refused login ends a discovery, so it
// must not read as one resource the server lacks.
func TestRefusedError_IsNotAStatusError(t *testing.T) {
	var err error = &RefusedError{Answer: &StatusError{Status: http.StatusUnauthorized}}
	var status *StatusError
	if errors.As(err, &status) || !isFatal(err) {
		t.Error("a refused credential reads as a StatusError, or not as fatal")
	}
	if !strings.Contains(err.Error(), "refused the credential") {
		t.Errorf("Error() = %q", err)
	}
	none := &RefusedError{Answer: &StatusError{Status: http.StatusForbidden}, NoneSent: true}
	if !strings.Contains(none.Error(), "http_auth") || !strings.Contains(none.Error(), "403") {
		t.Errorf("Error() = %q, want the property named", none)
	}
}
