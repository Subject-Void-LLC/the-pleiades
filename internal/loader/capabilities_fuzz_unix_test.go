//go:build unix

// Package loader: a fuzz test of the capability section of a program's
// describe document, through both of the loader's passes.
package loader

import (
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// FuzzDescribeCapabilities feeds the loader a describe document an
// untrusted program controls completely, its capability section included,
// and holds its two passes to each other: whatever pass one
// (validateCandidates) accepts, pass two (register) defines and registers
// without a refusal, so a directory never loads halfway; every capability
// it defines is registered under the program's file name, never the name
// the program gave itself, with a digest of a digest's shape; and nothing
// pass one refused is defined at all.
func FuzzDescribeCapabilities(f *testing.F) {
	method := `{"name":"acme.widget.read","manifest":{"status":"implemented","reversibility":{"reversible":false,"notes":"a fixture"},"requiredCapabilities":["acme.widget_api"]}}`
	probe := `{"name":"acme.widget.probe","manifest":{"status":"implemented","reversibility":{"readOnly":true,"notes":"reads"}}}`
	for _, seed := range []string{
		`{"protocol":1,"methods":[` + method + `],"capabilities":[{"name":"acme.widget_api","properties":[{"name":"endpoint","type":"string","required":true}]}]}`,
		`{"protocol":1,"methods":[` + method + `,` + probe + `],"capabilities":[{"name":"acme.widget_api","provider":"someone-else","probe":"acme.widget.probe","revision":4,"requires":["SSHTransportCapable"]}]}`,
		`{"protocol":1,"methods":[` + method + `],"capabilities":[{"name":"acme.widget_api","revision":-1}]}`,
		`{"protocol":1,"methods":[` + method + `],"capabilities":[{"name":"acme.widget_api"},{"name":"acme.widget_api"}]}`,
		`{"protocol":1,"methods":[` + method + `],"capabilities":[{"name":"acme.widget_api","properties":[{"name":"api_password","type":"string"}]}]}`,
		`{"protocol":1,"methods":[` + method + `],"capabilities":[{"name":"pleiades.widget_api"}]}`,
		`{"protocol":1,"methods":[` + method + `],"capabilities":[{"name":"acme.widget_api","doc":"a\u001b[2Jb"}]}`,
		`{"protocol":1,"methods":[` + method + `],"capabilities":[{"name":"acme.widget_api","requires":["acme.other"]}]}`,
		`{"protocol":1,"methods":[` + method + `],"capabilities":null}`,
		`{"protocol":1,"methods":[` + method + `],"capabilities":[{"name":"acme.widget_api","properties":[{"name":"p","type":"int"},{"name":"p","type":"int"}]}]}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		defer collection.SnapshotForTest()()
		defer capability.SnapshotForTest()()
		desc, err := parseDescription(data)
		if err != nil {
			return
		}
		path := "/fuzz/widgets"
		found := []candidate{{path: path, digest: "sha256:" + "ab", desc: desc}}
		if _, problems := validateCandidates(found, "dev", reservedNamespaces()); len(problems) > 0 {
			for _, def := range desc.Capabilities {
				if d, ok := capability.DataOf(def.Name); ok {
					t.Fatalf("pass one refused the description, yet %q is defined: %+v", def.Name, d)
				}
			}
			return
		}
		if _, err := register(found, Options{}); err != nil {
			t.Fatalf("pass one accepted %s and pass two refused it: %v", data, err)
		}
		for _, def := range desc.Capabilities {
			got, ok := capability.DataOf(def.Name)
			if !ok {
				t.Fatalf("pass two did not define %q", def.Name)
			}
			if got.Provider != filepath.Base(path) {
				t.Fatalf("%q is provided by %q, want the program's file name", def.Name, got.Provider)
			}
			if !capability.IsDefinitionDigest(capability.DefinitionDigest(got)) {
				t.Fatalf("%q's digest has the wrong shape", def.Name)
			}
		}
	})
}
