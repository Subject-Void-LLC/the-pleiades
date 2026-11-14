// Package netconf: tests of what a session discovers when it opens, against a
// real Catalyst 8000V's captured hello and YANG library replies.
package netconf

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
)

// The fixtures in testdata were captured on 2026-11-14 from a Cisco Catalyst
// 8000V running IOS XE 17.15.1a in a CML lab, with its candidate datastore
// off: the hello's capabilities (one per line), and the device's verbatim
// rpc-replies to a get of ietf-yang-library:yang-library (RFC 8525) and of
// modules-state (RFC 7895). The hello lists 338 modules; the library 559,
// which is RFC 7950 section 5.6.4 measured: a YANG 1.1 server's hello need not
// name its modules.

// fixture reads one gzipped capture from testdata.
func fixture(t *testing.T, name string) string {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// c8kHelloCapabilities is the real device's hello capabilities, one each.
func c8kHelloCapabilities(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(fixture(t, "c8k-17.15.1a-hello-capabilities.txt.gz"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// helloOf is a hello message advertising capabilities, escaped as XML.
func helloOf(capabilities []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><capabilities>`)
	for _, c := range capabilities {
		b.WriteString("<capability>" + strings.ReplaceAll(c, "&", "&amp;") + "</capability>")
	}
	b.WriteString(`</capabilities><session-id>42</session-id></hello>`)
	return b.String()
}

// realMessageID is the message-id attribute a captured reply carries, which a
// scripted device replaces with the request's.
var realMessageID = regexp.MustCompile(`message-id="[^"]*"`)

// replyTo is a captured reply answering request.
func replyTo(request, captured string) string {
	return realMessageID.ReplaceAllString(captured, `message-id="`+messageIDOf(request)+`"`)
}

func TestDiscoverFromHello_ARealIOSXEHello(t *testing.T) {
	d := discoverFromHello(c8kHelloCapabilities(t))
	if d.Dropped != 0 {
		t.Errorf("Dropped = %d (%v), want none from a real device's hello", d.Dropped, d.DroppedSamples)
	}
	if len(d.Modules) != 338 {
		t.Errorf("hello modules = %d, want the 338 the capture lists", len(d.Modules))
	}
	// Tail-f's actions capability names no module: it is a protocol
	// capability, kept, not a malformed module.
	if !containsString(d.Protocol, "http://tail-f.com/ns/netconf/actions/1.0") {
		t.Errorf("Protocol = %v, want Tail-f's actions capability kept", d.Protocol)
	}
	if d.Completeness != Partial {
		t.Errorf("Completeness = %q, want partial: the hello advertises a YANG library the session has not read", d.Completeness)
	}
	if d.ContentID == "" {
		t.Error("ContentID is empty, want the yang-library:1.1 capability's content-id")
	}
	if got := strings.Join(d.Datastores, ","); got != "running" {
		t.Errorf("Datastores = %q, want running alone: this capture was taken with the candidate datastore off", got)
	}
}

func TestReadLibrary_ARealIOSXEYANGLibrary(t *testing.T) {
	modules, datastores, err := readLibrary([]byte(fixture(t, "c8k-17.15.1a-yang-library-reply.xml.gz")))
	if err != nil {
		t.Fatal(err)
	}
	var d Discovery
	kept := keepModules(modules, &d)
	if len(kept) != 559 || d.Dropped != 0 {
		t.Fatalf("library modules = %d with %d dropped (%v), want all 559 kept", len(kept), d.Dropped, d.DroppedSamples)
	}
	native := findModule(kept, "Cisco-IOS-XE-native")
	if native == nil || native.Namespace != "http://cisco.com/ns/yang/Cisco-IOS-XE-native" || native.Revision != "2024-07-01" {
		t.Errorf("Cisco-IOS-XE-native = %+v, want its namespace and the 2024-07-01 revision", native)
	}
	if got := strings.Join(datastoresFrom(nil, datastores), ","); got != "intended,operational,running" {
		t.Errorf("datastores = %q, want the NMDA set the library lists", got)
	}
}

// TestReadLibrary_ModulesStateLeavesImportOnlyModulesOut holds RFC 7895's
// form to RFC 8525's on the same device: modules-state lists 613 modules,
// import-only ones among them, and with those left out the implemented set
// is the yang-library's own.
func TestReadLibrary_ModulesStateLeavesImportOnlyModulesOut(t *testing.T) {
	library, _, err := readLibrary([]byte(fixture(t, "c8k-17.15.1a-yang-library-reply.xml.gz")))
	if err != nil {
		t.Fatal(err)
	}
	state, _, err := readLibrary([]byte(fixture(t, "c8k-17.15.1a-modules-state-reply.xml.gz")))
	if err != nil {
		t.Fatal(err)
	}
	var a, b Discovery
	fromLibrary, fromState := keepModules(library, &a), keepModules(state, &b)
	if len(fromState) == 0 {
		t.Fatal("modules-state gave no modules")
	}
	names := func(ms []Module) map[string]bool {
		out := map[string]bool{}
		for _, m := range ms {
			out[m.Name+"@"+m.Revision] = true
		}
		return out
	}
	lib, st := names(fromLibrary), names(fromState)
	for k := range st {
		if !lib[k] {
			t.Errorf("modules-state implements %s, which the yang-library does not list", k)
		}
	}
	if len(st) > len(lib) {
		t.Errorf("modules-state gave %d implemented modules, the yang-library %d", len(st), len(lib))
	}
}

// TestReadLibrary_PrefixesDeclaredOnTheReply reads a reply that declares its
// namespaces as prefixes on rpc-reply, as Junos does, which data's inner XML
// would carry with unbound prefixes.
func TestReadLibrary_PrefixesDeclaredOnTheReply(t *testing.T) {
	reply := `<nc:rpc-reply xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" xmlns:yl="urn:ietf:params:xml:ns:yang:ietf-yang-library" message-id="1">` +
		`<nc:data><yl:yang-library><yl:module-set><yl:name>all</yl:name>` +
		`<yl:module><yl:name>junos-conf-root</yl:name><yl:revision>2023-01-01</yl:revision><yl:namespace>http://yang.juniper.net/junos/conf/root</yl:namespace><yl:feature>f1</yl:feature></yl:module>` +
		`</yl:module-set><yl:datastore><yl:name xmlns:ds="urn:ietf:params:xml:ns:yang:ietf-datastores">ds:running</yl:name></yl:datastore>` +
		`<yl:content-id>7</yl:content-id></yl:yang-library></nc:data></nc:rpc-reply>`
	modules, datastores, err := readLibrary([]byte(reply))
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 1 || modules[0].Name != "junos-conf-root" || modules[0].Namespace != "http://yang.juniper.net/junos/conf/root" || len(modules[0].Features) != 1 {
		t.Errorf("modules = %+v, want the one prefixed module with its feature", modules)
	}
	if len(datastores) != 1 || datastores[0] != "running" {
		t.Errorf("datastores = %v, want running, read from the ds: identityref", datastores)
	}
}

func TestKeepModules_RefusesAMalformedEntryWhole(t *testing.T) {
	ok := Module{Name: "good", Namespace: "urn:good", Revision: "2024-01-01", Features: []string{"b", "a", "bad feature"}}
	var d Discovery
	kept := keepModules([]Module{
		ok,
		ok, // a duplicate
		{Name: "bad\x1bname", Namespace: "urn:x"},
		{Name: "rev", Namespace: "urn:x", Revision: "2024-1-1"},
		{Name: "ns", Namespace: "urn:\xe2\x80\xaex"},
		{Name: "long", Namespace: "urn:" + strings.Repeat("x", maxNamespaceBytes)},
		{Name: "", Namespace: "urn:x"},
		{Name: "alpha", Namespace: "urn:alpha"},
	}, &d)
	if len(kept) != 2 || kept[0].Name != "alpha" || kept[1].Name != "good" {
		t.Fatalf("kept = %+v, want alpha and good, sorted", kept)
	}
	if got := strings.Join(kept[1].Features, ","); got != "a,b" {
		t.Errorf("features = %q, want the identifiers alone, sorted", got)
	}
	if d.Dropped != 5 {
		t.Errorf("Dropped = %d, want the five malformed entries", d.Dropped)
	}
	for _, s := range d.DroppedSamples {
		if strings.ContainsAny(s, "\x1b\xe2\x80\xae") {
			t.Errorf("sample %s carries a control character; samples are quoted", s)
		}
	}
}

func TestDiscovery_CompletenessDecidesWhetherANamespaceIsKnown(t *testing.T) {
	module := "urn:ietf:params:xml:ns:yang:ietf-interfaces?module=ietf-interfaces&revision=2018-02-20"
	cases := []struct {
		name string
		caps []string
		want Completeness
	}{
		{"no modules and no library", []string{CapabilityBase11}, Unknown},
		{"modules and no library", []string{CapabilityBase11, module}, Complete},
		{"a library not yet read", []string{CapabilityBase11, module, CapabilityYANGLibrary11 + "?content-id=1"}, Partial},
	}
	for _, tc := range cases {
		d := discoverFromHello(tc.caps)
		if d.Completeness != tc.want {
			t.Errorf("%s: Completeness = %q, want %q", tc.name, d.Completeness, tc.want)
		}
		served, known := d.Serves("urn:ietf:params:xml:ns:yang:ietf-interfaces")
		if known != (tc.want == Complete) || (known && !served) {
			t.Errorf("%s: Serves = %v, %v", tc.name, served, known)
		}
	}
	d := discoverFromHello([]string{CapabilityBase11, module})
	if served, known := d.Serves("urn:example:absent"); served || !known {
		t.Errorf("Serves(absent) = %v, %v; want not served, known", served, known)
	}
}

func TestModulesDigest_IsTheSetNotTheOrder(t *testing.T) {
	a := discoverFromHello([]string{CapabilityBase11, "urn:a?module=a&revision=2020-01-01", "urn:b?module=b&features=y,x"})
	b := discoverFromHello([]string{"urn:b?module=b&features=x,y", CapabilityBase11, "urn:a?module=a&revision=2020-01-01"})
	if a.ModulesDigest() == "" || a.ModulesDigest() != b.ModulesDigest() {
		t.Errorf("digests %q and %q differ for one module set listed two ways", a.ModulesDigest(), b.ModulesDigest())
	}
	c := discoverFromHello([]string{CapabilityBase11, "urn:a?module=a&revision=2020-01-02", "urn:b?module=b&features=x,y"})
	if c.ModulesDigest() == a.ModulesDigest() {
		t.Error("a changed revision kept the same digest")
	}
}

// libraryDevice is a scripted device advertising a YANG library with
// contentID, answering a get of it with the real capture and counting the
// gets.
func libraryDevice(t *testing.T, contentID string, extra ...string) (*fakeDevice, *int) {
	t.Helper()
	library := fixture(t, "c8k-17.15.1a-yang-library-reply.xml.gz")
	gets := 0
	caps := append([]string{CapabilityBase10, CapabilityBase11, CapabilityYANGLibrary11 + "?revision=2019-01-04&content-id=" + contentID}, extra...)
	d := &fakeDevice{hello: helloOf(caps), respond: func(req string) string {
		if strings.Contains(req, "<get>") {
			gets++
			return replyTo(req, library)
		}
		return fmt.Sprintf(`<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="%s"><ok/></rpc-reply>`, messageIDOf(req))
	}}
	return d, &gets
}

func TestOpen_ReadsTheYANGLibraryAndCachesItByContentID(t *testing.T) {
	key := t.Name()
	t.Cleanup(func() { libraryCache.Range(func(k, _ any) bool { libraryCache.Delete(k); return true }) })

	d1, gets1 := libraryDevice(t, "c1")
	s := openAgainst(t, d1, Options{DiscoveryCacheKey: key})
	if got := s.Discovery(); got.Completeness != Complete || len(got.Modules) != 559 || got.Source != "yang-library:1.1" {
		t.Fatalf("Discovery = %s, %d modules from %s; want complete, 559, from the library", got.Completeness, len(got.Modules), got.Source)
	}
	if *gets1 != 1 {
		t.Errorf("gets = %d, want one read of the library", *gets1)
	}

	d2, gets2 := libraryDevice(t, "c1")
	if s := openAgainst(t, d2, Options{DiscoveryCacheKey: key}); len(s.Discovery().Modules) != 559 || *gets2 != 0 {
		t.Errorf("a second session to an unchanged device read the library %d times, want 0 (cached), and %d modules", *gets2, len(s.Discovery().Modules))
	}
	d3, gets3 := libraryDevice(t, "c2")
	if openAgainst(t, d3, Options{DiscoveryCacheKey: key}); *gets3 != 1 {
		t.Errorf("a changed content-id read the library %d times, want 1", *gets3)
	}
	// A session with no key (its host key was not verified) neither reads
	// nor fills the cache.
	d4, gets4 := libraryDevice(t, "c1")
	if openAgainst(t, d4, Options{}); *gets4 != 1 {
		t.Errorf("a session with no cache key read the library %d times, want 1", *gets4)
	}
}

func TestOpen_ARefusedLibraryReadLeavesTheHelloPartial(t *testing.T) {
	caps := []string{CapabilityBase11, CapabilityYANGLibrary11 + "?content-id=x", "urn:a?module=a"}
	d := &fakeDevice{hello: helloOf(caps), respond: func(req string) string {
		return fmt.Sprintf(`<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="%s"><rpc-error><error-type>application</error-type><error-tag>access-denied</error-tag><error-severity>error</error-severity></rpc-error></rpc-reply>`, messageIDOf(req))
	}}
	got := openAgainst(t, d, Options{}).Discovery()
	if got.Completeness != Partial || got.LibraryError == "" || len(got.Modules) != 1 {
		t.Errorf("Discovery = %s, %q, %d modules; want partial, the refusal said, the hello's module kept", got.Completeness, got.LibraryError, len(got.Modules))
	}
}

func TestOpen_ChoosesTheTargetFromTheHello(t *testing.T) {
	cases := []struct {
		name string
		caps []string
		opts Options
		want Datastore
	}{
		{"writable-running", []string{CapabilityBase11, CapabilityWritableRunning, CapabilityCandidate}, Options{ChooseTarget: true}, Running},
		{"candidate alone", []string{CapabilityBase11, CapabilityCandidate}, Options{ChooseTarget: true}, Candidate},
		{"neither", []string{CapabilityBase11}, Options{ChooseTarget: true}, Running},
		{"not asked to choose", []string{CapabilityBase11, CapabilityCandidate}, Options{}, Running},
		{"named", []string{CapabilityBase11, CapabilityWritableRunning, CapabilityCandidate}, Options{Target: Candidate, ChooseTarget: true}, Candidate},
	}
	for _, tc := range cases {
		d := &fakeDevice{hello: helloOf(tc.caps)}
		if got := openAgainst(t, d, tc.opts).Target(); got != tc.want {
			t.Errorf("%s: Target() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDo_AWarningBesideOKIsNotAFailure(t *testing.T) {
	d := &fakeDevice{hello: realHelloPrelude, respond: func(req string) string {
		return fmt.Sprintf(`<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="%s"><rpc-error><error-type>application</error-type><error-tag>operation-failed</error-tag><error-severity>warning</error-severity><error-message>statement deprecated</error-message></rpc-error><ok/></rpc-reply>`, messageIDOf(req))
	}}
	s := openAgainst(t, d, Options{})
	if err := s.EditConfig(context.Background(), EditConfigRequest{Config: "<x/>"}); err != nil {
		t.Fatalf("EditConfig = %v, want the warning kept apart from the answer", err)
	}
	if w := s.TakeWarnings(); len(w) != 1 || w[0].Message != "statement deprecated" {
		t.Errorf("TakeWarnings = %+v, want the one warning", w)
	}
	if w := s.TakeWarnings(); len(w) != 0 {
		t.Errorf("TakeWarnings again = %+v, want none", w)
	}
}

func TestValidate_AValidate11OnlyServerValidates(t *testing.T) {
	d := &fakeDevice{hello: helloOf([]string{CapabilityBase11, CapabilityCandidate, CapabilityValidate11}), respond: func(req string) string {
		return fmt.Sprintf(`<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="%s"><ok/></rpc-reply>`, messageIDOf(req))
	}}
	s := openAgainst(t, d, Options{ChooseTarget: true})
	if !s.CanValidate() {
		t.Fatal("CanValidate = false for a server advertising validate:1.1")
	}
	if err := s.Validate(context.Background()); err != nil {
		t.Errorf("Validate = %v, want nil", err)
	}
}

func TestGetConfigFrom_ReadsTheNamedSourceAndKeepsTheTarget(t *testing.T) {
	d := &fakeDevice{hello: helloOf([]string{CapabilityBase11, CapabilityCandidate}), respond: func(req string) string {
		return fmt.Sprintf(`<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="%s"><data/></rpc-reply>`, messageIDOf(req))
	}}
	s := openAgainst(t, d, Options{ChooseTarget: true})
	if _, err := s.GetConfigFrom(context.Background(), Running, datastore.Path{}); err != nil {
		t.Fatal(err)
	}
	if req := d.lastRequest(t); !strings.Contains(req, "<source><running/></source>") {
		t.Errorf("request = %s, want a read of running", req)
	}
	if s.Target() != Candidate {
		t.Errorf("Target() = %q after the read, want candidate still", s.Target())
	}
}

// containsString reports whether list holds s.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// findModule is the module named name in modules, nil when absent.
func findModule(modules []Module, name string) *Module {
	for i := range modules {
		if modules[i].Name == name {
			return &modules[i]
		}
	}
	return nil
}
