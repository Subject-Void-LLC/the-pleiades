// Package main: tests of the OpenAPI envelope and the catalog contract.
package main

import (
	"reflect"
	"testing"
)

// api builds a one-path OpenAPI document around one operation.
func api(op string) string {
	return `{"openapi":"3.1.0","paths":{"/jobs/{id}":{"get":` + op + `}}}`
}

func TestOpenAPIChecks(t *testing.T) {
	base := `{"security":[{"bearerAuth":["job:read"]}],
	  "parameters":[{"in":"query","name":"limit","required":null,"schema":{"type":"integer"}}],
	  "responses":{"200":{"content":{"application/json":{"schema":{"type":"object",
	    "properties":{"id":{"type":"integer"},"note":{"type":"string"}},"required":["id"]}}}},
	    "404":{"content":{"application/json":{"schema":{"type":"object"}}}}}}`
	cases := []struct {
		name, new string
		want      []string
	}{
		{name: "unchanged", new: api(base), want: []string{}},
		{name: "path removed", new: `{"openapi":"3.1.0","paths":{}}`, want: []string{"path-removed x.json#/paths/~1jobs~1{id}"}},
		{name: "operation removed", new: `{"openapi":"3.1.0","paths":{"/jobs/{id}":{"post":{}}}}`,
			want: []string{"operation-removed x.json#/paths/~1jobs~1{id}/get"}},
		{name: "parameter became required",
			new:  api(`{"security":[{"bearerAuth":["job:read"]}],"parameters":[{"in":"query","name":"limit","required":true,"schema":{"type":"integer"}}],"responses":{"200":{"content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"integer"},"note":{"type":"string"}},"required":["id"]}}}}}}`),
			want: []string{"parameter-became-required x.json#/paths/~1jobs~1{id}/get/parameters/query:limit"}},
		{name: "a required response property removed, an error status removed, a scope added",
			new: api(`{"security":[{"bearerAuth":["job:read","admin"]}],"parameters":[{"in":"query","name":"limit","schema":{"type":"integer"}}],"responses":{"200":{"content":{"application/json":{"schema":{"type":"object","properties":{"note":{"type":"string"}}}}}}}}`),
			want: []string{
				"property-removed x.json#/paths/~1jobs~1{id}/get/responses/200/content/application~1json/schema/properties/id",
				"scope-added x.json#/paths/~1jobs~1{id}/get/security/bearerAuth:admin",
			}},
		{name: "a parameter both required now and narrowed reports both",
			new: api(`{"security":[{"bearerAuth":["job:read"]}],"parameters":[{"in":"query","name":"limit","required":true,"schema":{"type":"integer","maximum":10}}],"responses":{"200":{"content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"integer"},"note":{"type":"string"}},"required":["id"]}}}}}}`),
			want: []string{
				"parameter-became-required x.json#/paths/~1jobs~1{id}/get/parameters/query:limit",
				"bound-tightened x.json#/paths/~1jobs~1{id}/get/parameters/query:limit/schema/maximum",
			}},
		{name: "the success status removed",
			new:  api(`{"security":[{"bearerAuth":["job:read"]}],"responses":{"201":{}}}`),
			want: []string{"response-status-removed x.json#/paths/~1jobs~1{id}/get/responses/200"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tokens(t, openAPI, api(base), tc.new); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("findings %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCatalogChecks(t *testing.T) {
	method := func(params, returns, facts, caps string) string {
		return `{"status":"implemented","requiredCapabilities":[` + caps + `],"supportedTransports":["ssh"],
		  "doc":{"params":[` + params + `],"returns":[` + returns + `]},"facts":[` + facts + `]}`
	}
	old := `{"exec.command":` + method(`{"name":"cmd","type":"string"},{"name":"chdir","type":"string","default":"/"}`,
		`{"name":"rc","type":"int"}`, `{"key":"os.family","type":"string","kind":"stable"},{"prefix":"package.","type":"object","kind":"stable"}`, `"CommandExecCapable"`) +
		`,"old.planned":{"status":"declared"}}`
	cases := []struct {
		name, new string
		want      []string
	}{
		{name: "unchanged", new: old, want: []string{}},
		{name: "a declared method removed is not a break", new: `{"exec.command":` + method(`{"name":"cmd","type":"string"},{"name":"chdir","type":"string","default":"/"}`,
			`{"name":"rc","type":"int"}`, `{"key":"os.family","type":"string","kind":"stable"},{"prefix":"package.","type":"object","kind":"stable"}`, `"CommandExecCapable"`) + `}`, want: []string{}},
		{name: "an implemented method removed", new: `{}`, want: []string{"method-removed x.json#/exec.command"}},
		{name: "a method no longer implemented", new: `{"exec.command":{"status":"declared"}}`, want: []string{"method-removed x.json#/exec.command"}},
		{name: "params, returns, facts and capabilities",
			new: `{"exec.command":` + method(`{"name":"cmd","type":"list of string","required":true},{"name":"chdir","type":"string","default":"/tmp"},{"name":"user","type":"string","required":true}`,
				``, `{"key":"os.family","type":"string","kind":"volatile"}`, `"CommandExecCapable","LinuxCapable"`) + `}`,
			want: []string{
				"param-default-changed x.json#/exec.command/params/chdir/default",
				"param-became-required x.json#/exec.command/params/cmd",
				"param-type-changed x.json#/exec.command/params/cmd/type",
				"param-added-required x.json#/exec.command/params/user",
				"return-removed x.json#/exec.command/returns/rc",
				"fact-kind-changed x.json#/exec.command/facts/os.family/kind",
				"fact-removed x.json#/exec.command/facts/package.*",
				"capability-required x.json#/exec.command/requiredCapabilities/LinuxCapable",
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tokens(t, catalog, old, tc.new); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("findings %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAnOperationInheritsTheDocumentsSecurity(t *testing.T) {
	// The operation names no security of its own, so the document's applies;
	// a scope added there reaches it.
	doc := func(scope string) string {
		return `{"openapi":"3.1.0","security":[{"bearerAuth":[` + scope + `]}],"paths":{"/x":{"get":{"responses":{"200":{}}}}}}`
	}
	want := []string{"scope-added x.json#/paths/~1x/get/security/bearerAuth:admin"}
	if got := tokens(t, openAPI, doc(``), doc(`"admin"`)); !reflect.DeepEqual(got, want) {
		t.Errorf("findings %q, want %q", got, want)
	}
}

func TestACatalogWithNothingImplementedIsAProblemNotAPass(t *testing.T) {
	old := `{"a.b":{"status":"Implemented"}}`
	if _, err := compareFile("module-catalog.json", catalog, []byte(old), []byte(old)); err == nil {
		t.Error("a catalog the check compared nothing in passed")
	}
}

func TestProtocolWindowChecks(t *testing.T) {
	doc := func(ps string) string { return `{"schemaVersion":1,"current":2,"protocols":[` + ps + `]}` }
	old := doc(`{"version":1,"status":"supported"},{"version":2,"status":"supported"}`)
	cases := []struct {
		name, new string
		want      []string
	}{
		{name: "a new version added", new: doc(`{"version":1,"status":"supported"},{"version":2,"status":"supported"},{"version":3,"status":"supported"}`), want: []string{}},
		{name: "a version deprecated", new: doc(`{"version":1,"status":"deprecated","removed_in":"0.6.0"},{"version":2,"status":"supported"}`),
			want: []string{"protocol-deprecated x.json#/protocols/1/status"}},
		{name: "a version removed", new: doc(`{"version":2,"status":"supported"}`), want: []string{"protocol-removed x.json#/protocols/1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tokens(t, protocols, old, tc.new); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("findings %q, want %q", got, tc.want)
			}
		})
	}
}
