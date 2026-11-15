// Package restconf: tests of reading a written path, the writes, and the
// Location a created resource is named by.
package restconf

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
)

func TestParsePath_ReadsRFC8040Paths(t *testing.T) {
	cases := map[string]Path{
		"ietf-interfaces:interfaces":                                     {Node("ietf-interfaces", "interfaces")},
		"/ietf-interfaces:interfaces/interface=GigabitEthernet0%2F0%2F1": {Node("ietf-interfaces", "interfaces"), Node("", "interface").Entry("GigabitEthernet0/0/1")},
		"m:list=a%2Cb,,c":   {Node("m", "list").Entry("a,b", "", "c")},
		"m:x/other:y=k:1@h": {Node("m", "x"), Node("other", "y").Entry("k:1@h")},
		"m:l=caf%C3%A9%20x": {Node("m", "l").Entry("caf" + string(rune(0xe9)) + " x")},
	}
	for in, want := range cases {
		got, err := ParsePath(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("ParsePath(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
}

func TestParsePath_RefusesWhatIsNotAPath(t *testing.T) {
	for _, in := range []string{
		"", "/", "m:a?x=1", "m:a#f", "m:a//b", "m:a/", "a", "m:a/../b", "m:a/./b",
		"m:l=a b", "m:l=a%zz", "m:l=a\nb", "m:l=%0A", "1m:a", "m:l=a\"b",
		strings.Repeat("m:a/", maxPathTextBytes),
	} {
		if p, err := ParsePath(in); err == nil {
			t.Errorf("ParsePath(%.40q) = %+v, want a refusal", in, p)
		}
	}
}

// FuzzParsePath: a path that parses rebuilds into text that parses back to
// the same nodes and keys, so a key is decoded exactly once.
func FuzzParsePath(f *testing.F) {
	for _, seed := range []string{"m:a", "m:a/b=k", "m:l=a%2Cb,c", "m:l=%2F%2E%2E", "m:a/x:b=1,2/c", "m:l=%25"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		p, err := ParsePath(in)
		if err != nil {
			return
		}
		escaped, err := p.Escaped()
		if err != nil {
			t.Fatalf("a parsed path does not rebuild: %v", err)
		}
		again, err := ParsePath(escaped)
		if err != nil || !reflect.DeepEqual(again, p) {
			t.Fatalf("%q rebuilt as %q, which reads back as %+v (%v), not %+v", in, escaped, again, err, p)
		}
	})
}

func TestResourceRef_DataAndDatastores(t *testing.T) {
	c := &Client{root: "/restconf", rootFound: true}
	p := Path{Node("m", "a")}
	if ref, err := c.resourceRef("", p); err != nil || ref != "/restconf/data/m:a" {
		t.Errorf("data ref = %q, %v", ref, err)
	}
	if ref, err := c.resourceRef("running", p); err != nil || ref != "/restconf/ds/ietf-datastores:running/m:a" {
		t.Errorf("datastore ref = %q, %v", ref, err)
	}
	if ref, err := c.resourceRef("running", nil); err != nil || ref != "/restconf/ds/ietf-datastores:running" {
		t.Errorf("datastore root = %q, %v", ref, err)
	}
	if _, err := c.resourceRef("bad name", p); err == nil {
		t.Error("a datastore that is no identifier was accepted")
	}
	if _, err := (&Client{}).resourceRef("running", p); err == nil {
		t.Error("a datastore ref before the root was found")
	}
	if _, err := c.resourceRef("running", Path{Node("", "a")}); err == nil {
		t.Error("an unqualified first node was accepted under a datastore")
	}
}

func TestLocationPath_OnlyAResourceUnderTheDatastoreWritten(t *testing.T) {
	base, _ := url.Parse("https://192.0.2.1")
	c := &Client{base: base, root: "/restconf", rootFound: true}
	cases := map[string]bool{
		"https://192.0.2.1/restconf/data/m:a/b=k%2F1":     true,
		"/restconf/data/m:a/b=k":                          true,
		"https://elsewhere.example/restconf/data/m:a/b=k": false,
		"/restconf/ds/ietf-datastores:running/m:a":        false,
		"/other/data/m:a":                                 false,
		"/restconf/data/m:a?x=1":                          false,
		"/restconf/data/":                                 false,
		"%zz":                                             false,
	}
	for loc, ok := range cases {
		if got := c.locationPath(loc, ""); (got != nil) != ok {
			t.Errorf("locationPath(%q) = %+v, want found=%v", loc, got, ok)
		}
	}
}

func TestWrite_RefusesWhatItCannotSend(t *testing.T) {
	c := refusingClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }, httpapi.AuthNone)
	ctx := context.Background()
	if _, err := c.Write(ctx, "MERGE", "", Path{Node("m", "a")}, MediaTypeJSON, nil); err == nil {
		t.Error("an unknown method was sent")
	}
	if _, err := c.Write(ctx, MethodPut, "", Path{Node("m", "a")}, MediaTypeJSON, make([]byte, MaxWriteBytes+1)); err == nil {
		t.Error("a document past the bound was sent")
	}
	if _, err := c.Write(ctx, MethodPut, "", Path{Node("", "a")}, MediaTypeJSON, []byte("{}")); err == nil {
		t.Error("an unqualified path was sent")
	}
	if w, err := c.Write(ctx, MethodDelete, "", Path{Node("m", "a")}, MediaTypeJSON, []byte("ignored")); err != nil || w.Status != http.StatusNoContent || w.Location != nil {
		t.Errorf("a delete = %+v, %v", w, err)
	}
	if _, _, err := c.Read(ctx, "bad name", Path{Node("m", "a")}, MediaTypeJSON); err == nil {
		t.Error("a read of a datastore that is no identifier was sent")
	}
}

func TestRead_AStatusOtherThan200Or404IsAStatusError(t *testing.T) {
	c := refusingClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "missing") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}, httpapi.AuthNone)
	if _, found, err := c.Read(context.Background(), "", Path{Node("m", "missing")}, MediaTypeJSON); err != nil || found {
		t.Errorf("a 404 = %v, %v; want not found and no error", found, err)
	}
	if _, _, err := c.Read(context.Background(), "", Path{Node("m", "broken")}, MediaTypeJSON); err == nil {
		t.Error("a 500 read as a resource")
	}
}
