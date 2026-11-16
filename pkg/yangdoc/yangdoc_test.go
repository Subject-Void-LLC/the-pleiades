// Package yangdoc_test: the canonical form a change is judged by, the tree
// a document reads into, the one-line writer and the content checks, apart
// from any server.
package yangdoc_test

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
)

func TestCanonical_SetsAsideOrderAndWhiteSpaceAndKeepsTheRest(t *testing.T) {
	same := [][2]string{
		{`{"b": 1, "a": [1, 2]}`, `{"a":[1,2],"b":1}`},
		{`{"n": 1.50}`, `{"n":1.50}`},
	}
	for _, pair := range same {
		a, errA := yangdoc.Canonical([]byte(pair[0]), true)
		b, errB := yangdoc.Canonical([]byte(pair[1]), true)
		if errA != nil || errB != nil || a != b {
			t.Errorf("%s and %s read as different (%v, %v)", pair[0], pair[1], errA, errB)
		}
	}
	differs := [][2]string{
		{`{"a": [1, 2]}`, `{"a": [2, 1]}`},
		{`{"n": 1.5}`, `{"n": 1.50}`},
	}
	for _, pair := range differs {
		a, _ := yangdoc.Canonical([]byte(pair[0]), true)
		b, _ := yangdoc.Canonical([]byte(pair[1]), true)
		if a == b {
			t.Errorf("%s and %s read as the same", pair[0], pair[1])
		}
	}
	x1 := `<a xmlns="urn:x" xmlns:p="urn:p" y="2" x="1"><!-- note --><b> text </b>
	  <c/></a>`
	x2 := `<q:a xmlns:q="urn:x" x="1" y="2"><q:b>text</q:b><q:c></q:c></q:a>`
	a, errA := yangdoc.Canonical([]byte(x1), false)
	b, errB := yangdoc.Canonical([]byte(x2), false)
	if errA != nil || errB != nil || a != b {
		t.Errorf("XML differing in prefixes, attribute order, comments and white space read as different:\n%s\n%s", a, b)
	}
	if c, _ := yangdoc.Canonical([]byte(`<a xmlns="urn:other"><b>text</b><c/></a>`), false); c == a {
		t.Error("another namespace read as the same")
	}
	// Order is kept: an ordered-by user list's order is configuration.
	o1, _ := yangdoc.Canonical([]byte(`<a xmlns="urn:x"><b>1</b><b>2</b></a>`), false)
	o2, _ := yangdoc.Canonical([]byte(`<a xmlns="urn:x"><b>2</b><b>1</b></a>`), false)
	if o1 == o2 {
		t.Error("two orders of one list read as the same")
	}
	for _, bad := range []string{`{`, `{} {}`} {
		if _, err := yangdoc.Canonical([]byte(bad), true); err == nil {
			t.Errorf("Canonical(%q) accepted", bad)
		}
	}
	for _, bad := range []string{`<a>`, strings.Repeat("<a>", yangdoc.MaxDepth+1), `<a>x<b/></a>`, `<a><b/>x</a>`, `text`} {
		if _, err := yangdoc.Canonical([]byte(bad), false); err == nil {
			t.Errorf("Canonical(%.30q) accepted", bad)
		}
	}
}

// TestCanonical_AnIdentityrefIsReadByItsNamespace: two servers, or one server
// twice, may bind another prefix to the same namespace; the value is the same.
func TestCanonical_AnIdentityrefIsReadByItsNamespace(t *testing.T) {
	a, _ := yangdoc.Canonical([]byte(`<t xmlns="urn:if" xmlns:ianaift="urn:iana">ianaift:softwareLoopback</t>`), false)
	b, _ := yangdoc.Canonical([]byte(`<t xmlns="urn:if" xmlns:x="urn:iana">x:softwareLoopback</t>`), false)
	if a != b {
		t.Errorf("one identity under two prefixes read as different:\n%s\n%s", a, b)
	}
	c, _ := yangdoc.Canonical([]byte(`<t xmlns="urn:if" xmlns:x="urn:other">x:softwareLoopback</t>`), false)
	if c == a {
		t.Error("an identity in another namespace read as the same")
	}
	d, _ := yangdoc.Canonical([]byte(`<t xmlns="urn:if">10:30</t>`), false)
	if !strings.Contains(d, `"10:30"`) {
		t.Errorf("a value whose prefix nothing binds was rewritten: %s", d)
	}
}

func TestCheckDocument(t *testing.T) {
	for doc, ok := range map[string]bool{
		`<a/>`:                         true,
		`<?xml version="1.0"?><a/>`:    true,
		`<a xmlns="u"/><b xmlns="v"/>`: true,
		`text only`:                    false,
		`<!DOCTYPE a><a/>`:             false,
		strings.Repeat("<a>", 300):     false,
		`<a></b>`:                      false,
	} {
		if err := yangdoc.CheckDocument(doc, false); (err == nil) != ok {
			t.Errorf("CheckDocument(%.20q) = %v, want ok=%v", doc, err, ok)
		}
	}
	if err := yangdoc.CheckDocument(`{"a": 1}`, true); err != nil {
		t.Errorf("JSON refused: %v", err)
	}
	if err := yangdoc.CheckDocument(`{"a": `, true); err == nil {
		t.Error("JSON cut short accepted")
	}
}

