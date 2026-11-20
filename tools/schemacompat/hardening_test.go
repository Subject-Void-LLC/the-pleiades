// Package main: Schema and Injection Hardening tests for schemacompat's tag
// validation, git argument passing, and recursive $ref cycle handling.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestRunAndGitShow_RefuseHostileTagsAndNeverInvokeAShell proves hostile tags
// (shell metacharacters, git flags, padded strings, negatives, overflows, and
// non-releases) are refused before reaching git, and that git commands never
// pass through a shell.
func TestRunAndGitShow_RefuseHostileTagsAndNeverInvokeAShell(t *testing.T) {
	repo(t, map[string]string{"run-report.json": `{"type":"object","properties":{"ok":{"type":"boolean"}}}`})

	marker := filepath.Join(t.TempDir(), "pwned")
	hostileTags := []struct {
		name string
		tag  string
	}{
		{name: "semicolon shell cmd", tag: "v0.3.0; touch " + marker},
		{name: "command substitution", tag: "$(touch " + marker + ")"},
		{name: "backtick substitution", tag: "`touch " + marker + "`"},
		{name: "pipe shell cmd", tag: "v0.3.0|touch " + marker},
		{name: "newline shell cmd", tag: "v0.3.0\ntouch " + marker},
		{name: "git upload-pack flag", tag: "--upload-pack=touch " + marker},
		{name: "git short flag", tag: "-h"},
		{name: "git format flag", tag: "--format=%H"},
		{name: "git all flag", tag: "--all"},
		{name: "leading space", tag: " v0.3.0"},
		{name: "trailing newline", tag: "v0.3.0\n"},
		{name: "negative with v", tag: "v-1.0.0"},
		{name: "negative bare", tag: "-1.0.0"},
		{name: "huge major overflow", tag: "v999999999999999999999999999.0.0"},
		{name: "huge minor overflow", tag: "v0.999999999999999999999999999.0"},
		{name: "huge patch overflow", tag: "v0.3.999999999999999999999999999"},
		{name: "non-release string", tag: "not-a-tag"},
		{name: "prerelease tag", tag: "v0.3.0-rc.1"},
	}

	for _, tc := range hostileTags {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := run(gitShow{}, tc.tag, "0.4.0", schemaDir, "changelog"); err == nil || !strings.Contains(err.Error(), "not a release tag") {
				t.Errorf("run(%q) err = %v, want refusal naming not a release tag", tc.tag, err)
			}
			if _, err := (gitShow{}).list(tc.tag, schemaDir); err == nil || !strings.Contains(err.Error(), "not a release tag") {
				t.Errorf("gitShow.list(%q) err = %v, want refusal naming not a release tag", tc.tag, err)
			}
			if _, err := (gitShow{}).read(tc.tag, schemaDir+"/run-report.json"); err == nil || !strings.Contains(err.Error(), "not a release tag") {
				t.Errorf("gitShow.read(%q) err = %v, want refusal naming not a release tag", tc.tag, err)
			}
			if _, statErr := os.Stat(marker); statErr == nil {
				t.Fatalf("marker %s was created while checking tag %q", marker, tc.tag)
			}
		})
	}

	// Prove git() invokes the git binary directly with argv rather than a shell.
	if _, err := git("rev-parse", "--verify", "$(touch "+marker+")"); err == nil {
		t.Fatal("git accepted unresolvable metacharacter revision")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("git() evaluated shell metacharacters and created %s", marker)
	}

	// A real tag in the repository holding shell metacharacters is ignored by
	// baseTag and never blocks or runs against a valid release tag.
	if out, err := exec.Command("git", "tag", "bad;tag").CombinedOutput(); err != nil {
		t.Fatalf("creating non-release tag: %v: %s", err, out)
	}
	v, err := run(gitShow{}, "", "0.4.0", schemaDir, "changelog")
	if err != nil || !v.Pass || v.Base != "v0.3.0" {
		t.Fatalf("run with hostile repo tag = %+v, err = %v; want pass against v0.3.0", v, err)
	}
}

// TestFollowRefs_RenamedRecursiveDefinitionDoesNotLoop proves comparing a
// renamed self-referential $defs cycle terminates cleanly instead of recursing
// until stack overflow, and still reports changes inside the definition.
func TestFollowRefs_RenamedRecursiveDefinitionDoesNotLoop(t *testing.T) {
	old := `{"properties":{"step":{"$ref":"#/$defs/Old"}},"$defs":{"Old":{"type":"object","properties":{"name":{"type":["string","null"]},"next":{"$ref":"#/$defs/Old"}}}}}`
	sameShape := `{"properties":{"step":{"$ref":"#/$defs/New"}},"$defs":{"New":{"type":"object","properties":{"name":{"type":["string","null"]},"next":{"$ref":"#/$defs/New"}}}}}`
	if got := tokens(t, inputSchema, old, sameShape); len(got) != 0 {
		t.Errorf("renamed recursive definition with identical shape reported %v, want none", got)
	}

	narrowed := `{"properties":{"step":{"$ref":"#/$defs/New"}},"$defs":{"New":{"type":"object","properties":{"name":{"type":"string"},"next":{"$ref":"#/$defs/New"}}}}}`
	got := tokens(t, inputSchema, old, narrowed)
	want := []string{"type-narrowed x.json#/properties/step/properties/name/type"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("renamed narrowed recursive definition reported %v, want %v", got, want)
	}
}

// TestCompareProtocolsAndFirstParty_SurviveMalformedNonScalarFields proves
// fuzz-discovered non-float "current" fields and non-comparable slice/map
// values in first-party metadata return clean errors or findings rather than
// panicking.
func TestCompareProtocolsAndFirstParty_SurviveMalformedNonScalarFields(t *testing.T) {
	if _, err := compareFile("extension-protocols.json", protocols, []byte(`{}`), []byte(`{}`)); err == nil {
		t.Error("empty protocols object should error rather than pass or panic")
	}
	if _, err := compareFile("extension-protocols.json", protocols, []byte(`{"versions":[{"version":1,"status":"supported"}]}`), []byte(`{"current":null,"versions":[{"version":1,"status":"supported"}]}`)); err != nil {
		t.Errorf("protocols missing current panicked or errored unexpectedly: %v", err)
	}
	malformedFP := `{"groups":[{"name":"facts","core":[]}],"methods":{"facts.gather":["facts"]}}`
	if _, err := compareFileOn("first-party.json", firstParty, []byte(malformedFP), []byte(malformedFP), false); err != nil {
		t.Errorf("first-party with slice fields errored unexpectedly: %v", err)
	}
}
