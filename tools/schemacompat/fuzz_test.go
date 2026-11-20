// Package main: fuzz targets for schemacompat's schema reader, version parser,
// and changelog token extractor.
package main

import (
	"reflect"
	"strings"
	"testing"
)

// FuzzCompareFile throws arbitrary schema pairs across all six contract kinds
// at compareFileOn and asserts no panics, deterministic findings, reflexivity
// when comparing a document with itself, and well-formed Finding tokens.
func FuzzCompareFile(f *testing.F) {
	kinds := []kind{inputSchema, outputSchema, openAPI, catalog, protocols, firstParty}

	// JSON Schema input and output with self-references, renames, and escapes.
	f.Add(uint8(0), false,
		[]byte(`{"type":"object","properties":{"a":{"$ref":"#/$defs/Old"},"x/y~z":{"type":["string","null"]}},"$defs":{"Old":{"type":"object","properties":{"next":{"$ref":"#/$defs/Old"}}}}}`),
		[]byte(`{"type":"object","properties":{"a":{"$ref":"#/$defs/New"},"x/y~z":{"type":"string"}},"$defs":{"New":{"type":"object","properties":{"next":{"$ref":"#/$defs/New"}}}}}`))
	f.Add(uint8(1), false,
		[]byte(`{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}`),
		[]byte(`{"type":"object","properties":{"id":{"type":"number"},"extra":{"type":"string"}}}`))
	// OpenAPI.
	f.Add(uint8(2), false,
		[]byte(`{"openapi":"3.1.0","security":[{"bearerAuth":["job:read"]}],"paths":{"/jobs/{id}":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"integer"}}}}}}}}}}}`),
		[]byte(`{"openapi":"3.1.0","paths":{"/jobs/{id}":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"integer"}}}}}}}}}}}`))
	// Module catalog.
	f.Add(uint8(3), false,
		[]byte(`{"exec.command":{"status":"implemented","requiredCapabilities":["CommandExecCapable"],"supportedTransports":["ssh"],"doc":{"params":[{"name":"cmd","type":"string"}],"returns":[{"name":"rc","type":"int"}]},"facts":[{"key":"os.family","type":"string","kind":"stable"}]}}`),
		[]byte(`{"exec.command":{"status":"implemented","requiredCapabilities":["CommandExecCapable","LinuxCapable"],"supportedTransports":[],"doc":{"params":[{"name":"cmd","type":"list","required":true}],"returns":[]},"facts":[]}}`))
	// Protocol window.
	f.Add(uint8(4), false,
		[]byte(`{"schema_version":1,"current":2,"versions":[{"version":1,"status":"deprecated","removed_in":"0.7.0"},{"version":2,"status":"supported"}]}`),
		[]byte(`{"schema_version":1,"current":1,"versions":[{"version":2,"status":"deprecated","removed_in":"0.6.0"}]}`))
	// First-party lists.
	f.Add(uint8(5), true,
		[]byte(`{"schema_version":1,"groups":[{"name":"facts","core":true}],"namespaces":["facts"],"fact_floor":["os."],"fact_family_owners":{"os.":["facts"]},"methods":{"facts.gather":"facts"}}`),
		[]byte(`{"schema_version":1,"groups":[{"name":"facts","core":true},{"name":"net"}],"namespaces":["facts","net"],"fact_floor":["os.","net."],"fact_family_owners":{"os.":["facts"]},"methods":{"facts.gather":"net"}}`))
	// Malformed JSON, non-objects, direct $ref cycles, and wrong-typed fields.
	f.Add(uint8(0), false, []byte(`{"$ref":"#/a","a":{"$ref":"#/a"}}`), []byte(`{"$ref":"#/b","b":{"$ref":"#/b"}}`))
	f.Add(uint8(0), false, []byte(`{`), []byte(`[]`))
	f.Add(uint8(2), false, []byte(`{"paths":"not-a-map"}`), []byte(`{"paths":{"/x":{"get":{"parameters":"bad"}}}}`))

	f.Fuzz(func(t *testing.T, kindIdx uint8, patch bool, oldB, newB []byte) {
		if len(oldB) > 64<<10 || len(newB) > 64<<10 {
			return
		}
		k := kinds[int(kindIdx)%len(kinds)]
		f1, err1 := compareFileOn("x.json", k, oldB, newB, patch)
		f2, err2 := compareFileOn("x.json", k, oldB, newB, patch)
		if (err1 == nil) != (err2 == nil) || !reflect.DeepEqual(f1, f2) {
			t.Fatalf("nondeterministic comparison: (%v, %v) vs (%v, %v)", f1, err1, f2, err2)
		}
		for _, item := range f1 {
			if item.Rule == "" || !strings.HasPrefix(item.Path, "x.json#") {
				t.Fatalf("malformed finding: %+v", item)
			}
		}
		same, sameErr := compareFileOn("x.json", k, oldB, oldB, patch)
		if sameErr == nil && len(same) != 0 {
			t.Fatalf("reflexive comparison produced findings: %+v", same)
		}
	})
}

// FuzzParseVersionAndTokens fuzzes release-tag parsing, baseTag selection, and
// breaking-fragment token extraction, asserting accepted tags hold only safe
// numeric components with no whitespace, leading hyphen, or shell characters.
func FuzzParseVersionAndTokens(f *testing.F) {
	seeds := []struct{ tag, frag string }{
		{"v0.3.0", "Dropped `property-removed run-report.json#/properties/a`."},
		{"0.4.0", "`method-removed module-catalog.json#/x.y` and `type-narrowed runbook.schema.json#/type`"},
		{"v0.4.0-rc.1", "No token here."},
		{"v999999999999999999999999999.0.0", ""},
		{" v0.3.0\n", ""},
		{"v0.3.0; touch /tmp/x", ""},
		{"$(id)", ""},
		{"--upload-pack=sh", ""},
		{"v-1.0.0", ""},
	}
	for _, s := range seeds {
		f.Add(s.tag, s.frag)
	}
	f.Fuzz(func(t *testing.T, tag, frag string) {
		if len(tag) > 4096 || len(frag) > 64<<10 {
			return
		}
		v, ok := parseVersion(tag)
		if ok {
			if v.major < 0 || v.minor < 0 || v.patch < 0 {
				t.Fatalf("parseVersion(%q) returned negative component: %+v", tag, v)
			}
			if strings.HasPrefix(tag, "-") || strings.ContainsAny(tag, " \t\n\r;|&$`\x00") {
				t.Fatalf("parseVersion accepted unsafe tag %q", tag)
			}
		}
		_ = baseTag([]string{tag, "v0.3.0", "v0.4.0"}, version{major: 0, minor: 4, patch: 0})
		_ = tokenPattern.FindAllStringSubmatch(frag, -1)
	})
}
