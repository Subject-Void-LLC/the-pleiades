// Package yanglib: tests of CheckSource, which holds a fetched answer to
// being the YANG source asked for.
package yanglib

import (
	"strings"
	"testing"
)

// probeSource is a module written the way the netconf live tests' is: words a
// shape-based masker reads as secrets (a password leaf, a list's key, the
// word secret, numbers, an address), all of them schema.
const probeSource = `/* A comment naming module other { revision 1999-01-01; } */
// module other {
module pleiades-probe {
  yang-version 1.1;
  namespace "urn:pleiades:probe";
  prefix pp;
  description "revision 2001-01-01 is only words here";
  revision "2026-11-16" {
    description 'the secret key 100 at 10.1.2.3';
  }
  revision 2025-01-01;
  container users {
    list user {
      key "name";
      leaf name { type string; }
      leaf password { type string; }
    }
  }
}
`

func TestCheckSource_AcceptsTheModuleAskedFor(t *testing.T) {
	for _, rev := range []string{"2026-11-16", "2025-01-01", ""} {
		if err := CheckSource([]byte(probeSource), Schema{Name: "pleiades-probe", Revision: rev}, false); err != nil {
			t.Errorf("revision %q: %v", rev, err)
		}
	}
	// A byte-order mark, a quoted name and a joined one are all YANG.
	for _, src := range []string{
		"\xef\xbb\xbfmodule m { revision 2024-01-01; }",
		`module "m" { revision "2024-01-01"; }`,
		`module "" + 'm' { revision "2024" + "-01-01"; }`,
		"submodule m {\n belongs-to x { prefix x; }\n revision 2024-01-01;\n}\n// trailing comment",
	} {
		sub := strings.Contains(src, "submodule")
		if err := CheckSource([]byte(src), Schema{Name: "m", Revision: "2024-01-01"}, sub); err != nil {
			t.Errorf("CheckSource(%q): %v", src, err)
		}
	}
}

// TestCheckSource_RefusesAnythingElse holds each refusal: an answer that is
// not the source asked for is never kept under its name.
func TestCheckSource_RefusesAnythingElse(t *testing.T) {
	want := Schema{Name: "pleiades-probe", Revision: "2026-11-16"}
	cases := map[string]string{
		"another module":            strings.Replace(probeSource, "module pleiades-probe", "module pleiades-other", 1),
		"a revision only in words":  strings.Replace(strings.Replace(probeSource, `revision "2026-11-16"`, `revision "2020-01-01"`, 1), "2001-01-01", "2026-11-16", 1),
		"a nested revision only":    "module pleiades-probe { container c { revision 2026-11-16; } }",
		"a login page":              "<html><body>Login</body></html>",
		"a JSON error":              `{"ietf-restconf:errors": {}}`,
		"not UTF-8":                 "module pleiades-probe { revision 2026-11-16; \xff }",
		"a control character":       "module pleiades-probe { revision 2026-11-16; \x1b[2J }",
		"a NUL":                     "module pleiades-probe { revision 2026-11-16; \x00 }",
		"an open comment":           "module pleiades-probe { revision 2026-11-16; /* }",
		"an open string":            `module pleiades-probe { revision 2026-11-16; description "x }`,
		"an unclosed module":        "module pleiades-probe { revision 2026-11-16;",
		"more after the module":     "module pleiades-probe { revision 2026-11-16; } module x { }",
		"an extra close":            "module pleiades-probe { revision 2026-11-16; } }",
		"no body":                   "module pleiades-probe;",
		"no name":                   "module { revision 2026-11-16; }",
		"empty":                     "",
		"a submodule":               "submodule pleiades-probe { revision 2026-11-16; }",
		"a bad join":                `module "pleiades-" + probe { revision 2026-11-16; }`,
		"a statement of braces":     "module pleiades-probe { { } }",
		"a statement ending badly":  "module pleiades-probe { revision 2026-11-16 }",
		"a nesting bomb":            "module pleiades-probe { revision 2026-11-16; " + strings.Repeat("a {", maxSourceDepth+1),
		"a string where a keyword":  `module pleiades-probe { "revision" 2026-11-16; }`,
		"an argument then a string": `module pleiades-probe { revision 2026-11-16 "x"; }`,
	}
	for name, src := range cases {
		if err := CheckSource([]byte(src), want, false); err == nil {
			t.Errorf("%s: accepted, want a refusal", name)
		} else if !strings.Contains(err.Error(), "module pleiades-probe@2026-11-16") {
			t.Errorf("%s: %v, want the refusal to name what was asked for", name, err)
		}
	}
	if err := CheckSource([]byte("module m { }"), Schema{Name: "m"}, true); err == nil || !strings.Contains(err.Error(), "submodule m") {
		t.Errorf("a module asked for as a submodule: %v, want a refusal naming the submodule", err)
	}
}

// TestCheckSource_NeverQuotesTheAnswer holds every refusal to naming kinds
// of token only: a debug page echoing a login's Authorization header, at
// each place a refusal could have quoted it, never reaches the error.
func TestCheckSource_NeverQuotesTheAnswer(t *testing.T) {
	const echoed = "YWRtaW46czNjcmV0LVBhc3N3MHJk"
	for _, src := range []string{
		"Authorization: Basic " + echoed + "\n",
		"module " + echoed + " { }",
		echoed + " pleiades-probe { }",
		"module pleiades-probe { revision 2026-11-16 " + echoed + " }",
		`module pleiades-probe { "` + echoed + `" x; }`,
		"module pleiades-probe { revision 2026-11-16; } " + echoed,
	} {
		err := CheckSource([]byte(src), Schema{Name: "pleiades-probe", Revision: "2026-11-16"}, false)
		if err == nil || strings.Contains(err.Error(), echoed) || strings.Contains(err.Error(), "Basic") {
			t.Errorf("CheckSource(%q) = %v, want a refusal quoting none of the answer", src, err)
		}
	}
}

// FuzzCheckSource holds CheckSource to returning, whatever it is handed.
func FuzzCheckSource(f *testing.F) {
	f.Add([]byte(probeSource), "pleiades-probe", "2026-11-16")
	f.Add([]byte(`module "a" + 'b' { revision "x"; }`), "ab", "")
	f.Add([]byte("/* */ // \n module m { a { b; } }"), "m", "")
	f.Fuzz(func(t *testing.T, src []byte, name, revision string) {
		_ = CheckSource(src, Schema{Name: name, Revision: revision}, false)
		_ = CheckSource(src, Schema{Name: name, Revision: revision}, true)
	})
}
