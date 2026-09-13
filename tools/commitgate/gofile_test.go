// Package main: tests for the rules that apply only to staged Go files.
package main

import (
	"strings"
	"testing"
)

// wellFormedSource is a file that satisfies every Go rule here: it is
// gofmt clean, it opens with a docstring, and it is small.
const wellFormedSource = `// Package x does a thing.
package x

// F does the thing.
func F() {}
`

func TestCheckGoFileAcceptsAWellFormedAddedFile(t *testing.T) {
	file := stagedFile{Path: "internal/x/x.go", Kind: changeAdded}
	if findings := checkGoFile(file, []byte(wellFormedSource)); len(findings) != 0 {
		t.Errorf("reported %d findings against a well formed file: %+v", len(findings), findings)
	}
}

func TestCheckGoFileRefusesUnformattedSource(t *testing.T) {
	unformatted := "// Package x does a thing.\npackage x\n\n// F does the thing.\nfunc F()  {   }\n"
	file := stagedFile{Path: "internal/x/x.go", Kind: changeModified}
	if !hasRule(checkGoFile(file, []byte(unformatted)), SeverityFail, "gofmt") {
		t.Error("accepted source gofmt would rewrite")
	}
}

func TestCheckGoFileRefusesAnAddedFileWithNoDocstring(t *testing.T) {
	src := "package x\n\n// F does the thing.\nfunc F() {}\n"
	file := stagedFile{Path: "internal/x/x.go", Kind: changeAdded}
	findings := checkGoFile(file, []byte(src))
	if !hasRule(findings, SeverityFail, "docstring") {
		t.Fatal("accepted an added file with no doc comment above its package clause")
	}
	for _, f := range findings {
		if strings.Contains(f.Rule, "docstring") && !strings.Contains(f.Detail, "Package x") {
			t.Errorf("the finding does not tell the author what to write: %q", f.Detail)
		}
	}
}

func TestCheckGoFileDoesNotDemandADocstringFromAnExistingFile(t *testing.T) {
	// The rule asks for something that is cheap while writing a file and
	// noisy forever afterwards, so it fires only on a file the commit
	// adds. A rename reaches here as a modification for the same reason.
	src := "package x\n\n// F does the thing.\nfunc F() {}\n"
	file := stagedFile{Path: "internal/x/x.go", Kind: changeModified}
	if hasRule(checkGoFile(file, []byte(src)), SeverityFail, "docstring") {
		t.Error("demanded a docstring from a file this commit did not add")
	}
}

func TestCheckGoFileTreatsABuildTagAsNotADocstring(t *testing.T) {
	// go/parser only treats a comment group adjacent to the package
	// clause as the file's doc, so a build constraint followed by a blank
	// line leaves the file genuinely undocumented.
	src := "//go:build integration\n\npackage x\n\n// F does the thing.\nfunc F() {}\n"
	file := stagedFile{Path: "internal/x/x.go", Kind: changeAdded}
	if !hasRule(checkGoFile(file, []byte(src)), SeverityFail, "docstring") {
		t.Error("counted a build constraint as the file's docstring")
	}
}

func TestCheckGoFileRefusesSourceThatDoesNotParse(t *testing.T) {
	file := stagedFile{Path: "internal/x/x.go", Kind: changeAdded}
	findings := checkGoFile(file, []byte("package x\n\nfunc F( {\n"))
	if !hasRule(findings, SeverityFail, "parse") {
		t.Fatal("accepted Go source that does not parse")
	}
	if len(findings) != 1 {
		t.Errorf("reported %d findings for unparseable source, want exactly 1: every other rule would be reading a broken tree", len(findings))
	}
}

func TestCheckLogicSizeDoesNotCountComments(t *testing.T) {
	// A file that is long because it explains itself is following the
	// repository's own comment rules, so the cap must not punish it.
	var b strings.Builder
	b.WriteString("// Package x does a thing.\npackage x\n")
	for i := 0; i < 900; i++ {
		b.WriteString("// a line of explanation that carries no code at all\n")
	}
	b.WriteString("\n// F does the thing.\nfunc F() {}\n")
	if findings := checkLogicSize("internal/x/x.go", []byte(b.String())); len(findings) != 0 {
		t.Errorf("counted comment lines against the soft cap: %+v", findings)
	}
}

