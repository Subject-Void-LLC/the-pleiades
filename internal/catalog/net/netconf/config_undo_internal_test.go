// Package netconf: tests of the undo planner, apart from any session: what
// each shape of edit plans against what was read before it, and what is
// left to the read after.
package netconf

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
)

// nacmNS is ietf-netconf-acm's namespace, which these documents use.
const nacmNS = "urn:ietf:params:xml:ns:yang:ietf-netconf-acm"

// parse parses doc or fails t.
func parse(t *testing.T, doc string) []*yangdoc.Element {
	t.Helper()
	els, err := yangdoc.Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parsing %s: %v", doc, err)
	}
	return els
}

// groups is a nacm document holding the given group elements.
func groups(inner string) string {
	return `<nacm xmlns="` + nacmNS + `"><groups>` + inner + `</groups></nacm>`
}

// planned plans content against before and writes the undo.
func planned(t *testing.T, content, before, defaultOp string) (string, *plan) {
	t.Helper()
	p := planUndo(parse(t, content), parse(t, before), defaultOp)
	if p.why != "" {
		return "", p
	}
	doc, err := yangdoc.Write(p.top)
	if err != nil {
		t.Fatal(err)
	}
	return doc, p
}

func TestPlanUndo_WhatEachEditPlans(t *testing.T) {
	const nc = `xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0"`
	for name, tc := range map[string]struct {
		content, before, defaultOp string
		want                       string
		deferred                   bool
		why                        string
	}{
		"a group created where none was": {
			content: groups(`<group><name>ops</name><user-name>alice</user-name></group>`),
			before:  ``,
			want:    groups(`<group ` + nc + ` nc:operation="remove"><name>ops</name><user-name>alice</user-name></group>`),
		},
		"a group created beside two, its first key new": {
			content: groups(`<group><name>ops</name></group>`),
			before:  groups(`<group><name>a</name></group><group><name>b</name></group>`),
			want:    groups(`<group ` + nc + ` nc:operation="remove"><name>ops</name></group>`),
		},
		"a group beside one is left to the read after": {
			content:  groups(`<group><name>ops</name></group>`),
			before:   groups(`<group><name>a</name></group>`),
			deferred: true,
		},
		"a re-run of what is there plans nothing": {
			content: groups(`<group><name>ops</name><user-name>alice</user-name></group>`),
			before:  groups(`<group><name>a</name></group><group><name>ops</name><user-name>alice</user-name></group>`),
			want:    ``,
		},
		"a user added to a group of two users is removed": {
			content: groups(`<group><name>ops</name><user-name>carol</user-name></group>`),
			before:  groups(`<group><name>a</name></group><group><name>ops</name><user-name>alice</user-name><user-name>bob</user-name></group>`),
			want:    groups(`<group><name>ops</name><user-name ` + nc + ` nc:operation="remove">carol</user-name></group>`),
		},
		"a user added to a group of one user is left to the read after": {
			content:  groups(`<group><name>ops</name><user-name>carol</user-name></group>`),
			before:   groups(`<group><name>a</name></group><group><name>ops</name><user-name>alice</user-name></group>`),
			deferred: true,
		},
		"a container's changed value would be a put-back": {
			content: `<sys xmlns="urn:s"><contact>new</contact></sys>`,
			before:  `<sys xmlns="urn:s"><location>here</location><contact>old</contact></sys>`,
			why:     "put back what was read from the device",
		},
		"a leaf new to a container is removed inside it": {
			content: `<sys xmlns="urn:s"><contact>new</contact></sys>`,
			before:  `<sys xmlns="urn:s"><location>here</location></sys>`,
			want:    `<sys xmlns="urn:s"><contact ` + nc + ` nc:operation="remove">new</contact></sys>`,
		},
		"a replace of what is there would be a put-back": {
			content: groups(`<group ` + nc + ` nc:operation="replace"><name>ops</name></group>`),
			before:  groups(`<group><name>a</name></group><group><name>ops</name><user-name>alice</user-name></group>`),
			why:     "put back what was read from the device",
		},
		"a delete of what is not there plans nothing": {
			content: groups(`<group ` + nc + ` nc:operation="delete"><name>x</name></group>`),
			before:  groups(`<group><name>a</name></group><group><name>b</name></group>`),
			want:    ``,
		},
		"a create of what is there plans nothing": {
			content: groups(`<group ` + nc + ` nc:operation="create"><name>a</name></group>`),
			before:  groups(`<group><name>a</name></group><group><name>b</name></group>`),
			want:    ``,
		},
		"none changes nothing for a leaf": {
			content:   `<sys xmlns="urn:s"><contact>new</contact></sys>`,
			before:    `<sys xmlns="urn:s"><contact>old</contact></sys>`,
			defaultOp: netconf.DefaultOperationNone,
			want:      ``,
		},
		"a nested change under a named entry would be a put-back": {
			content: `<top xmlns="urn:t"><item><id>1</id><sub><k>x</k></sub></item></top>`,
			before:  `<top xmlns="urn:t"><item><id>1</id><sub><k>y</k></sub></item><item><id>2</id></item></top>`,
			why:     "put back what was read from the device",
		},
		"an entry created under a named one is named by the author's own key": {
			content: `<top xmlns="urn:t"><item><id>1</id><new><k>x</k></new></item></top>`,
			before:  `<top xmlns="urn:t"><item><id>1</id><descr>device-text</descr></item><item><id>2</id></item></top>`,
			want:    `<top xmlns="urn:t"><item><id>1</id><new ` + nc + ` nc:operation="remove"><k>x</k></new></item></top>`,
		},
		"a composite-key group with one entry it cannot name is left whole to the read after": {
			content:  `<p xmlns="urn:p"><pair><a>b</a><b>x</b></pair><pair><a>a</a><b>z</b></pair></p>`,
			before:   `<p xmlns="urn:p"><pair><a>a</a><b>x</b></pair><pair><a>a</a><b>y</b></pair></p>`,
			deferred: true,
		},
		"default_operation replace": {
			content: `<sys xmlns="urn:s"><contact>new</contact></sys>`, before: ``, defaultOp: netconf.DefaultOperationReplace,
			why: "replaces the whole datastore",
		},
		"two instances of a container": {
			content: groups(`<group><name>ops</name></group>`),
			before:  `<nacm xmlns="` + nacmNS + `"><groups/></nacm><nacm xmlns="` + nacmNS + `"><groups/></nacm>`,
			why:     "instances of nacm",
		},
		"an entry whose first leaf changes is left to the read after": {
			content:  `<a xmlns="urn:a"><b><c>1</c></b></a>`,
			before:   `<a xmlns="urn:a"><b><c>2</c></b></a>`,
			deferred: true,
		},
		"a value at the top": {
			content: `<c xmlns="urn:a">1</c>`,
			before:  `<c xmlns="urn:a">2</c>`,
			why:     "a value directly under containers",
		},
		"an insert attribute": {
			content:  `<a xmlns="urn:a" xmlns:yang="urn:ietf:params:xml:ns:yang:1"><b><e yang:insert="first"><k>1</k></e></b></a>`,
			before:   `<a xmlns="urn:a"><b><e><k>2</k></e></b></a>`,
			deferred: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, p := planned(t, tc.content, tc.before, tc.defaultOp)
			switch {
			case tc.why != "":
				if !strings.Contains(p.why, tc.why) {
					t.Fatalf("why = %q, want %q", p.why, tc.why)
				}
				return
			case p.why != "":
				t.Fatalf("no undo: %s", p.why)
			case (len(p.deferred) > 0) != tc.deferred:
				t.Fatalf("deferred = %v, want %v", p.deferred, tc.deferred)
			}
			if !tc.deferred && got != tc.want {
				t.Errorf("undo =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

func TestDecideGroup_FromTheInstancesBeforeAndAfter(t *testing.T) {
	const nc = `xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0"`
	path := []*yangdoc.Element{parse(t, `<nacm xmlns="`+nacmNS+`"/>`)[0], parse(t, `<groups xmlns="`+nacmNS+`"/>`)[0]}
	name := parse(t, `<group xmlns="`+nacmNS+`"/>`)[0].Name
	content := func(inner string) []*yangdoc.Element {
		return parse(t, groups(inner))[0].Children[0].Children
	}
	for name2, tc := range map[string]struct {
		before, after, content, want, why string
	}{
		"unchanged":   {groups(`<group><name>a</name></group>`), groups(`<group><name>a</name></group>`), `<group><name>a</name></group>`, ``, ``},
		"one created": {groups(`<group><name>a</name></group>`), groups(`<group><name>a</name></group><group><name>b</name><user-name>u</user-name></group>`), `<group><name>b</name><user-name>u</user-name></group>`, groups(`<group ` + nc + ` nc:operation="remove"><name>b</name><user-name>u</user-name></group>`), ``},
		"one changed": {groups(`<group><name>a</name><user-name>u</user-name></group>`), groups(`<group><name>a</name><user-name>v</user-name></group>`), `<group><name>a</name><user-name>v</user-name></group>`, ``, "put back what was read"},
		"one gone":    {groups(`<group><name>a</name></group><group><name>b</name></group>`), groups(`<group><name>a</name></group>`), `<group><name>b</name></group>`, ``, "put back what was read"},
		"another client's entry beside this task's": {groups(`<group><name>a</name></group>`), groups(`<group><name>a</name></group><group><name>b</name></group><group><name>c</name></group>`), `<group><name>b</name></group>`, ``, "2 instances are new and the content names 1"},
		"a new entry the content does not name":     {groups(`<group><name>a</name></group>`), groups(`<group><name>a</name></group><group><name>c</name></group>`), `<group><name>b</name></group>`, ``, "matches no element of the content"},
	} {
		t.Run(name2, func(t *testing.T) {
			p := &plan{deferred: []group{{path: path, name: name, content: content(tc.content)}}}
			p.resolve(parse(t, tc.before), parse(t, tc.after))
			if tc.why != "" {
				if !strings.Contains(p.why, tc.why) {
					t.Fatalf("why = %q, want %q", p.why, tc.why)
				}
				return
			}
			if p.why != "" {
				t.Fatal(p.why)
			}
			got, _ := yangdoc.Write(p.top)
			if got != tc.want {
				t.Errorf("undo =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
	if got := instancesAt(parse(t, `<nacm xmlns="`+nacmNS+`"/><nacm xmlns="`+nacmNS+`"/>`), group{path: path, name: name}); got != nil {
		t.Errorf("instances under a doubled container = %v", got)
	}
}

func TestUndoContent_HoldsAnUndoToWhatTheJournalKeeps(t *testing.T) {
	undo := func(doc string) *plan { return &plan{top: parse(t, doc)} }
	if doc, why := undoContent(&plan{}, nil, true); doc != "" || why != "" {
		t.Errorf("an empty plan = %q, %q", doc, why)
	}
	ok := `<sys xmlns="urn:s"><contact>x</contact></sys>`
	if doc, why := undoContent(undo(ok), nil, true); doc != ok || why != "" {
		t.Errorf("a short undo = %q, %q", doc, why)
	}
	for name, tc := range map[string]struct {
		doc      string
		creds    []string
		explicit bool
		why      string
	}{
		"too long":                     {`<sys xmlns="urn:s"><contact>` + strings.Repeat("x", 300) + `</contact></sys>`, nil, true, "bytes"},
		"a secret leaf":                {`<sys xmlns="urn:s"><password>hunter2hunter2</password></sys>`, nil, true, "masking hides"},
		"the credential":               {`<sys xmlns="urn:s"><contact>s3cret-value</contact></sys>`, []string{"s3cret-value"}, true, "masking hides"},
		"a template":                   {`<sys xmlns="urn:s"><contact>{{ x }}</contact></sys>`, nil, true, "secret reference"},
		"a placeholder":                {`<sys xmlns="urn:s"><contact>pleiades-masked-0123456789abcdef</contact></sys>`, nil, true, "placeholder"},
		"a formatting char":            {"<sys xmlns=\"urn:s\"><contact>a\u202eb</contact></sys>", nil, true, "control or formatting"},
		"reads not in explicit mode":   {`<sys xmlns="urn:s"><contact>x</contact></sys>`, nil, false, "with-defaults explicit"},
		"an attribute it cannot write": {`<sys xmlns="urn:s" xmlns:o="urn:o" o:a="1"><contact>x</contact></sys>`, nil, true, "cannot be written"},
	} {
		t.Run(name, func(t *testing.T) {
			if doc, why := undoContent(undo(tc.doc), tc.creds, tc.explicit); doc != "" || !strings.Contains(why, tc.why) {
				t.Errorf("undoContent() = %q, %q, want %q", doc, why, tc.why)
			}
		})
	}
}

func TestPlace_WritesEachContainerOnce(t *testing.T) {
	a := parse(t, `<a xmlns="urn:a"/>`)[0]
	b := parse(t, `<b xmlns="urn:a"/>`)[0]
	top := place(nil, []*yangdoc.Element{a, b}, parse(t, `<x xmlns="urn:a">1</x>`)...)
	top = place(top, []*yangdoc.Element{a, b}, parse(t, `<y xmlns="urn:a">2</y>`)...)
	top = place(top, nil, parse(t, `<z xmlns="urn:z"/>`)...)
	got, _ := yangdoc.Write(top)
	if want := `<a xmlns="urn:a"><b><x>1</x><y>2</y></b></a><z xmlns="urn:z"></z>`; got != want {
		t.Errorf("place() = %s, want %s", got, want)
	}
}

func TestConfigUndoCall_FromTheCallAlone(t *testing.T) {
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"plain":            {map[string]any{paramContent: `<a/>`}, ""},
		"a secret":         {map[string]any{paramContent: `<a><k>{{ secret.ike.psk }}</k></a>`}, "names a secret"},
		"replace":          {map[string]any{paramContent: `<a/>`, paramDefaultOperation: "replace"}, "whole datastore"},
		"merge":            {map[string]any{paramContent: `<a/>`, paramDefaultOperation: "merge"}, ""},
		"content not text": {map[string]any{paramContent: 7}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := configUndoCall(tc.params); (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
				t.Errorf("configUndoCall() = %q, want %q", got, tc.want)
			}
		})
	}
}
