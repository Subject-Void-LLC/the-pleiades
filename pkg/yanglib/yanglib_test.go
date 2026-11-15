// Package yanglib: tests of the library parser and the refusal rules against
// a real Catalyst 8000V's YANG library, in the XML a RESTCONF server answers
// with and in RFC 7951's JSON.
package yanglib

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The fixtures in testdata are the yang-library and modules-state elements of
// the rpc-replies pkg/netconf/testdata holds, captured on 2026-11-14 from a
// Cisco Catalyst 8000V running IOS XE 17.15.1a: the same data a RESTCONF
// server answers GET {+restconf}/data/ietf-yang-library:yang-library with in
// its XML encoding (RFC 8040 section 3.5, RFC 7950 section 7), the root
// element carrying its own namespace.

// fixture reads one gzipped document from testdata.
func fixture(t *testing.T, name string) []byte {
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
	return raw
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

func TestParseXML_ARealIOSXEYANGLibrary(t *testing.T) {
	lib, err := ParseXML(fixture(t, "c8k-17.15.1a-yang-library.xml.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if lib.Source != SourceLibrary11 || lib.ContentID != "fab9bf47e062dcbaeb6739b215fc4b27" {
		t.Errorf("Source = %q, ContentID = %q; want yang-library:1.1 and the capture's content-id", lib.Source, lib.ContentID)
	}
	kept, err := Keep(lib, Discovery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(kept.Modules) != 559 || kept.Dropped != 0 {
		t.Fatalf("modules = %d with %d dropped (%v), want all 559 kept", len(kept.Modules), kept.Dropped, kept.DroppedSamples)
	}
	native := findModule(kept.Modules, "Cisco-IOS-XE-native")
	if native == nil || native.Namespace != "http://cisco.com/ns/yang/Cisco-IOS-XE-native" || native.Revision != "2024-07-01" {
		t.Errorf("Cisco-IOS-XE-native = %+v, want its namespace and the 2024-07-01 revision", native)
	}
	if got := strings.Join(kept.Datastores, ","); got != "intended,operational,running" {
		t.Errorf("datastores = %q, want the NMDA set the library lists", got)
	}
	d := Discovery{}.WithLibrary(kept)
	if d.Completeness != Complete || d.Source != SourceLibrary11 || len(d.Modules) != 559 {
		t.Errorf("WithLibrary = %s from %s with %d modules, want complete from the library", d.Completeness, d.Source, len(d.Modules))
	}
}

// TestParseXML_ModulesStateLeavesImportOnlyModulesOut holds RFC 7895's form
// to RFC 8525's on the same device: modules-state lists import-only modules
// among its 613, and with those left out the implemented set is within the
// yang-library's own.
func TestParseXML_ModulesStateLeavesImportOnlyModulesOut(t *testing.T) {
	state, err := ParseXML(fixture(t, "c8k-17.15.1a-modules-state.xml.gz"))
	if err != nil {
		t.Fatal(err)
	}
	library, err := ParseXML(fixture(t, "c8k-17.15.1a-yang-library.xml.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if state.Source != SourceLibrary10 || state.ContentID == "" {
		t.Errorf("Source = %q, ContentID = %q; want yang-library:1.0 and its module-set-id", state.Source, state.ContentID)
	}
	fromState, err := Keep(state, Discovery{})
	if err != nil {
		t.Fatal(err)
	}
	fromLibrary, err := Keep(library, Discovery{})
	if err != nil {
		t.Fatal(err)
	}
	lib := map[string]bool{}
	for _, m := range fromLibrary.Modules {
		lib[m.Name+"@"+m.Revision] = true
	}
	for _, m := range fromState.Modules {
		if !lib[m.Name+"@"+m.Revision] {
			t.Errorf("modules-state implements %s@%s, which the yang-library does not list", m.Name, m.Revision)
		}
	}
	if len(fromState.Modules) == 0 || len(fromState.Modules) > len(fromLibrary.Modules) {
		t.Errorf("modules-state gave %d implemented modules, the yang-library %d", len(fromState.Modules), len(fromLibrary.Modules))
	}
}

func TestParseXML_RefusesWhatIsNotALibrary(t *testing.T) {
	cases := map[string]string{
		"another root":          `<data xmlns="urn:ietf:params:xml:ns:yang:ietf-yang-library"/>`,
		"another namespace":     `<yang-library xmlns="urn:example"/>`,
		"no namespace":          `<yang-library/>`,
		"no element":            `<?xml version="1.0"?>`,
		"not XML":               `{"ietf-yang-library:yang-library":{}}`,
		"broken yang-library":   `<yang-library xmlns="urn:ietf:params:xml:ns:yang:ietf-yang-library"><module-set>`,
		"broken modules-state":  `<modules-state xmlns="urn:ietf:params:xml:ns:yang:ietf-yang-library"><module>`,
		"an unbalanced closing": `<yang-library xmlns="urn:ietf:params:xml:ns:yang:ietf-yang-library"></x>`,
	}
	for name, doc := range cases {
		if _, err := ParseXML([]byte(doc)); err == nil {
			t.Errorf("%s: ParseXML accepted %q", name, doc)
		}
	}
}

// libraryAsJSON renders lib in RFC 7951's JSON encoding, the form a RESTCONF
// server answers application/yang-data+json with, so the JSON parser is held
// to the same real library the XML one reads.
func libraryAsJSON(t *testing.T, lib Library) []byte {
	t.Helper()
	type module struct {
		Name      string   `json:"name"`
		Revision  string   `json:"revision,omitempty"`
		Namespace string   `json:"namespace"`
		Feature   []string `json:"feature,omitempty"`
		Deviation []string `json:"deviation,omitempty"`
	}
	var modules []module
	for _, m := range lib.Modules {
		modules = append(modules, module{m.Name, m.Revision, m.Namespace, m.Features, m.Deviations})
	}
	var datastores []map[string]string
	for _, ds := range lib.Datastores {
		datastores = append(datastores, map[string]string{"name": "ietf-datastores:" + ds, "schema": "common"})
	}
	doc := map[string]any{"ietf-yang-library:yang-library": map[string]any{
		"module-set": []any{map[string]any{"name": "common", "module": modules}},
		"datastore":  datastores,
		"content-id": lib.ContentID,
	}}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseJSON_ReadsTheSameLibraryAsXML(t *testing.T) {
	fromXML, err := ParseXML(fixture(t, "c8k-17.15.1a-yang-library.xml.gz"))
	if err != nil {
		t.Fatal(err)
	}
	fromJSON, err := ParseJSON(libraryAsJSON(t, fromXML))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Keep(fromXML, Discovery{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Keep(fromJSON, Discovery{})
	if err != nil {
		t.Fatal(err)
	}
	da, db := Discovery{}.WithLibrary(a), Discovery{}.WithLibrary(b)
	if da.ModulesDigest() != db.ModulesDigest() || len(db.Modules) != 559 {
		t.Errorf("JSON read %d modules with digest %s, XML %d with %s; want the same set", len(db.Modules), db.ModulesDigest(), len(da.Modules), da.ModulesDigest())
	}
	if strings.Join(b.Datastores, ",") != "intended,operational,running" || b.ContentID != a.ContentID || b.Source != SourceLibrary11 {
		t.Errorf("JSON datastores %v, content-id %q, source %q; want the XML read's", b.Datastores, b.ContentID, b.Source)
	}
}

func TestParseJSON_ModulesStateLeavesImportOnlyOutAndReadsDeviations(t *testing.T) {
	raw := `{"ietf-yang-library:modules-state":{"module-set-id":"42","module":[` +
		`{"name":"ietf-interfaces","revision":"2018-02-20","namespace":"urn:ietf:params:xml:ns:yang:ietf-interfaces","feature":["if-mib"],"conformance-type":"implement","deviation":[{"name":"vendor-dev","revision":"2020-01-01"}]},` +
		`{"name":"ietf-yang-types","revision":"2013-07-15","namespace":"urn:ietf:params:xml:ns:yang:ietf-yang-types","conformance-type":"import"}]}}`
	lib, err := ParseJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if lib.Source != SourceLibrary10 || lib.ContentID != "42" || len(lib.Modules) != 1 {
		t.Fatalf("lib = %+v, want one implemented module from modules-state 42", lib)
	}
	m := lib.Modules[0]
	if m.Name != "ietf-interfaces" || len(m.Features) != 1 || len(m.Deviations) != 1 || m.Deviations[0] != "vendor-dev" {
		t.Errorf("module = %+v, want its feature and its deviating module", m)
	}
}

func TestParseJSON_RefusesWhatIsNotALibrary(t *testing.T) {
	for _, doc := range []string{`{}`, `{"ietf-yang-library:other":{}}`, `[`, `<yang-library/>`, `{"yang-library":{}}`} {
		if _, err := ParseJSON([]byte(doc)); err == nil {
			t.Errorf("ParseJSON accepted %q", doc)
		}
	}
}

func TestKeepModules_RefusesAMalformedEntryWhole(t *testing.T) {
	ok := Module{Name: "good", Namespace: "urn:good", Revision: "2024-01-01", Features: []string{"b", "a", "bad feature"}}
	var d Discovery
	kept := KeepModules([]Module{
		ok,
		ok, // a duplicate
		{Name: "bad\x1bname", Namespace: "urn:x"},
		{Name: "rev", Namespace: "urn:x", Revision: "2024-1-1"},
		{Name: "ns", Namespace: "urn:\xe2\x80\xaex"},
		{Name: "long", Namespace: "urn:" + strings.Repeat("x", MaxNamespaceBytes)},
		{Name: "", Namespace: "urn:x"},
		{Name: "alpha", Namespace: "urn:alpha"},
		{Name: "features", Namespace: "urn:f", Features: make([]string, MaxListEntries+1)},
		{Name: "no-namespace"},
	}, &d)
	if len(kept) != 2 || kept[0].Name != "alpha" || kept[1].Name != "good" {
		t.Fatalf("kept = %+v, want alpha and good, sorted", kept)
	}
	if got := strings.Join(kept[1].Features, ","); got != "a,b" {
		t.Errorf("features = %q, want the identifiers alone, sorted", got)
	}
	if d.Dropped != 7 {
		t.Errorf("Dropped = %d, want the seven malformed entries", d.Dropped)
	}
	if len(d.DroppedSamples) != maxDroppedSamples {
		t.Errorf("samples = %d, want the first %d", len(d.DroppedSamples), maxDroppedSamples)
	}
	for _, s := range d.DroppedSamples {
		if strings.ContainsAny(s, "\x1b\xe2\x80\xae") {
			t.Errorf("sample %s carries a control character; samples are quoted", s)
		}
	}
}

func TestKeepModules_PastTheBoundEachFurtherEntryIsDropped(t *testing.T) {
	modules := make([]Module, MaxModules+2)
	for i := range modules {
		modules[i] = Module{Name: "m" + strings.Repeat("x", i%7) + strconv.Itoa(i), Namespace: "urn:m"}
	}
	var d Discovery
	kept := KeepModules(modules, &d)
	if len(kept) != MaxModules || d.Dropped != 2 {
		t.Errorf("kept %d with %d dropped, want %d and 2", len(kept), d.Dropped, MaxModules)
	}
}

func TestDrop_CutsALongSampleAndQuotesIt(t *testing.T) {
	var d Discovery
	d.Drop(strings.Repeat("a", 200) + "\x00")
	if d.Dropped != 1 || len(d.DroppedSamples) != 1 || len(d.DroppedSamples[0]) != 122 {
		t.Errorf("Drop kept %v, want one quoted sample of 120 bytes", d.DroppedSamples)
	}
}

func TestKeep_AnEmptyLibraryIsRefusedAndCountsAddToTheBase(t *testing.T) {
	if _, err := Keep(Library{Source: SourceLibrary11, Modules: []Module{{Name: "bad name"}}}, Discovery{}); !errors.Is(err, ErrEmptyLibrary) {
		t.Errorf("Keep of a library with no usable module = %v, want ErrEmptyLibrary", err)
	}
	base := Discovery{}
	base.Drop("a hello entry")
	k, err := Keep(Library{Modules: []Module{{Name: "a", Namespace: "urn:a"}, {Name: "b b", Namespace: "urn:b"}}, ContentID: "not one token", Datastores: []string{"running", "bad name", "running"}}, base)
	if err != nil {
		t.Fatal(err)
	}
	if k.Dropped != 2 || len(k.DroppedSamples) != 2 {
		t.Errorf("Dropped = %d (%v), want the base's one and the library's one", k.Dropped, k.DroppedSamples)
	}
	if k.ContentID != "" || strings.Join(k.Datastores, ",") != "running" {
		t.Errorf("ContentID = %q, Datastores = %v; want an unusable content-id left out and the datastores held to identifiers", k.ContentID, k.Datastores)
	}
	if d := (Discovery{}).WithLibrary(k); d.Completeness != Partial {
		t.Errorf("Completeness = %s after a dropped entry, want partial", d.Completeness)
	}
}

func TestValidID(t *testing.T) {
	cases := map[string]string{
		"abc":                    "abc",
		"":                       "",
		"a b":                    "",
		"a\tb":                   "",
		"a\x00":                  "",
		strings.Repeat("x", 257): "",
	}
	for in, want := range cases {
		if got := ValidID(in); got != want {
			t.Errorf("ValidID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeepCapabilities_RefusesWholeAndKeepsOnce(t *testing.T) {
	var d Discovery
	got := KeepCapabilities([]string{
		" urn:ietf:params:restconf:capability:defaults:1.0?basic-mode=explicit ",
		"urn:ietf:params:restconf:capability:depth:1.0",
		"urn:ietf:params:restconf:capability:depth:1.0",
		"",
		"urn:bad\x1b",
		"urn:" + strings.Repeat("x", MaxNamespaceBytes),
	}, &d)
	want := "urn:ietf:params:restconf:capability:defaults:1.0?basic-mode=explicit,urn:ietf:params:restconf:capability:depth:1.0"
	if strings.Join(got, ",") != want || d.Dropped != 2 {
		t.Errorf("KeepCapabilities = %v with %d dropped, want %s and 2", got, d.Dropped, want)
	}
}

func TestDiscovery_ServesAndTheDigest(t *testing.T) {
	a := Discovery{Completeness: Complete, Modules: []Module{{Name: "a", Namespace: "urn:a"}}}
	if served, known := a.Serves("urn:a"); !served || !known {
		t.Errorf("Serves(urn:a) = %v, %v; want served and known", served, known)
	}
	if served, known := a.Serves("urn:b"); served || !known {
		t.Errorf("Serves(urn:b) = %v, %v; want not served, known", served, known)
	}
	a.Completeness = Partial
	if _, known := a.Serves("urn:a"); known {
		t.Error("Serves answered on a partial list")
	}
	b := Discovery{Modules: []Module{{Name: "a", Namespace: "urn:a"}}}
	if a.ModulesDigest() == "" || a.ModulesDigest() != b.ModulesDigest() {
		t.Error("one module set gave two digests")
	}
	b.Modules[0].Revision = "2020-01-01"
	if a.ModulesDigest() == b.ModulesDigest() {
		t.Error("a changed revision kept the digest")
	}
}

func TestIsIdentifier(t *testing.T) {
	for in, want := range map[string]bool{"ietf-interfaces": true, "_x.y-z": true, "1abc": false, "a b": false, "": false, strings.Repeat("a", 256): false} {
		if got := IsIdentifier(in); got != want {
			t.Errorf("IsIdentifier(%q) = %v, want %v", in, got, want)
		}
	}
}