func TestParse_KeepsLeavesChildrenAndScope(t *testing.T) {
	els, err := yangdoc.Parse([]byte(`<top xmlns="urn:t" xmlns:p="urn:p">
	  <list><k>1</k><v>  spaced  </v><sub><x p:a="1">p:y</x></sub></list>
	  <list><k>2</k></list>
	</top><other xmlns="urn:o"/>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(els) != 2 || els[1].Name != (xml.Name{Space: "urn:o", Local: "other"}) || !els[1].IsLeaf() {
		t.Fatalf("top-level elements %+v", els)
	}
	lists := els[0].Find(xml.Name{Space: "urn:t", Local: "list"})
	if len(lists) != 2 || len(yangdoc.FindIn(els, xml.Name{Space: "urn:t", Local: "top"})) != 1 {
		t.Fatalf("lists %d", len(lists))
	}
	lead := lists[0].LeadingLeaves()
	if len(lead) != 2 || lead[1].Text() != "  spaced  " {
		t.Fatalf("leading leaves %+v", lead)
	}
	if got := lists[1].LeadingLeaves(); len(got) != 1 {
		t.Fatalf("an entry of leaves only: leading leaves %d", len(got))
	}
	x := lists[0].Children[2].Children[0]
	if prefix, uri, ok := x.ValuePrefix(); !ok || prefix != "p" || uri != "urn:p" {
		t.Errorf("ValuePrefix() = %q %q %v", prefix, uri, ok)
	}
	if len(x.Attrs) != 1 || x.Attrs[0].Name.Space != "urn:p" {
		t.Errorf("attributes %+v", x.Attrs)
	}
	if lists[0].Text() != "" {
		t.Errorf("an element with children has text %q", lists[0].Text())
	}
	if _, ok := lists[0].Resolve("missing"); ok {
		t.Error("an undeclared prefix resolved")
	}
}

func TestWrite_OneLineWithItsNamespacesAndPrefixes(t *testing.T) {
	els, err := yangdoc.Parse([]byte(`<rpc-reply xmlns:if="urn:if" xmlns:ianaift="urn:iana"><data>
	  <if:interfaces><if:interface><if:name>lo&amp;1</if:name>
	  <if:type>ianaift:softwareLoopback</if:type><if:description>a
b	"c"</if:description><x xmlns="urn:aug"><y>10:30</y></x></if:interface></if:interfaces></data></rpc-reply>`))
	if err != nil {
		t.Fatal(err)
	}
	iface := els[0].Children[0].Children[0].Children[0]
	op := []xml.Attr{{Name: xml.Name{Space: yangdoc.NetconfBase, Local: "operation"}, Value: "replace"}}
	wrapped := yangdoc.New(xml.Name{Space: "urn:if", Local: "interfaces"}, nil, "", yangdoc.New(iface.Name, op, "", iface.Children...))
	got, err := yangdoc.Write([]*yangdoc.Element{wrapped})
	if err != nil {
		t.Fatal(err)
	}
	want := `<interfaces xmlns="urn:if"><interface xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" nc:operation="replace">` +
		`<name>lo&amp;1</name><type xmlns:ianaift="urn:iana">ianaift:softwareLoopback</type>` +
		`<description>a&#xA;b&#x9;&#34;c&#34;</description><x xmlns="urn:aug"><y>10:30</y></x></interface></interfaces>`
	if got != want {
		t.Errorf("Write() =\n%s\nwant\n%s", got, want)
	}
	// What it wrote reads back as the same configuration.
	back, err := yangdoc.Parse([]byte(got))
	if err != nil {
		t.Fatal(err)
	}
	if yangdoc.CanonicalElements(back[0].Children[0].Children) != yangdoc.CanonicalElements(iface.Children) {
		t.Error("the written element does not read back as the one read")
	}
	if _, err := yangdoc.Write([]*yangdoc.Element{yangdoc.New(xml.Name{Local: "a"}, []xml.Attr{{Name: xml.Name{Space: "urn:unknown", Local: "z"}, Value: "1"}}, "")}); err == nil {
		t.Error("an attribute in a namespace with no prefix was written")
	}
	plain, _ := yangdoc.Write([]*yangdoc.Element{yangdoc.New(xml.Name{Local: "a"}, []xml.Attr{{Name: xml.Name{Local: "z"}, Value: "1"}}, "v")})
	if plain != `<a z="1">v</a>` {
		t.Errorf("plain element = %s", plain)
	}
}
