// Tests for reading SPDX and holding it to the NTIA minimum elements.
package sbomcheck_test

import (
	"os"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sbomcheck"
)

// read parses a testdata SBOM.
func read(t *testing.T, name string) *sbomcheck.Document {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	d, err := sbomcheck.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestValidate_ARealImageSBOMOnceItsSubjectHasASupplier: Syft 1.52.0's
// SBOM of an Alpine image archive meets every element but the subject's
// supplier, which only the operator knows; once it is set (on the subject
// alone) the document passes, and the dependencies Syft knew no supplier
// for are reported apart.
func TestValidate_ARealImageSBOMOnceItsSubjectHasASupplier(t *testing.T) {
	d := read(t, "syft-1.52.0-image.spdx.json")
	r := d.Validate()
	if r.Components != 15 || len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "the subject /in/image.tar has no supplier") {
		t.Fatalf("before: %+v", r)
	}
	out, err := d.SetSubjectSupplier("Organization: Example Co", "pleiades")
	if err != nil {
		t.Fatal(err)
	}
	again, err := sbomcheck.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	r = again.Validate()
	if len(r.Problems) != 0 || r.Dependencies != 14 || strings.Join(r.MissingSupplier, ",") != "busybox,busybox-binsh,ssl_client" {
		t.Fatalf("after: %+v", r)
	}
	suppliers := 0
	for _, p := range again.Packages {
		if p.Supplier == "Organization: Example Co" {
			suppliers++
		}
	}
	if suppliers != 1 {
		t.Errorf("%d packages carry the operator's supplier, want only the subject", suppliers)
	}
	if got := again.Creators[len(again.Creators)-1]; got != "Tool: pleiades" {
		t.Errorf("creators = %v", again.Creators)
	}
	if !strings.Contains(string(out), `"licenseListVersion"`) {
		t.Error("the edit dropped a field it does not read")
	}
}

// TestValidate_NamesEachMissingElement: a directory scanned with no
// version for its subject, and synthetic documents each missing one
// element.
func TestValidate_NamesEachMissingElement(t *testing.T) {
	r := read(t, "syft-1.52.0-dir.spdx.json").Validate()
	if got := strings.Join(r.Problems, "\n"); !strings.Contains(got, "with no version: /src") || !strings.Contains(got, "the subject /src has no supplier") {
		t.Errorf("a subject with no version or supplier: %v", r.Problems)
	}
	base := func(packages, relationships, creation string) string {
		return `{"spdxVersion":"SPDX-2.3","name":"x","documentNamespace":"https://example.com/x","creationInfo":` + creation +
			`,"packages":[` + packages + `],"relationships":[` + relationships + `]}`
	}
	subject := `{"SPDXID":"SPDXRef-root","name":"app","versionInfo":"1.0","supplier":"Organization: Example Co"}`
	describes := `{"spdxElementId":"SPDXRef-DOCUMENT","relationshipType":"DESCRIBES","relatedSpdxElement":"SPDXRef-root"}`
	contains := `{"spdxElementId":"SPDXRef-root","relationshipType":"CONTAINS","relatedSpdxElement":"SPDXRef-a"}`
	good := `{"created":"2026-10-15T00:00:00Z","creators":["Tool: syft"]}`
	dep := func(fields string) string { return `{"SPDXID":"SPDXRef-a"` + fields + `}` }
	for name, tc := range map[string]struct {
		doc  string
		want string
	}{
		"no author":       {base(subject, describes, `{"created":"2026-10-15T00:00:00Z"}`), "names no author"},
		"no timestamp":    {base(subject, describes, `{"creators":["Tool: x"]}`), "has no timestamp"},
		"no subject":      {base(subject, ``, good), "describes nothing"},
		"no version":      {base(subject+","+dep(`,"name":"a","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:npm/a@1"}]`), describes+","+contains, good), "with no version: a"},
		"no identifier":   {base(subject+","+dep(`,"name":"a","versionInfo":"1"`), describes+","+contains, good), "with no purl or CPE: a"},
		"no relationship": {base(subject+","+dep(`,"name":"a","versionInfo":"1","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:npm/a@1"}]`), describes, good), "with no dependency relationship: a"},
	} {
		d, err := sbomcheck.Parse([]byte(tc.doc))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := strings.Join(d.Validate().Problems, "\n"); !strings.Contains(got, tc.want) {
			t.Errorf("%s: problems %q, want %q", name, got, tc.want)
		}
	}
}

