// Package netconf: tests of get-schema over a real codec and a real pipe,
// against a scripted server whose hello and replies have RFC 6022's shapes.
package netconf

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// monitoringHello is a YANG 1.0 server's hello advertising
// ietf-netconf-monitoring as a module capability, as RFC 6022 has a server
// offering get-schema do.
const monitoringHello = `<?xml version="1.0" encoding="UTF-8"?>
<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><capabilities>
<capability>urn:ietf:params:netconf:base:1.1</capability>
<capability>urn:ietf:params:xml:ns:yang:ietf-netconf-monitoring?module=ietf-netconf-monitoring&amp;revision=2010-10-04</capability>
</capabilities><session-id>7</session-id></hello>`

// plainHello advertises no ietf-netconf-monitoring.
const plainHello = `<?xml version="1.0" encoding="UTF-8"?>
<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><capabilities>
<capability>urn:ietf:params:netconf:base:1.1</capability>
</capabilities><session-id>8</session-id></hello>`

// schemaDevice answers every get-schema with data holding body.
func schemaDevice(hello, data string) *fakeDevice {
	return &fakeDevice{hello: hello, respond: func(req string) string {
		return `<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="` + messageIDOf(req) + `">` + data + `</rpc-reply>`
	}}
}

// TestGetSchema_ReturnsTheSourceAsTheServerSentIt asks for a module and reads
// back its text with the XML's escaping read, in either form a server writes
// it, and nothing else changed: words a configuration masker would take for
// secrets stay as they are.
func TestGetSchema_ReturnsTheSourceAsTheServerSentIt(t *testing.T) {
	const source = "module m {\n  leaf password { type string; description \"a <secret> & key 100 at 10.1.2.3\"; }\n}\n"
	escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(source)
	for name, data := range map[string]string{
		"escaped": `<data xmlns="` + MonitoringNamespace + `">` + escaped + `</data>`,
		"cdata":   `<data xmlns="` + MonitoringNamespace + `"><![CDATA[` + source + `]]></data>`,
		"base ns": `<data>` + escaped + `</data>`,
	} {
		d := schemaDevice(monitoringHello, data)
		s := openAgainst(t, d, Options{})
		got, err := s.GetSchema(context.Background(), "m", "2024-01-01", 1<<20)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != source {
			t.Errorf("%s: GetSchema = %q, want the source byte for byte", name, got)
		}
		req := d.lastRequest(t)
		for _, want := range []string{`<get-schema xmlns="` + MonitoringNamespace + `">`, `<identifier>m</identifier>`, `<version>2024-01-01</version>`, `<format>yang</format>`} {
			if !strings.Contains(req, want) {
				t.Errorf("%s: request %q lacks %q", name, req, want)
			}
		}
	}
}

// TestGetSchema_AsksForTheOneVersionWhenNoneIsNamed sends no version element
// for a module the library names no revision of.
func TestGetSchema_AsksForTheOneVersionWhenNoneIsNamed(t *testing.T) {
	d := schemaDevice(monitoringHello, `<data xmlns="`+MonitoringNamespace+`">module m { }</data>`)
	s := openAgainst(t, d, Options{})
	if _, err := s.GetSchema(context.Background(), "m", "", 1<<20); err != nil {
		t.Fatal(err)
	}
	if req := d.lastRequest(t); strings.Contains(req, "<version>") {
		t.Errorf("request %q names a version, want none", req)
	}
}

// TestGetSchema_RefusesBeforeSending holds every refusal made before a byte
// is written: a server that offers no get-schema, a name that is not an
// identifier, a version that is not a date.
func TestGetSchema_RefusesBeforeSending(t *testing.T) {
	d := schemaDevice(plainHello, `<data>x</data>`)
	s := openAgainst(t, d, Options{})
	if _, err := s.GetSchema(context.Background(), "m", "", 1<<20); !errors.Is(err, ErrNoGetSchema) {
		t.Errorf("a server without ietf-netconf-monitoring: %v, want ErrNoGetSchema", err)
	}
	m := openAgainst(t, schemaDevice(monitoringHello, `<data>x</data>`), Options{})
	for _, c := range [][2]string{{"m</identifier><kill-session/>", ""}, {"../m", ""}, {"m", "2024-01-01</version>"}, {"m", "latest"}} {
		if _, err := m.GetSchema(context.Background(), c[0], c[1], 1<<20); err == nil {
			t.Errorf("GetSchema(%q, %q) succeeded, want a refusal", c[0], c[1])
		}
	}
	d.mu.Lock()
	sent := len(d.requests)
	d.mu.Unlock()
	if sent != 0 {
		t.Errorf("%d requests reached the server without get-schema, want none", sent)
	}
}

// TestGetSchema_RefusesAnAnswerThatIsNotTheSource holds each answer that
// carries no source text to an error rather than an empty or partial one.
func TestGetSchema_RefusesAnAnswerThatIsNotTheSource(t *testing.T) {
	for name, data := range map[string]string{
		"an element":  `<data xmlns="` + MonitoringNamespace + `"><module>m</module></data>`,
		"no data":     `<ok/>`,
		"too large":   `<data xmlns="` + MonitoringNamespace + `">` + strings.Repeat("a", 65) + `</data>`,
		"bad entity":  `<data xmlns="` + MonitoringNamespace + `">&nope;</data>`,
		"data nested": `<other><data>module m { }</data></other>`,
	} {
		s := openAgainst(t, schemaDevice(monitoringHello, data), Options{})
		if got, err := s.GetSchema(context.Background(), "m", "", 64); err == nil {
			t.Errorf("%s: GetSchema = %q, want a refusal", name, got)
		}
	}
	refusal := `<rpc-error><error-type>application</error-type><error-tag>invalid-value</error-tag><error-severity>error</error-severity><error-message>no such schema</error-message></rpc-error>`
	s := openAgainst(t, schemaDevice(monitoringHello, refusal), Options{})
	var rejected RPCErrors
	if _, err := s.GetSchema(context.Background(), "m", "", 64); !errors.As(err, &rejected) || rejected[0].Tag != "invalid-value" {
		t.Errorf("a refusal: %v, want the server's rpc-error", err)
	}
}

// TestCanGetSchema_ReadsTheLibraryOfAYANG11Server holds a server that lists
// ietf-netconf-monitoring only in its library, as a YANG 1.1 server may, to
// offering get-schema.
func TestCanGetSchema_ReadsTheLibraryOfAYANG11Server(t *testing.T) {
	s := openAgainst(t, schemaDevice(plainHello, `<data>x</data>`), Options{})
	if s.CanGetSchema() {
		t.Fatal("CanGetSchema = true for a server that names no monitoring module")
	}
	s.discovery.Modules = append(s.discovery.Modules, yanglib.Module{Name: "ietf-netconf-monitoring", Revision: "2010-10-04", Namespace: MonitoringNamespace})
	if !s.CanGetSchema() {
		t.Error("CanGetSchema = false for a server whose library lists ietf-netconf-monitoring")
	}
}