func TestCheckLogicSizeWarnsOverTheSoftCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("// Package x does a thing.\npackage x\n\n// F does the thing.\nfunc F() {\n")
	for i := 0; i < logicLineSoftCap+10; i++ {
		b.WriteString("\t_ = 1\n")
	}
	b.WriteString("}\n")
	findings := checkLogicSize("internal/x/x.go", []byte(b.String()))
	if !hasRule(findings, SeverityWarn, "soft cap") {
		t.Fatal("did not warn about a file well over the soft cap")
	}
	if hasRule(findings, SeverityFail, "soft cap") {
		t.Error("failed the commit over a cap AGENTS.md calls soft")
	}
}

func TestCheckGoFileSkipsTheSoftCapForTests(t *testing.T) {
	// AGENTS.md puts tests in a sibling file precisely so they are not
	// bound by the cap the logic file is.
	var b strings.Builder
	b.WriteString("// Package x: tests.\npackage x\n\n// TestF covers F.\nfunc TestF() {\n")
	for i := 0; i < logicLineSoftCap+10; i++ {
		b.WriteString("\t_ = 1\n")
	}
	b.WriteString("}\n")
	file := stagedFile{Path: "internal/x/x_test.go", Kind: changeAdded}
	if hasRule(checkGoFile(file, []byte(b.String())), SeverityWarn, "soft cap") {
		t.Error("applied the logic-file cap to a test file")
	}
}

func TestIsGoSource(t *testing.T) {
	cases := map[string]bool{
		"internal/journal/store.go":        true,
		"internal/ent/schema/journal.go":   true,
		"internal/ent/client.go":           true,
		"docs/10-running-in-production.md": false,
		"internal/x/x_test.go":             true,
		"Makefile":                         false,
	}
	for path, want := range cases {
		t.Run(path, func(t *testing.T) {
			if got := isGoSource(path); got != want {
				t.Errorf("isGoSource(%q) = %v, want %v", path, got, want)
			}
		})
	}
}

func TestIsGeneratedSourceReadsTheMarkerNotThePath(t *testing.T) {
	// The four hand-written files under internal/ent are the reason this
	// is a content question rather than a path question. Any path prefix
	// wide enough to catch the generated client also excuses every one of
	// them from every rule here.
	const marker = "// Code generated by ent, DO NOT EDIT."
	cases := map[string]struct {
		content string
		want    bool
	}{
		"ent client":   {marker + "\n\npackage ent\n", true},
		"after a tag":  {"//go:build x\n\n" + marker + "\n\npackage ent\n", true},
		"hand written": {"// Package ent holds the generated ent client. This file is hand written.\npackage ent\n", false},
		"quotes it":    {"// Package x explains what a generated marker looks like.\npackage x\n", false},
		"too far down": {"package x\n" + strings.Repeat("// filler\n", 20) + marker + "\n", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := isGeneratedSource([]byte(tc.content)); got != tc.want {
				t.Errorf("isGeneratedSource() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckGoFileReportsEveryRuleAtOnce(t *testing.T) {
	// A file that needs gofmt still has a perfectly good syntax tree, so
	// the other rules have something true to say. Stopping at the first
	// finding would hand the author one problem now and the rest after
	// they fixed it and committed again.
	src := "package x\n\nfunc  F()  {}\n"
	file := stagedFile{Path: "internal/x/x.go", Kind: changeAdded}
	findings := checkGoFile(file, []byte(src))
	if !hasRule(findings, SeverityFail, "gofmt") {
		t.Error("did not report the formatting problem")
	}
	if !hasRule(findings, SeverityFail, "docstring") {
		t.Error("did not report the missing docstring alongside the formatting problem")
	}
}