// TestParse_RefusesWhatIsNotSPDX23 and an edit with no subject or a
// malformed supplier.
func TestParse_RefusesWhatIsNotSPDX23(t *testing.T) {
	for src, want := range map[string]string{
		`{"spdxVersion":"SPDX-2.2"}`:                           `not SPDX-2.3`,
		`{"bomFormat":"CycloneDX"}`:                            `not SPDX-2.3`,
		`{"spdxVersion":"SPDX-2.3"} {}`:                        "more than one JSON document",
		`{"spdxVersion":"SPDX-2.3","packages":[{"name":"a"}]}`: "has no SPDXID",
		`{"spdxVersion":"SPDX-2.3","packages":["a"]}`:          "is not an object",
		`[`: "not JSON",
	} {
		if _, err := sbomcheck.Parse([]byte(src)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%s) = %v, want %q", src, err, want)
		}
	}
	d := read(t, "syft-1.52.0-image.spdx.json")
	if _, err := d.SetSubjectSupplier("Example Co", "pleiades"); err == nil {
		t.Error("a supplier without its kind was accepted")
	}
	empty, _ := sbomcheck.Parse([]byte(`{"spdxVersion":"SPDX-2.3"}`))
	if _, err := empty.SetSubjectSupplier("Organization: x", "pleiades"); err == nil {
		t.Error("a supplier was set on a document with no subject")
	}
}

// TestValidate_ASubjectMustBeAPackage: a DESCRIBES relationship naming an
// element the document holds no package for is a problem, beside the
// subject it does hold, and an edit refuses to set a supplier on it
// rather than skip it.
func TestValidate_ASubjectMustBeAPackage(t *testing.T) {
	doc := `{"spdxVersion":"SPDX-2.3","name":"x","documentNamespace":"https://example.com/x",` +
		`"creationInfo":{"created":"2026-10-15T00:00:00Z","creators":["Tool: syft"]},` +
		`"packages":[{"SPDXID":"SPDXRef-root","name":"app","versionInfo":"1.0","supplier":"Organization: Example Co"}],` +
		`"relationships":[{"spdxElementId":"SPDXRef-DOCUMENT","relationshipType":"DESCRIBES","relatedSpdxElement":"SPDXRef-root"},` +
		`{"spdxElementId":"SPDXRef-DOCUMENT","relationshipType":"DESCRIBES","relatedSpdxElement":"SPDXRef-gone"}]}`
	d, err := sbomcheck.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(d.Validate().Problems, "\n"); !strings.Contains(got, "1 DESCRIBES relationship(s) name no package in the document: SPDXRef-gone") {
		t.Errorf("problems = %q", got)
	}
	if _, err := d.SetSubjectSupplier("Organization: Example Co", "pleiades"); err == nil || !strings.Contains(err.Error(), "SPDXRef-gone") {
		t.Errorf("SetSubjectSupplier = %v", err)
	}
}

// TestValidate_AProblemBoundsTheNamesItLists: a name the document gives
// reaches a problem bounded and escaped, however long it is and whatever
// it holds.
func TestValidate_AProblemBoundsTheNamesItLists(t *testing.T) {
	long := "evil\x1b[2J" + strings.Repeat("n", 100000)
	doc := `{"spdxVersion":"SPDX-2.3","name":"x","documentNamespace":"https://example.com/x",` +
		`"creationInfo":{"created":"2026-10-15T00:00:00Z","creators":["Tool: syft"]},` +
		`"packages":[{"SPDXID":"SPDXRef-root","name":"app","versionInfo":"1.0","supplier":"Organization: Example Co"},` +
		`{"SPDXID":"SPDXRef-a","name":"` + strings.ReplaceAll(long, "\x1b", `\u001b`) + `"}],` +
		`"relationships":[{"spdxElementId":"SPDXRef-DOCUMENT","relationshipType":"DESCRIBES","relatedSpdxElement":"SPDXRef-root"}]}`
	d, err := sbomcheck.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	problems := d.Validate().Problems
	if len(problems) == 0 {
		t.Fatal("a dependency with no version passed")
	}
	for _, p := range problems {
		if len(p) > 1024 || strings.ContainsRune(p, 0x1b) {
			t.Errorf("a problem of %d bytes, escape held: %v", len(p), strings.ContainsRune(p, 0x1b))
		}
	}
}
