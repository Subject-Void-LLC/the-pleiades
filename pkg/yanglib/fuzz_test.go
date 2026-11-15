// Package yanglib: fuzz tests of the library parsers, which read what a
// device sends: neither may panic, and what Keep keeps from any answer holds
// to the refusal rules.
package yanglib

import (
	"strings"
	"testing"
)

// checkKept fails t when a kept module breaks a rule KeepModules enforces.
func checkKept(t *testing.T, lib Library) {
	t.Helper()
	k, err := Keep(lib, Discovery{})
	if err != nil {
		return
	}
	for _, m := range k.Modules {
		if !IsIdentifier(m.Name) || m.Namespace == "" || !Printable(m.Namespace) || len(m.Namespace) > MaxNamespaceBytes {
			t.Fatalf("kept a module the rules refuse: %+v", m)
		}
		for _, f := range append(append([]string(nil), m.Features...), m.Deviations...) {
			if !IsIdentifier(f) {
				t.Fatalf("kept a feature or deviation that is not an identifier: %q", f)
			}
		}
	}
	for _, ds := range k.Datastores {
		if !IsIdentifier(ds) {
			t.Fatalf("kept a datastore that is not an identifier: %q", ds)
		}
	}
	if k.ContentID != "" && (strings.ContainsAny(k.ContentID, " \t") || !Printable(k.ContentID)) {
		t.Fatalf("kept a content-id that is not a token: %q", k.ContentID)
	}
}

func FuzzParseXML(f *testing.F) {
	f.Add(`<yang-library xmlns="urn:ietf:params:xml:ns:yang:ietf-yang-library"><module-set><module><name>a</name><namespace>urn:a</namespace><feature>x</feature></module></module-set><datastore><name>ds:running</name></datastore><content-id>1</content-id></yang-library>`)
	f.Add(`<modules-state xmlns="urn:ietf:params:xml:ns:yang:ietf-yang-library"><module-set-id>2</module-set-id><module><name>b</name><namespace>urn:b</namespace><conformance-type>import</conformance-type></module></modules-state>`)
	f.Add(`<yang-library xmlns="urn:ietf:params:xml:ns:yang:ietf-yang-library"><module-set><module><name>a&#x1b;</name><namespace>urn:&#x202e;</namespace></module></module-set></yang-library>`)
	f.Add(`<x/>`)
	f.Fuzz(func(t *testing.T, doc string) {
		lib, err := ParseXML([]byte(doc))
		if err != nil {
			return
		}
		checkKept(t, lib)
	})
}

func FuzzParseJSON(f *testing.F) {
	f.Add(`{"ietf-yang-library:yang-library":{"module-set":[{"module":[{"name":"a","namespace":"urn:a","feature":["x"],"deviation":["d"]}]}],"datastore":[{"name":"ietf-datastores:running"}],"content-id":"1"}}`)
	f.Add(`{"ietf-yang-library:modules-state":{"module-set-id":"2","module":[{"name":"b","namespace":"urn:b","conformance-type":"implement","deviation":[{"name":"d"}]}]}}`)
	f.Add(`{"ietf-yang-library:yang-library":{"module-set":[{"module":[{"name":"a\u001b","namespace":"urn:\u202e"}]}]}}`)
	f.Add(`[]`)
	f.Fuzz(func(t *testing.T, doc string) {
		lib, err := ParseJSON([]byte(doc))
		if err != nil {
			return
		}
		checkKept(t, lib)
	})
}
