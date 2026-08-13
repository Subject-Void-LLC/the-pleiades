package credtype_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// FuzzInjectorDocument is the Phase 22 checklist's literal fuzz gate:
// "fuzz injector rendering against malformed and hostile templates."
//
// It fuzzes raw JSON bytes, not a pre-built struct, because that is the
// shape the real threat arrives in. A credential type is written through
// the API by any holder of credential:write and stored as a JSON document,
// so the deserializer is the first thing a hostile input touches, and it is
// one of the boundaries Phase 39's own audit categories name.
//
// The properties below are what make this a gate rather than a crash hunt.
// Two of them are security statements that no table of hand-written cases
// can establish, because they have to hold for every document rather than
// for the ones somebody thought of.
func FuzzInjectorDocument(f *testing.F) {
	seeds := []string{
		// The real corpus injector document, from a production Ascender
		// deployment.
		`{"env":{"REST_API_TOKEN":"{{ api_token }}","REST_API_URL":"{{ api_url }}"},"extra_vars":{"ansible_api_token":"{{ api_token }}"}}`,

		// Every legitimate shape.
		`{}`,
		`{"env":{"A":"{{ api_token }}"}}`,
		`{"extra_vars":{"a":{"b":"{{ api_token }}"}}}`,
		`{"extra_vars":{"flag":true,"port":443}}`,
		`{"file":{"template":"token={{ api_token }}"}}`,
		`{"file":{"template.cert":"{{ api_token }}","template.key":"{{ api_url }}"}}`,
		`{"env":{"P":"{{ tower.filename }}"},"file":{"template":"x"}}`,

		// Shapes the validator must refuse.
		`{"env":{"LD_PRELOAD":"{{ api_token }}"}}`,
		`{"env":{"PATH":"{{ api_token }}"}}`,
		`{"env":{"BASH_FUNC_x":"{{ api_token }}"}}`,
		`{"env":{"A-B":"{{ api_token }}"}}`,
		`{"env":{"":"{{ api_token }}"}}`,
		`{"env":{"A":"{{ undeclared }}"}}`,
		`{"env":{"A":"{{"}}`,
		`{"env":{"A":"{% for x in y %}{% endfor %}"}}`,
		`{"file":{"template":"a","template.cert":"b"}}`,
		`{"file":{"template./etc/passwd":"a"}}`,
		`{"file":{"template.../../etc/passwd":"a"}}`,
		`{"extra_vars":{"a":null}}`,
		`{"extra_vars":{"a":["{{ api_token }}"]}}`,
		`{"extra_vars":{"":"x"}}`,

		// Malformed and hostile input.
		``,
		`{`,
		`null`,
		`[]`,
		`{"env":null}`,
		`{"env":[]}`,
		`{"env":{"A":123}}`,
		`{"extra_vars":{"a":{"b":{"c":{"d":{"e":"{{ api_token }}"}}}}}}`,
		`{"env":{"A":"` + strings.Repeat("{{", 500) + `"}}`,
		`{"unknown_target":{"A":"x"}}`,
		"{\"env\":{\"A\":\"\x00\"}}",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	// The schema every fuzzed document is validated against. Fixed rather
	// than fuzzed so that "references an undeclared input" has a stable
	// meaning across runs.
	schema := credtype.InputSchema{
		Fields: []credtype.InputField{
			{ID: "api_token", Label: "Token", Secret: true},
			{ID: "api_url", Label: "URL"},
		},
	}
	eng := render.New()

	f.Fuzz(func(t *testing.T, document string) {
		var inj credtype.Injectors
		if err := json.Unmarshal([]byte(document), &inj); err != nil {
			// A document that does not decode never reaches validation,
			// which is the correct outcome and nothing further to assert.
			return
		}

		// Property 1: validation never panics, for any decodable document.
		// The f.Fuzz harness itself is the assertion.
		err := inj.Validate(schema, eng)
		if err != nil {
			return
		}

		// Everything past this point holds for documents the validator
		// ACCEPTED, which is the interesting half: a rejected document is
		// already safe.

		// Property 2, the injection statement. A validated document may
		// never name a reserved environment variable. This is the headline
		// item of the phase's Schema and Injection Hardening audit: the
		// customer's playbook runs inside the trust boundary, so an
		// injector that can set LD_PRELOAD is code execution inside the
		// run the credential was meant to authenticate.
		for name := range inj.Env {
			if reservedEnvName(name) {
				t.Fatalf("Validate() accepted a document setting the reserved environment variable %q: %s", name, document)
			}
		}

		// Property 3, the path statement. A validated document's file
		// labels may never contain a path separator or a parent reference.
		// A label becomes part of a generated filename, so one containing
		// "/" or ".." would write a credential outside the directory
		// chosen for it.
		for _, label := range inj.FileLabels() {
			if strings.ContainsAny(label, `/\`) || strings.Contains(label, "..") {
				t.Fatalf("Validate() accepted a file label that escapes its directory: %q in %s", label, document)
			}
		}

		// Property 4, the strict-undefined statement, and the reason
		// validation takes a render engine at all. Every template in a
		// validated document must compile and must reference only declared
		// inputs or the reserved namespace, so rendering it at launch can
		// never fail on an undefined variable. Checking it here, over
		// arbitrary documents, is what makes that a property rather than a
		// claim about the cases in validate_test.go.
		for name, tmpl := range inj.Env {
			assertRenderable(t, eng, schema, document, "env "+name, tmpl)
		}
		for key, tmpl := range inj.File {
			assertRenderable(t, eng, schema, document, "file "+key, tmpl)
		}
	})
}

// assertRenderable checks that a template a validated document carries
// compiles and names nothing undeclared.
func assertRenderable(t *testing.T, eng render.Engine, schema credtype.InputSchema, document, where, source string) {
	t.Helper()

	tmpl, err := eng.Compile(source)
	if err != nil {
		t.Fatalf("Validate() accepted a document whose %s template does not compile: %v (%s)", where, err, document)
	}

	known := map[string]bool{credtype.ReservedTower: true, credtype.ReservedPleiades: true}
	for _, id := range schema.IDs() {
		known[id] = true
	}

	for _, name := range tmpl.Names() {
		if !known[name] {
			t.Fatalf("Validate() accepted a document whose %s template references the undeclared %q: %s", where, name, document)
		}
	}
}

// reservedEnvName restates the reserved set independently of the
// implementation.
//
// Deliberately a second copy rather than a reference to the package's own
// map. A property test that asked the implementation what it forbids and
// then checked it forbade exactly that would prove only internal
// agreement, which LESSONS_LEARNED.md #99 records as the characteristic
// way a consistency test proves nothing. This list is the requirement,
// written out where a reader can compare it to the audit.
func reservedEnvName(name string) bool {
	switch name {
	case "LD_PRELOAD", "LD_LIBRARY_PATH", "LD_AUDIT",
		"PYTHONPATH", "PYTHONHOME", "PYTHONSTARTUP",
		"BASH_ENV", "ENV", "IFS", "PATH", "HOME",
		"ANSIBLE_CONFIG", "ANSIBLE_FORCE_COLOR", "ANSIBLE_NOCOLOR":
		return true
	}
	return strings.HasPrefix(name, "BASH_FUNC_")
}
