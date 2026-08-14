package managed

import (
	"strings"
	"testing"
	"testing/fstest"
)

// The catalog parser's guards.
//
// An internal test, because parseTypes is deliberately unexported: it
// exists so mustParse's panic stays unreachable for the embedded data
// while the checks that would catch a badly added document are still
// exercised. Each guard here refuses something a future contributor could
// plausibly write, and each one would otherwise be code that has never
// run.

// TestParseTypesReadsAWellFormedDirectory is the positive control. Without
// it the refusals below could all pass while the parser accepted nothing.
func TestParseTypesReadsAWellFormedDirectory(t *testing.T) {
	t.Parallel()

	files := fstest.MapFS{
		"types/one.json": &fstest.MapFile{Data: []byte(`{"name":"One","namespace":"one","kind":"cloud","managed":true,"inputs":{},"injectors":{}}`)},
		"types/two.json": &fstest.MapFile{Data: []byte(`{"name":"Two","namespace":"two","kind":"cloud","managed":true,"inputs":{},"injectors":{}}`)},
	}

	out, err := parseTypes(files, "types")
	if err != nil {
		t.Fatalf("parseTypes() error = %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("parseTypes() read %d types, want 2", len(out))
	}
	if out["one"].Name != "One" {
		t.Errorf("one.Name = %q, want %q", out["one"].Name, "One")
	}
}

// TestParseTypesRefusesABadlyAddedDocument covers every guard, each with
// the mistake it exists to catch.
func TestParseTypesRefusesABadlyAddedDocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files fstest.MapFS
		wants string
	}{
		{
			// The most likely real mistake: copying a document from an AWX
			// export, which carries fields this platform does not model.
			// Accepting it would produce a type that looks like it declares
			// something it does not.
			name: "a field this platform does not model",
			files: fstest.MapFS{
				"types/one.json": &fstest.MapFile{Data: []byte(`{"name":"One","namespace":"one","kind":"cloud","summary_fields":{"x":1}}`)},
			},
			wants: "decoding",
		},
		{
			// A file whose name and namespace disagree. Harmless to the
			// parser and corrosive to everybody reading the directory,
			// since the file name is how a person finds a type.
			name: "a file named after a different type",
			files: fstest.MapFS{
				"types/azure.json": &fstest.MapFile{Data: []byte(`{"name":"AWS","namespace":"aws","kind":"cloud"}`)},
			},
			wants: "should be named aws.json",
		},
		{
			name: "a document that is not JSON at all",
			files: fstest.MapFS{
				"types/one.json": &fstest.MapFile{Data: []byte(`not json`)},
			},
			wants: "decoding",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseTypes(tt.files, "types")
			if err == nil {
				t.Fatal("parseTypes() accepted it")
			}
			if !strings.Contains(err.Error(), tt.wants) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wants)
			}
		})
	}
}

// TestParseTypesReportsAMissingDirectory covers the read failure, which is
// what a mistyped embed pattern produces.
func TestParseTypesReportsAMissingDirectory(t *testing.T) {
	t.Parallel()

	if _, err := parseTypes(fstest.MapFS{}, "nowhere"); err == nil {
		t.Fatal("parseTypes() accepted a directory that does not exist")
	}
}
