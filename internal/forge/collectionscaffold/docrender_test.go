package collectionscaffold_test

import (
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// fullyPopulatedDoc is a Doc with every field of every one of its four
// record types set to a distinct non-zero value. It is the fixture both
// tests below drive, and TestRenderedDocFixtureCoversEveryField is what
// keeps it complete as pkg/collection.Doc grows.
func fullyPopulatedDoc() collection.Doc {
	return collection.Doc{
		Summary:      "Installs a package with apt.",
		Description:  "Longer prose, with a \"quoted\" phrase and an embedded\nnewline, because real documentation has both.",
		SinceVersion: "v1.2.0",
		Deprecated:   "Use pkg.install instead.",
		Params: []collection.Param{{
			Name:        "name",
			Type:        "string",
			Required:    true,
			Default:     "present",
			Choices:     []string{"present", "absent"},
			Description: "The package to install.",
			Format:      collection.ParamFormatCommand,
		}},
		Fragments: []string{"ssh_connection"},
		Returns: []collection.ReturnField{{
			Name:        "changed",
			Type:        "bool",
			Returned:    "always",
			Sample:      "true",
			Description: "Whether apt did anything.",
		}},
		Examples: []collection.Example{{
			Name:        "Install nginx",
			RunbookYAML: "- name: Install nginx\n  pkg.apt.install:\n    name: nginx\n",
		}},
		SeeAlso: []string{"pkg.apt.remove"},
	}
}

// TestGenerateRendersEveryDocField proves a Doc handed to Generate comes
// back out as Go source naming every field it was given.
//
// This is the property the generator exists for. internal/forge/
// catalogdata holds a full Doc per method and internal/archtest's
// TestCatalogDataDocsMatchTheRegistry compares it to the registered
// manifest for exact equality, so a field the renderer silently drops
// does not produce a compile error or a bad-looking file: it produces a
// generated method whose documentation is missing a piece, failing a
// test in a different package that says only that the two disagree.
func TestGenerateRendersEveryDocField(t *testing.T) {
	files, err := collectionscaffold.Generate(collectionscaffold.Config{
		Name: "pkg.apt.install",
		Doc:  fullyPopulatedDoc(),
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	source := string(files[0].Content)

	for _, name := range docFieldNames() {
		if !strings.Contains(source, name+":") {
			t.Errorf("generated source names no %s field; renderDoc has not learned about it:\n%s", name, source)
		}
	}

	// The values, not just the keys. Quoting is the part most likely to
	// go wrong, so the assertions worth making by hand are the string
	// carrying escaped quotation marks and the one carrying newlines.
	// These match the rendered literals alone rather than "Field:
	// literal", because gofmt aligns the keys of a multi-line struct
	// literal and the amount of padding is its business, not this
	// test's.
	for _, want := range []string{
		`"Longer prose, with a \"quoted\" phrase and an embedded\nnewline, because real documentation has both."`,
		`RunbookYAML: "- name: Install nginx\n  pkg.apt.install:\n    name: nginx\n"`,
		`Choices: []string{"present", "absent"}`,
		`Required: true`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated source is missing %s\ngot:\n%s", want, source)
		}
	}

	// It has to be real Go, not just text containing the right words.
	if _, err := parser.ParseFile(token.NewFileSet(), "install.go", source, parser.AllErrors); err != nil {
		t.Fatalf("generated source does not parse: %v\n%s", err, source)
	}
}

// TestRenderedDocFixtureCoversEveryField proves fullyPopulatedDoc leaves
// no field zero.
//
// Without it, TestGenerateRendersEveryDocField degrades quietly: a new
// field added to collection.Doc is absent from the fixture, so it is
// absent from the rendered output too, and the assertion that the
// output names it would fail for the right reason only by accident.
// This one fails first and says which field to add.
func TestRenderedDocFixtureCoversEveryField(t *testing.T) {
	assertNoZeroFields(t, reflect.ValueOf(fullyPopulatedDoc()), "Doc")
}

// assertNoZeroFields walks a struct value and fails for any exported
// field left at its zero value, recursing through slices into their
// element type.
func assertNoZeroFields(t *testing.T, v reflect.Value, path string) {
	t.Helper()
	typ := v.Type()
	for i := range typ.NumField() {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		value := v.Field(i)
		where := path + "." + field.Name
		if value.IsZero() {
			t.Errorf("%s is zero: add it to fullyPopulatedDoc, and teach renderDoc to emit it", where)
			continue
		}
		if value.Kind() == reflect.Slice && value.Type().Elem().Kind() == reflect.Struct {
			assertNoZeroFields(t, value.Index(0), where+"[0]")
		}
	}
}

// docFieldNames returns every exported field name of Doc and of the
// three record types it nests, which is exactly the set renderDoc has
// to know how to emit.
func docFieldNames() []string {
	var names []string
	for _, typ := range []reflect.Type{
		reflect.TypeOf(collection.Doc{}),
		reflect.TypeOf(collection.Param{}),
		reflect.TypeOf(collection.ReturnField{}),
		reflect.TypeOf(collection.Example{}),
	} {
		for i := range typ.NumField() {
			if field := typ.Field(i); field.IsExported() {
				names = append(names, field.Name)
			}
		}
	}
	return names
}

// TestGenerateOmitsAnEmptyDoc proves a method that documents nothing
// gets no Doc field at all, rather than an empty literal.
//
// The distinction matters to the equality guard in internal/archtest: a
// catalogdata entry with no Doc must generate a manifest whose Doc is
// the zero value, and an emitted collection.Doc{} would still compare
// equal, but an emitted Params: []collection.Param{} would not. Keeping
// the whole field out is the simplest way to never reach that case.
func TestGenerateOmitsAnEmptyDoc(t *testing.T) {
	files, err := collectionscaffold.Generate(collectionscaffold.Config{Name: "fs.mount"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	source := string(files[0].Content)
	if strings.Contains(source, "Doc:") {
		t.Errorf("a Config carrying no Doc still generated a Doc field:\n%s", source)
	}
}

// TestGenerateRendersAPartialDoc proves the common shape, a declared
// method carrying only a Summary, still emits just that one field.
func TestGenerateRendersAPartialDoc(t *testing.T) {
	files, err := collectionscaffold.Generate(collectionscaffold.Config{
		Name: "pkg.apt.remove",
		Doc:  collection.Doc{Summary: "Removes a package via APT."},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	source := string(files[0].Content)

	if !strings.Contains(source, `Summary: "Removes a package via APT."`) {
		t.Errorf("generated source is missing the Summary:\n%s", source)
	}
	for _, absent := range []string{"Params:", "Returns:", "Examples:", "SeeAlso:", "Description:"} {
		if strings.Contains(source, absent) {
			t.Errorf("a Summary-only Doc still emitted %s:\n%s", absent, source)
		}
	}
}
