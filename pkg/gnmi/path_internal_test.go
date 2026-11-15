// Package gnmi: the path parser and its wire form, held to gNMI's path
// conventions and to every refusal the parser makes.
package gnmi

import (
	"strings"
	"testing"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

// TestParsePath_ReadsTheConventions reads each form the conventions allow and
// holds String to writing it back the same way.
func TestParsePath_ReadsTheConventions(t *testing.T) {
	for _, tc := range []struct {
		in, out string
		origin  string
		elems   int
	}{
		{in: "/", out: "/"},
		{in: "openconfig:/", out: "openconfig:/", origin: "openconfig"},
		{in: "/interfaces/interface[name=eth0]/config", out: "/interfaces/interface[name=eth0]/config", elems: 3},
		{in: "openconfig:/system/config/hostname", out: "openconfig:/system/config/hostname", origin: "openconfig", elems: 3},
		{in: "/Cisco-IOS-XE-native:native/hostname", out: "/Cisco-IOS-XE-native:native/hostname", elems: 2},
		// A key value keeps "/" and "=", and escapes "]" and "\".
		{in: `/a/b[name=ethernet-1/1]`, out: `/a/b[name=ethernet-1/1]`, elems: 2},
		{in: `/a[k=x=y]`, out: `/a[k=x=y]`, elems: 1},
		{in: `/a[k=x\]y]`, out: `/a[k=x\]y]`, elems: 1},
		{in: `/a[k=x\\y]`, out: `/a[k=x\\y]`, elems: 1},
		// Keys come back sorted.
		{in: "/net/inst[z=1][a=2]", out: "/net/inst[a=2][z=1]", elems: 2},
		{in: "/interfaces/interface[name=*]/.../counters", out: "/interfaces/interface[name=*]/.../counters", elems: 4},
		{in: "/a[k=]", out: "/a[k=]", elems: 1},
	} {
		p, err := ParsePath(tc.in)
		if err != nil {
			t.Errorf("ParsePath(%q) = %v", tc.in, err)
			continue
		}
		if p.Origin != tc.origin || len(p.Elems) != tc.elems || p.String() != tc.out {
			t.Errorf("ParsePath(%q) = %+v as %q, want origin %q, %d elements, %q", tc.in, p, p.String(), tc.origin, tc.elems, tc.out)
		}
		back, err := ParsePath(p.String())
		if err != nil || !back.Equal(p) {
			t.Errorf("ParsePath(String(%q)) = %+v, %v, want it equal", tc.in, back, err)
		}
	}
}

// TestParsePath_RefusesWhatCouldMoveAKey refuses each malformed path rather
// than reading it as some other node.
func TestParsePath_RefusesWhatCouldMoveAKey(t *testing.T) {
	for _, in := range []string{
		"",
		"interfaces",
		"/a//b",
		"/a/",
		"/a[k=v",
		"/a[kv]",
		"/a[k=v][k=w]",
		`/a[k=v\x]`,
		`/a[k=v\]`,
		"/a b",
		"/a]b",
		`/a\b`,
		"/a[k=v]x",
		"/1a",
		"/a[1k=v]",
		"/a[k=v\x01]",
		"/a[k=v\u200b]",
		"bad origin:/a",
		"/a:b:c",
		":/a",
		"/" + strings.Repeat("a/", MaxElems) + "a",
		"/a[k=" + strings.Repeat("v", MaxKeyBytes+1) + "]",
		"/a[k=\xff]",
	} {
		if p, err := ParsePath(in); err == nil {
			t.Errorf("ParsePath(%q) = %q, want it refused", in, p)
		}
	}
}

// TestValidate_RefusesWildcardsWhereOneNodeIsNamed holds a write path to
// naming one node.
func TestValidate_RefusesWildcardsWhereOneNodeIsNamed(t *testing.T) {
	for _, in := range []string{"/a/*", "/a/.../b", "/a[k=*]"} {
		p, err := ParsePath(in)
		if err != nil {
			t.Fatal(err)
		}
		if !p.HasWildcard() || p.Validate(false) == nil {
			t.Errorf("%s: HasWildcard %v, Validate(false) accepted it", in, p.HasWildcard())
		}
	}
}

// TestJoin_TakesThePathsOriginFirst joins a path under a prefix.
func TestJoin_TakesThePathsOriginFirst(t *testing.T) {
	prefix, _ := ParsePath("openconfig:/interfaces")
	path, _ := ParsePath("/interface[name=e1]")
	if got := Join(prefix, path).String(); got != "openconfig:/interfaces/interface[name=e1]" {
		t.Errorf("Join = %s", got)
	}
	other, _ := ParsePath("rfc7951:/x")
	if got := Join(prefix, other).Origin; got != "rfc7951" {
		t.Errorf("Join's origin = %s, want the path's own", got)
	}
	if Join(prefix, path).Equal(prefix) {
		t.Error("Equal reports a longer path equal to its prefix")
	}
}

// TestPathFromProto_HoldsADevicesPathToTheSameRules reads paths a device
// sent, refusing the deprecated element field and anything the parser would.
func TestPathFromProto_HoldsADevicesPathToTheSameRules(t *testing.T) {
	good := &gpb.Path{Origin: "openconfig", Elem: []*gpb.PathElem{{Name: "interfaces"}, {Name: "interface", Key: map[string]string{"name": "e1"}}}}
	p, err := pathFromProto(good)
	if err != nil || p.String() != "openconfig:/interfaces/interface[name=e1]" {
		t.Fatalf("pathFromProto = %q, %v", p, err)
	}
	if back := p.proto(); back.GetElem()[1].GetKey()["name"] != "e1" || back.GetOrigin() != "openconfig" {
		t.Errorf("proto = %v", back)
	}
	if p, err := pathFromProto(nil); err != nil || len(p.Elems) != 0 {
		t.Errorf("pathFromProto(nil) = %+v, %v, want the root", p, err)
	}
	manyKeys := map[string]string{}
	for i := 0; i <= MaxKeys; i++ {
		manyKeys["k"+strings.Repeat("x", i)] = "v"
	}
	for name, bad := range map[string]*gpb.Path{
		"element":   {Element: []string{"interfaces", "interface[name=e1]"}},
		"bad name":  {Elem: []*gpb.PathElem{{Name: "a/b"}}},
		"bad value": {Elem: []*gpb.PathElem{{Name: "a", Key: map[string]string{"k": "\n"}}}},
		"many keys": {Elem: []*gpb.PathElem{{Name: "a", Key: manyKeys}}},
		"long":      {Elem: make([]*gpb.PathElem, MaxElems+1)},
	} {
		if _, err := pathFromProto(bad); err == nil {
			t.Errorf("%s: pathFromProto accepted it", name)
		}
	}
}
