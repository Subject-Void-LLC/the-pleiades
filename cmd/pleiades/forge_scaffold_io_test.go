package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestParseDocJSONFlag covers the one flag on `forge new-collection` that
// takes structured input rather than a plain value.
//
// The rejection cases are the point. This flag exists to carry a method's
// reference documentation into a generated manifest that
// internal/archtest then compares for exact equality, so an input this
// parser accepts loosely becomes a generated file that fails a guard in
// a different package with no hint that the input was wrong. Refusing
// here is what keeps that failure local to the thing that caused it.
func TestParseDocJSONFlag(t *testing.T) {
	t.Run("empty yields the zero Doc", func(t *testing.T) {
		doc, err := parseDocJSONFlag("")
		if err != nil {
			t.Fatalf("parseDocJSONFlag(\"\"): %v", err)
		}
		if doc.Summary != "" || len(doc.Params) != 0 {
			t.Errorf("an absent flag produced %+v, want the zero Doc", doc)
		}
	})

	t.Run("inline JSON", func(t *testing.T) {
		doc, err := parseDocJSONFlag(`{"summary":"Installs a package.","params":[{"name":"name","type":"string","required":true,"description":"The package."}]}`)
		if err != nil {
			t.Fatalf("parseDocJSONFlag: %v", err)
		}
		if doc.Summary != "Installs a package." {
			t.Errorf("Summary = %q, want %q", doc.Summary, "Installs a package.")
		}
		if len(doc.Params) != 1 || doc.Params[0].Name != "name" || !doc.Params[0].Required {
			t.Errorf("Params = %+v, want one required param named name", doc.Params)
		}
	})

	t.Run("@file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "doc.json")
		if err := os.WriteFile(path, []byte(`{"summary":"From a file."}`), 0o600); err != nil {
			t.Fatalf("writing the fixture: %v", err)
		}
		doc, err := parseDocJSONFlag("@" + path)
		if err != nil {
			t.Fatalf("parseDocJSONFlag(@file): %v", err)
		}
		if doc.Summary != "From a file." {
			t.Errorf("Summary = %q, want it read from the file", doc.Summary)
		}
	})

	t.Run("a missing file names the file", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "absent.json")
		_, err := parseDocJSONFlag("@" + missing)
		if err == nil {
			t.Fatal("a nonexistent --doc-json file was accepted")
		}
		if !strings.Contains(err.Error(), "--doc-json file") {
			t.Errorf("error = %q, want it to say which flag failed", err)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		_, err := parseDocJSONFlag(`{"summary":`)
		if err == nil {
			t.Fatal("truncated JSON was accepted")
		}
		if !strings.Contains(err.Error(), "collection.Doc") {
			t.Errorf("error = %q, want it to name what it was trying to parse", err)
		}
	})

	t.Run("an unknown field is refused, not ignored", func(t *testing.T) {
		_, err := parseDocJSONFlag(`{"summry":"a typo"}`)
		if err == nil {
			t.Fatal("a misspelled key was accepted, so the Doc would silently lose it")
		}
		if !strings.Contains(err.Error(), "summry") {
			t.Errorf("error = %q, want it to name the key it did not recognize", err)
		}
	})

	t.Run("an unknown field in a file names the file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "doc.json")
		if err := os.WriteFile(path, []byte(`{"nope":1}`), 0o600); err != nil {
			t.Fatalf("writing the fixture: %v", err)
		}
		_, err := parseDocJSONFlag("@" + path)
		if err == nil {
			t.Fatal("a misspelled key in a file was accepted")
		}
		// The path rather than the flag name, because with a file
		// involved that is the thing to open and fix.
		if !strings.Contains(err.Error(), path) {
			t.Errorf("error = %q, want it to name %s", err, path)
		}
	})

	t.Run("every Doc field survives", func(t *testing.T) {
		// Guards the decoder against a tag that does not match what
		// gencatalog's json.Marshal emits, which would silently drop a
		// whole section of a method's documentation.
		encoded := `{"summary":"s","description":"d","sinceVersion":"v1","deprecated":"dep",` +
			`"params":[{"name":"p","type":"string","required":true,"default":"x","choices":["a"],"description":"pd"}],` +
			`"fragments":["f"],` +
			`"returns":[{"name":"r","type":"bool","returned":"always","sample":"true","description":"rd"}],` +
			`"examples":[{"name":"e","runbookYaml":"- x\n"}],` +
			`"seeAlso":["other.method"]}`
		doc, err := parseDocJSONFlag(encoded)
		if err != nil {
			t.Fatalf("parseDocJSONFlag: %v", err)
		}
		want := collection.Doc{
			Summary: "s", Description: "d", SinceVersion: "v1", Deprecated: "dep",
			Params:    []collection.Param{{Name: "p", Type: "string", Required: true, Default: "x", Choices: []string{"a"}, Description: "pd"}},
			Fragments: []string{"f"},
			Returns:   []collection.ReturnField{{Name: "r", Type: "bool", Returned: "always", Sample: "true", Description: "rd"}},
			Examples:  []collection.Example{{Name: "e", RunbookYAML: "- x\n"}},
			SeeAlso:   []string{"other.method"},
		}
		if doc.Summary != want.Summary || doc.Description != want.Description ||
			doc.SinceVersion != want.SinceVersion || doc.Deprecated != want.Deprecated {
			t.Errorf("scalar fields = %+v, want %+v", doc, want)
		}
		// Field by field rather than with ==, since Param holds a slice
		// and is not a comparable type.
		if len(doc.Params) != 1 || doc.Params[0].Default != "x" || len(doc.Params[0].Choices) != 1 {
			t.Errorf("Params = %+v, want default x and one choice", doc.Params)
		}
		if len(doc.Fragments) != 1 || len(doc.Returns) != 1 || len(doc.Examples) != 1 || len(doc.SeeAlso) != 1 {
			t.Errorf("a repeated section was dropped: %+v", doc)
		}
		if doc.Returns[0].Sample != "true" || doc.Examples[0].RunbookYAML != "- x\n" {
			t.Errorf("a nested field was dropped: %+v", doc)
		}
	})
}

// TestFirstExistingFile covers the check `--skip-existing` decides on.
//
// The empty-string answer is the one worth pinning. It means "none of
// these are on disk," which is what lets the caller write the entry; a
// bug that returned a path there would make the forge silently generate
// nothing, and a run that generates nothing looks exactly like a run
// whose work was already done.
func TestFirstExistingFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "present.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	t.Run("none exist", func(t *testing.T) {
		got, err := firstExistingFile(dir, []string{"a.go", "b.go"})
		if err != nil {
			t.Fatalf("firstExistingFile: %v", err)
		}
		if got != "" {
			t.Errorf("firstExistingFile returned %q for paths that do not exist", got)
		}
	})

	t.Run("the second one exists", func(t *testing.T) {
		got, err := firstExistingFile(dir, []string{"absent.go", "present.go"})
		if err != nil {
			t.Fatalf("firstExistingFile: %v", err)
		}
		if want := filepath.Join(dir, "present.go"); got != want {
			t.Errorf("firstExistingFile = %q, want %q: an entry is skipped when ANY of its files is there", got, want)
		}
	})

	t.Run("no paths at all", func(t *testing.T) {
		got, err := firstExistingFile(dir, nil)
		if err != nil {
			t.Fatalf("firstExistingFile: %v", err)
		}
		if got != "" {
			t.Errorf("firstExistingFile = %q for an empty set, want the empty string", got)
		}
	})
}
